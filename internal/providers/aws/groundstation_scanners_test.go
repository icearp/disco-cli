package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/groundstation"
	gstypes "github.com/aws/aws-sdk-go-v2/service/groundstation/types"
	"github.com/icearp/disco-cli/store"
)

// gsTestTotal is every row of the fixture: two of each of the four types,
// one per page.
const gsTestTotal = 2 * 4

var (
	gsTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: groundstation:List")
	gsTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	gsTestBad      = apiErr("InvalidParameterException", "bad")
	gsTestGone     = apiErr("ResourceNotFoundException", "gone")
	gsTestCreated  = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

// gsTestKind is one listing: its op, the type it stores, its ARN's resource
// path before the id, and the noun its warnings name its elements by.
type gsTestKind struct {
	op, typ, path, what string
}

func gsTestKinds() []gsTestKind {
	return []gsTestKind{
		{"ListConfigs", TypeGroundStationConfig, "config/tracking/", "configs"},
		{"ListDataflowEndpointGroups", TypeGroundStationDataflowEndpointGroup, "dataflow-endpoint-group/", "dataflow endpoint groups"},
		{"ListMissionProfiles", TypeGroundStationMissionProfile, "mission-profile/", "mission profiles"},
		{"ListEphemerides", TypeGroundStationEphemeris, "ephemeris/", "ephemerides"},
	}
}

func gsTestKindOf(t *testing.T, op string) gsTestKind {
	t.Helper()
	for _, k := range gsTestKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no Ground Station listing", op)
	return gsTestKind{}
}

func (k gsTestKind) id(suffix string) string {
	return k.typ[len("aws:ground-station:"):] + "-" + suffix
}

func (k gsTestKind) arn(suffix string) string {
	return "arn:aws:groundstation:" + testRegion + ":" + testAccountID + ":" + k.path + k.id(suffix)
}

// name is the row's Name: a dataflow endpoint group is named by its id, an
// element without a Name.
func (k gsTestKind) name(suffix string) string {
	if k.typ == TypeGroundStationDataflowEndpointGroup {
		return k.id(suffix)
	}
	return "name-" + k.id(suffix)
}

// element is k's listed element with suffix, under arn (nil for an element
// listed without one); an ephemeris, which has no ARN, lacks its id instead.
func (k gsTestKind) element(arn *string, suffix string) any {
	id, name := sdkaws.String(k.id(suffix)), sdkaws.String(k.name(suffix))
	switch k.typ {
	case TypeGroundStationConfig:
		return gstypes.ConfigListItem{ConfigArn: arn, ConfigId: id, ConfigType: gstypes.ConfigCapabilityTypeTracking, Name: name}
	case TypeGroundStationDataflowEndpointGroup:
		return gstypes.DataflowEndpointListItem{DataflowEndpointGroupArn: arn, DataflowEndpointGroupId: id}
	case TypeGroundStationMissionProfile:
		return gstypes.MissionProfileListItem{MissionProfileArn: arn, MissionProfileId: id, Name: name, Region: sdkaws.String(testRegion)}
	default:
		if arn == nil {
			id = nil
		}
		return gstypes.EphemerisItem{
			EphemerisId: id, Name: name, Status: gstypes.EphemerisStatusEnabled, EphemerisType: gstypes.EphemerisTypeOem,
			CreationTime: &gsTestCreated, Enabled: sdkaws.Bool(true), Priority: sdkaws.Int32(2),
		}
	}
}

// page is k's output holding element, and next.
func (k gsTestKind) page(element any, next *string) any {
	switch e := element.(type) {
	case gstypes.ConfigListItem:
		return &groundstation.ListConfigsOutput{NextToken: next, ConfigList: []gstypes.ConfigListItem{e}}
	case gstypes.DataflowEndpointListItem:
		return &groundstation.ListDataflowEndpointGroupsOutput{NextToken: next, DataflowEndpointGroupList: []gstypes.DataflowEndpointListItem{e}}
	case gstypes.MissionProfileListItem:
		return &groundstation.ListMissionProfilesOutput{NextToken: next, MissionProfileList: []gstypes.MissionProfileListItem{e}}
	default:
		return &groundstation.ListEphemeridesOutput{NextToken: next, Ephemerides: []gstypes.EphemerisItem{e.(gstypes.EphemerisItem)}}
	}
}

// gsTestCalls records the calls of a scan and answers ListTagsForResource,
// by ARN, and DescribeEphemeris, by id, with tags or an error.
type gsTestCalls struct {
	mu         sync.Mutex
	maxResults map[string][]int32
	calls      map[string]int
	ephInputs  []groundstation.ListEphemeridesInput
	tagARNs    []string
	ephIDs     []string
	tags       map[string]map[string]string // by ARN, or by id for an ephemeris
	tagErrs    map[string]error             // likewise
}

// newGSTestCalls tags every "-tagged" row with its type.
func newGSTestCalls() *gsTestCalls {
	c := &gsTestCalls{maxResults: map[string][]int32{}, calls: map[string]int{}, tags: map[string]map[string]string{}, tagErrs: map[string]error{}}
	for _, k := range gsTestKinds() {
		c.tags[k.tagKey("tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

// tagKey is what the tag read of k's row with suffix is sent: its ARN, or
// an ephemeris's id.
func (k gsTestKind) tagKey(suffix string) string {
	if k.typ == TypeGroundStationEphemeris {
		return k.id(suffix)
	}
	return k.arn(suffix)
}

func (c *gsTestCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[op]++
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		c.maxResults[op] = append(c.maxResults[op], *n)
	}
	if e, ok := in.(*groundstation.ListEphemeridesInput); ok {
		c.ephInputs = append(c.ephInputs, *e)
	}
}

func (c *gsTestCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

func (c *gsTestCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *gsTestCalls) sortedEphemerisIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.ephIDs))
}

// tagsOf answers the tag read sent key.
func (c *gsTestCalls) tagsOf(key string) (map[string]string, error) {
	if err := c.tagErrs[key]; err != nil {
		return nil, err
	}
	return c.tags[key], nil
}

func (c *gsTestCalls) listTags(in any) (any, error) {
	arn := sv(in.(*groundstation.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	tags, err := c.tagsOf(arn)
	if err != nil {
		return nil, err
	}
	return &groundstation.ListTagsForResourceOutput{Tags: tags}, nil
}

func (c *gsTestCalls) describeEphemeris(in any) (any, error) {
	id := sv(in.(*groundstation.DescribeEphemerisInput).EphemerisId)
	c.record("DescribeEphemeris", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ephIDs = append(c.ephIDs, id)
	tags, err := c.tagsOf(id)
	if err != nil {
		return nil, err
	}
	return &groundstation.DescribeEphemerisOutput{EphemerisId: sdkaws.String(id), Name: sdkaws.String("described-" + id), Tags: tags}, nil
}

// twoPages answers k's op with "-tagged" on the first page and "-bare" on
// the second.
func (c *gsTestCalls) twoPages(k gsTestKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.page(k.element(sdkaws.String(k.arn(suffix)), suffix), next), nil
	}
}

// gsTestHandlers answers every op the scan calls from c.
func gsTestHandlers(c *gsTestCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags, "DescribeEphemeris": c.describeEphemeris}
	for _, k := range gsTestKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// gsTestScan runs the scan's real entry point against handlers; every op
// they do not answer is denied.
func gsTestScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, gsTestDenied)
	return scanGroundStation(ctx, acct, testRegion, st, testScanID)
}

// gsListTagsARNs is the ARN of every config, dataflow endpoint group and
// mission profile of the fixture, sorted.
func gsListTagsARNs() []string {
	var arns []string
	for _, k := range gsTestKinds() {
		if k.typ != TypeGroundStationEphemeris {
			arns = append(arns, k.arn("tagged"), k.arn("bare"))
		}
	}
	slices.Sort(arns)
	return arns
}

func gsWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

func gsJSONValue(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	return m
}

// Every listing stores both its pages, each row under its listed ARN (an
// ephemeris under the ARN built from its id), in the scanned account's name,
// with its listed element as attributes and the tags its tag read returns
// (nil for none). Only an ephemeris has a Status and CreatedAt. The
// ListTagsForResource ARNs are exactly those of the configs, dataflow
// endpoint groups and mission profiles; DescribeEphemeris is sent exactly the
// ephemeris ids. Every listing is sent MaxResults 100. No error, no warning.
func TestScanGroundStation_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newGSTestCalls()

	total, inserted, err := gsTestScan(context.Background(), st, gsTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != gsTestTotal || inserted != gsTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, gsTestTotal, gsTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range gsTestKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", k.typ, len(rows))
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := k.arn(suffix)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			wantStatus, wantCreated := "", ""
			if k.typ == TypeGroundStationEphemeris {
				wantStatus, wantCreated = "ENABLED", sv(tp(&gsTestCreated))
			}
			if sv(r.Name) != k.name(suffix) || sv(r.Region) != testRegion || sv(r.Status) != wantStatus || sv(r.CreatedAt) != wantCreated || r.ManagedByProvider {
				t.Errorf("%s Name, Region, Status, CreatedAt, Managed = %q, %q, %q, %q, %v; want %q, %q, %q, %q, false",
					arn, sv(r.Name), sv(r.Region), sv(r.Status), sv(r.CreatedAt), r.ManagedByProvider, k.name(suffix), testRegion, wantStatus, wantCreated)
			}
			if wantStatus == "" && r.Status != nil {
				t.Errorf("%s Status = %q; want nil", arn, *r.Status)
			}
			if got, want := sv(r.AccountName), newTestAccount(testAccountID).Name; got != want {
				t.Errorf("%s AccountName = %q; want %q", arn, got, want)
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("%s attrs: %v", arn, err)
			}
			if listed := gsJSONValue(t, k.element(sdkaws.String(arn), suffix)); !reflect.DeepEqual(attrs, listed) {
				t.Errorf("%s attrs = %s; want its listed element %v", arn, r.AttributesJSON, listed)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		if got := calls.maxResults[k.op]; !slices.Equal(got, []int32{100, 100}) {
			t.Errorf("%s MaxResults = %v; want [100 100]", k.op, got)
		}
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, gsListTagsARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, gsListTagsARNs())
	}
	eph := gsTestKindOf(t, "ListEphemerides")
	if got, want := calls.sortedEphemerisIDs(), []string{eph.id("bare"), eph.id("tagged")}; !slices.Equal(got, want) {
		t.Errorf("DescribeEphemeris ids = %v; want %v", got, want)
	}
}

// ListEphemerides matches on expiration time inside the window, so every
// page is sent a window reaching back past any real expiration and far
// ahead of any upload, every status, and no satellite (an ephemeris bound to
// none would be missed).
func TestScanGroundStation_EphemeridesSendWideWindowAndEveryStatus(t *testing.T) {
	st := newTestStore(t)
	calls := newGSTestCalls()
	if _, _, err := gsTestScan(context.Background(), st, gsTestHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(calls.ephInputs) != 2 {
		t.Fatalf("ListEphemerides called %d times; want 2 (one per page)", len(calls.ephInputs))
	}
	earliest := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	latest := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, in := range calls.ephInputs {
		if in.StartTime == nil || in.StartTime.After(earliest) {
			t.Errorf("call %d StartTime = %v; want <= %v", i, in.StartTime, earliest)
		}
		if in.EndTime == nil || !in.EndTime.After(latest) {
			t.Errorf("call %d EndTime = %v; want after %v", i, in.EndTime, latest)
		}
		if in.SatelliteId != nil || in.EphemerisType != "" {
			t.Errorf("call %d SatelliteId, EphemerisType = %v, %q; want unset", i, in.SatelliteId, in.EphemerisType)
		}
		want := []gstypes.EphemerisStatus{
			gstypes.EphemerisStatusValidating, gstypes.EphemerisStatusInvalid, gstypes.EphemerisStatusError,
			gstypes.EphemerisStatusEnabled, gstypes.EphemerisStatusDisabled, gstypes.EphemerisStatusExpired,
		}
		if got := slices.Sorted(slices.Values(in.StatusList)); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
			t.Errorf("call %d StatusList = %v; want %v", i, in.StatusList, want)
		}
	}
}

// A service-managed ephemeris is stored provider-managed, and its tags are
// not read; an uploaded one is read.
func TestScanGroundStation_ServiceManagedEphemerisNotRead(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newGSTestCalls()
	eph := gsTestKindOf(t, "ListEphemerides")
	handlers := gsTestHandlers(calls)
	twoPages := handlers["ListEphemerides"]
	handlers["ListEphemerides"] = func(in any) (any, error) {
		out, err := twoPages(in)
		page := out.(*groundstation.ListEphemeridesOutput)
		if sv(page.Ephemerides[0].EphemerisId) == eph.id("tagged") {
			page.Ephemerides[0].EphemerisType = gstypes.EphemerisTypeServiceManaged
		}
		return out, err
	}

	if _, _, err := gsTestScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	rows := rowsByNativeID(t, st, TypeGroundStationEphemeris)
	if r, ok := rows[eph.arn("tagged")]; !ok || !r.ManagedByProvider || r.TagsJSON != nil {
		t.Errorf("SERVICE_MANAGED ephemeris stored=%v managed=%v tags=%v; want stored, managed, untagged", ok, r.ManagedByProvider, r.TagsJSON)
	}
	if r, ok := rows[eph.arn("bare")]; !ok || r.ManagedByProvider {
		t.Errorf("OEM ephemeris stored=%v managed=%v; want stored and not managed", ok, r.ManagedByProvider)
	}
	if got, want := calls.sortedEphemerisIDs(), []string{eph.id("bare")}; !slices.Equal(got, want) {
		t.Errorf("DescribeEphemeris ids = %v; want %v", got, want)
	}
}

// An ephemeris listed without a Name is named by its id, and one without a
// Status has none.
func TestScanGroundStation_EphemerisWithoutNameOrStatus(t *testing.T) {
	st := newTestStore(t)
	eph := gsTestKindOf(t, "ListEphemerides")
	handlers := gsTestHandlers(newGSTestCalls())
	twoPages := handlers["ListEphemerides"]
	handlers["ListEphemerides"] = func(in any) (any, error) {
		out, err := twoPages(in)
		e := &out.(*groundstation.ListEphemeridesOutput).Ephemerides[0]
		e.Name, e.Status = nil, ""
		return out, err
	}

	if _, _, err := gsTestScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	r, ok := rowsByNativeID(t, st, TypeGroundStationEphemeris)[eph.arn("bare")]
	if !ok {
		t.Fatalf("%s missing", eph.arn("bare"))
	}
	if sv(r.Name) != eph.id("bare") || r.Status != nil {
		t.Errorf("Name, Status = %q, %v; want %q, nil", sv(r.Name), r.Status, eph.id("bare"))
	}
}

// Every listing writes its first page, with its tags, before it reads the
// second.
func TestScanGroundStation_PagesWrittenAsRead(t *testing.T) {
	st := newTestStore(t)
	handlers := gsTestHandlers(newGSTestCalls())
	var (
		mu          sync.Mutex
		storedFirst = map[string]bool{}
	)
	for _, k := range gsTestKinds() {
		list := handlers[k.op]
		handlers[k.op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				r, err := st.GetResource(store.ResourceID("aws", testAccountID, k.arn("tagged")))
				mu.Lock()
				storedFirst[k.op] = err == nil && r != nil && r.TagsJSON != nil
				mu.Unlock()
			}
			return list(in)
		}
	}

	if _, _, err := gsTestScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, k := range gsTestKinds() {
		if !storedFirst[k.op] {
			t.Errorf("%s: %s not stored with its tags when the second page was read", k.op, k.arn("tagged"))
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanGroundStation_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := gsTestScan(context.Background(), st, gsTestHandlers(newGSTestCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := gsTestScan(context.Background(), st, gsTestHandlers(newGSTestCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != gsTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, gsTestTotal)
	}
}

// Empty listings: each listed once, nothing stored, no tag read, no error,
// no warning.
func TestScanGroundStation_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newGSTestCalls()
	handlers := gsTestHandlers(calls)
	empty := map[string]any{
		"ListConfigs":                &groundstation.ListConfigsOutput{},
		"ListDataflowEndpointGroups": &groundstation.ListDataflowEndpointGroupsOutput{},
		"ListMissionProfiles":        &groundstation.ListMissionProfilesOutput{},
		"ListEphemerides":            &groundstation.ListEphemeridesOutput{},
	}
	for op, out := range empty {
		handlers[op] = func(in any) (any, error) {
			calls.record(op, in)
			return out, nil
		}
	}

	total, inserted, err := gsTestScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for op := range empty {
		if n := calls.count(op); n != 1 {
			t.Errorf("%s called %d times; want 1", op, n)
		}
	}
	for _, op := range []string{"ListTagsForResource", "DescribeEphemeris"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
}

// A failed tag read. ResourceNotFoundException is modeled by both tag ops,
// so a resource deleted since it was listed is dropped silently. A denial
// keeps the row untagged and warns once under the tag op with the scan's
// scope; a throttle keeps it and is transient; any other error keeps it and
// is an error.
func TestScanGroundStation_TagReadFailures(t *testing.T) {
	for _, c := range []struct {
		name      string
		op        string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      string
	}{
		{"config/not found", "ListConfigs", gsTestGone, -1, "", gsTestTotal - 1, false, ""},
		{"dataflow endpoint group/denied", "ListDataflowEndpointGroups", gsTestDenied, -1, "", gsTestTotal, true, "groundstation:ListTagsForResource"},
		{"mission profile/transient", "ListMissionProfiles", gsTestThrottle, outcomeTransient, "ThrottlingException", gsTestTotal, true, ""},
		{"mission profile/other error", "ListMissionProfiles", gsTestBad, outcomeError, "InvalidParameterException", gsTestTotal, true, ""},
		{"ephemeris/not found", "ListEphemerides", gsTestGone, -1, "", gsTestTotal - 1, false, ""},
		{"ephemeris/denied", "ListEphemerides", gsTestDenied, -1, "", gsTestTotal, true, "groundstation:DescribeEphemeris"},
		{"ephemeris/transient", "ListEphemerides", gsTestThrottle, outcomeTransient, "ThrottlingException", gsTestTotal, true, ""},
		{"ephemeris/other error", "ListEphemerides", gsTestBad, outcomeError, "InvalidParameterException", gsTestTotal, true, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newGSTestCalls()
			k := gsTestKindOf(t, c.op)
			calls.tagErrs[k.tagKey("tagged")] = c.err

			total, _, err := gsTestScan(context.Background(), st, gsTestHandlers(calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			rows := rowsByNativeID(t, st, k.typ)
			r, ok := rows[k.arn("tagged")]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if _, ok := rows[k.arn("bare")]; !ok {
				t.Errorf("sibling %s missing", k.arn("bare"))
			}
			w := warns()
			var want []string
			if c.warn != "" {
				want = []string{c.warn}
			}
			if got := gsWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// Denied tag reads in three phases send one warning per tag op for the
// whole scan; the rows are kept.
func TestScanGroundStation_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newGSTestCalls()
	calls.tagErrs[gsTestKindOf(t, "ListConfigs").tagKey("bare")] = gsTestDenied
	calls.tagErrs[gsTestKindOf(t, "ListMissionProfiles").tagKey("tagged")] = gsTestDenied
	eph := gsTestKindOf(t, "ListEphemerides")
	calls.tagErrs[eph.tagKey("tagged")] = gsTestDenied
	calls.tagErrs[eph.tagKey("bare")] = gsTestDenied

	total, _, err := gsTestScan(context.Background(), st, gsTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != gsTestTotal {
		t.Errorf("total = %d; want %d", total, gsTestTotal)
	}
	want := []string{"groundstation:ListTagsForResource", "groundstation:DescribeEphemeris"}
	if got := gsWarnOps(warns()); !slices.Equal(got, want) {
		t.Errorf("warnings = %v; want %v", got, want)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanGroundStation_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "groundstation:ListTagsForResource")
	calls := newGSTestCalls()
	calls.tagErrs[gsTestKindOf(t, "ListConfigs").tagKey("bare")] = gsTestThrottle
	calls.tagErrs[gsTestKindOf(t, "ListMissionProfiles").tagKey("tagged")] = gsTestBad

	_, _, err := gsTestScan(context.Background(), st, gsTestHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidParameterException")
	if *warns != 1 {
		t.Errorf("groundstation:ListTagsForResource warnings = %d; want 1", *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored.
func TestScanGroundStation_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeGroundStationDataflowEndpointGroup)
	calls := newGSTestCalls()
	calls.tagErrs[gsTestKindOf(t, "ListDataflowEndpointGroups").tagKey("tagged")] = gsTestDenied

	total, _, err := gsTestScan(context.Background(), st, gsTestHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListMissionProfiles", "ListEphemerides"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0 after the failed write", op, n)
		}
	}
	if total != 2 {
		t.Errorf("total = %d; want the 2 configs", total)
	}
	if n := len(rowsByNativeID(t, st, TypeGroundStationConfig)); n != 2 {
		t.Errorf("configs = %d; want 2", n)
	}
}

// A listing's failed later page keeps the page read before it, and its
// error does not stop the other phases. Every listing's first page denied
// is warned about under its op, and the other phases run too.
func TestScanGroundStation_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	type listFailure struct {
		name      string
		op        string
		err       error
		laterPage bool
		wantKept  int
		wantWarn  bool
		wantErr   bool
	}
	var cases []listFailure
	for _, k := range gsTestKinds() {
		cases = append(cases,
			listFailure{"later page fails/" + k.op, k.op, gsTestBad, true, 1, false, true},
			listFailure{"later page denied/" + k.op, k.op, gsTestDenied, true, 1, true, false},
			listFailure{"denied/" + k.op, k.op, gsTestDenied, false, 0, true, false})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newGSTestCalls()
			handlers := gsTestHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if later := bacField[*string](in, "NextToken") != nil; later == c.laterPage {
					calls.record(c.op, in)
					return nil, c.err
				}
				return list(in)
			}

			total, _, err := gsTestScan(context.Background(), st, handlers)
			if c.wantErr {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "InvalidParameterException")
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			var wantWarn []string
			if c.wantWarn {
				wantWarn = []string{"groundstation:" + c.op}
			}
			if got := gsWarnOps(warns()); !slices.Equal(got, wantWarn) {
				t.Errorf("warnings = %v; want %v", got, wantWarn)
			}
			if want := gsTestTotal - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			for _, k := range gsTestKinds() {
				rows := rowsByNativeID(t, st, k.typ)
				if k.op != c.op {
					if len(rows) != 2 {
						t.Errorf("%s rows = %d; want 2, its phase run", k.typ, len(rows))
					}
					continue
				}
				if _, ok := rows[k.arn("tagged")]; ok != (c.wantKept == 1) {
					t.Errorf("%s stored = %v; want %v", k.arn("tagged"), ok, c.wantKept == 1)
				}
				if _, ok := rows[k.arn("bare")]; ok {
					t.Errorf("%s stored; its page failed", k.arn("bare"))
				}
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanGroundStation_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	calls := newGSTestCalls()
	handlers := gsTestHandlers(calls)
	twoPages := handlers["ListMissionProfiles"]
	handlers["ListMissionProfiles"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*groundstation.ListMissionProfilesOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, _, err := gsTestScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != gsTestTotal {
		t.Errorf("total = %d; want %d", total, gsTestTotal)
	}
	if n := len(rowsByNativeID(t, st, TypeGroundStationMissionProfile)); n != 2 {
		t.Errorf("mission profiles = %d; want 2", n)
	}
}

// An element listed without its ARN (an ephemeris without its id) is not
// stored, not read, and warned about once under its listing with the scan's
// scope, the warning naming the listing's elements and the element; the rest
// of the listing is stored.
func TestScanGroundStation_ElementWithoutIdentifier(t *testing.T) {
	type variant struct {
		name    string
		element any
		key     string
	}
	for _, k := range gsTestKinds() {
		variants := []variant{
			{"named", k.element(nil, "noid"), k.name("noid")},
			{"no identifier", gsUnidentified(k.element(nil, "noid"), false), noIdentifier},
		}
		// Only configs and mission profiles carry both a Name and an id: a
		// dataflow endpoint group has no Name, and an ephemeris is listed
		// without an ARN by lacking its id.
		if k.typ == TypeGroundStationConfig || k.typ == TypeGroundStationMissionProfile {
			variants = append(variants, variant{"id only", gsUnidentified(k.element(nil, "noid"), true), k.id("noid")})
		}
		for _, v := range variants {
			t.Run(k.op+"/"+v.name, func(t *testing.T) {
				gsCheckElementWithoutIdentifier(t, k, v.element, v.key)
			})
		}
	}
}

// gsUnidentified is element without its Name, and without its id unless
// keepID.
func gsUnidentified(element any, keepID bool) any {
	switch e := element.(type) {
	case gstypes.ConfigListItem:
		e.Name = nil
		if !keepID {
			e.ConfigId = nil
		}
		return e
	case gstypes.DataflowEndpointListItem:
		if !keepID {
			e.DataflowEndpointGroupId = nil
		}
		return e
	case gstypes.MissionProfileListItem:
		e.Name = nil
		if !keepID {
			e.MissionProfileId = nil
		}
		return e
	default:
		eph := e.(gstypes.EphemerisItem)
		eph.Name = nil
		return eph
	}
}

// gsCheckElementWithoutIdentifier scans with element, listed without an
// ARN (or an ephemeris without an id), on k's second page, and checks that
// it alone is not stored or tag-read and that one warning names it by key.
func gsCheckElementWithoutIdentifier(t *testing.T, k gsTestKind, element any, key string) {
	t.Helper()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newGSTestCalls()
	handlers := gsTestHandlers(calls)
	twoPages := handlers[k.op]
	handlers[k.op] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			calls.record(k.op, in)
			return k.page(element, nil), nil
		}
		return twoPages(in)
	}

	total, _, err := gsTestScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := gsTestTotal - 1; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	if n := len(rowsByNativeID(t, st, k.typ)); n != 1 {
		t.Errorf("%s rows = %d; want the 1 with an identifier", k.typ, n)
	}
	wantARNs, wantIDs := gsListTagsARNs(), []string{"ephemeris-bare", "ephemeris-tagged"}
	if k.typ == TypeGroundStationEphemeris {
		wantIDs = []string{"ephemeris-tagged"}
	} else {
		wantARNs = slices.DeleteFunc(wantARNs, func(a string) bool { return a == k.arn("bare") })
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, wantARNs)
	}
	if got := calls.sortedEphemerisIDs(); !slices.Equal(got, wantIDs) {
		t.Errorf("DescribeEphemeris ids = %v; want %v", got, wantIDs)
	}
	w := warns()
	if got := gsWarnOps(w); !slices.Equal(got, []string{"groundstation:" + k.op}) {
		t.Fatalf("warnings = %v; want one under groundstation:%s", w, k.op)
	}
	if x := w[0]; x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, k.what) || !strings.Contains(x.Message, key) {
		t.Errorf("warning = %+v; want Scope %s/%s, naming %s and %q", x, testAccountID, testRegion, k.what, key)
	}
}

// In GovCloud a listed ARN reaches ListTagsForResource unchanged, and an
// ephemeris's NativeID is built in the account's partition and region while
// DescribeEphemeris is sent its id.
func TestScanGroundStation_TagReadsKeepPartitionARNs(t *testing.T) {
	const partition, region = "aws-us-gov", "us-gov-west-1"
	st := newTestStore(t)
	calls := newGSTestCalls()
	handlers := gsTestHandlers(calls)
	cfg := gsTestKindOf(t, "ListConfigs")
	govConfig := "arn:" + partition + ":groundstation:" + region + ":" + testAccountID + ":config/tracking/" + cfg.id("gov")
	calls.tags[govConfig] = map[string]string{"k": "config"}
	handlers["ListConfigs"] = func(in any) (any, error) {
		calls.record("ListConfigs", in)
		return cfg.page(cfg.element(sdkaws.String(govConfig), "gov"), nil), nil
	}
	eph := gsTestKindOf(t, "ListEphemerides")
	govEphemeris := "arn:" + partition + ":groundstation:" + region + ":" + testAccountID + ":ephemeris/" + eph.id("tagged")
	acct := newTestAccount(testAccountID)
	acct.Partition = partition
	acct.cfg = opStubConfig(handlers, gsTestDenied)

	if _, _, err := scanGroundStation(context.Background(), acct, region, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Contains(calls.sortedTagARNs(), govConfig) {
		t.Errorf("ListTagsForResource ARNs %v lack %s", calls.sortedTagARNs(), govConfig)
	}
	assertTagsMap(t, rowsByNativeID(t, st, TypeGroundStationConfig)[govConfig].TagsJSON, map[string]string{"k": "config"})
	r, ok := rowsByNativeID(t, st, TypeGroundStationEphemeris)[govEphemeris]
	if !ok {
		t.Fatalf("ephemeris not stored under %s", govEphemeris)
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeGroundStationEphemeris})
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanGroundStation_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newGSTestCalls()
	handlers := gsTestHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListDataflowEndpointGroups"]
	handlers["ListDataflowEndpointGroups"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := gsTestScan(ctx, st, handlers)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	for _, op := range []string{"ListMissionProfiles", "ListEphemerides"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and no later phase runs.
func TestScanGroundStation_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "groundstation.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := gsTestHandlers(newGSTestCalls())
		handlers["ListEphemerides"] = func(any) (any, error) { return nil, nx }
		total, _, err := gsTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := gsTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newGSTestCalls()
		calls.tagErrs[gsTestKindOf(t, "ListConfigs").tagKey("tagged")] = nx
		_, _, err := gsTestScan(context.Background(), st, gsTestHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newGSTestCalls()
		handlers := map[string]func(in any) (any, error){}
		for _, k := range gsTestKinds() {
			handlers[k.op] = func(in any) (any, error) {
				calls.record(k.op, in)
				return nil, nx
			}
		}
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(handlers, nx)
		_, _, err := scanGroundStation(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("ListDataflowEndpointGroups"); n != 0 {
			t.Errorf("ListDataflowEndpointGroups called %d times; want 0 once the region proved unavailable", n)
		}
	})
}
