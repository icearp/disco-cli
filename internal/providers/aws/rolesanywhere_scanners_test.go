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
	"github.com/aws/aws-sdk-go-v2/service/rolesanywhere"
	rolesanywheretypes "github.com/aws/aws-sdk-go-v2/service/rolesanywhere/types"
	"github.com/icearp/disco-cli/store"
)

// raTestTotal is every row of the fixture: two of each of the four types,
// one per page.
const raTestTotal = 2 * 4

var (
	raTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: rolesanywhere:List")
	raTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	raTestBad      = apiErr("ValidationException", "bad")
	raTestGone     = apiErr("ResourceNotFoundException", "gone")
	raTestCreated  = time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
)

// raTestKind is one listing: its op, the type it stores, its ARN's resource
// kind, and the noun its warnings name its elements by.
type raTestKind struct {
	op, typ, kind, what string
}

func raTestKinds() []raTestKind {
	return []raTestKind{
		{"ListCrls", TypeRolesAnywhereCRL, "crl", "CRLs"},
		{"ListProfiles", TypeRolesAnywhereProfile, "profile", "profiles"},
		{"ListTrustAnchors", TypeRolesAnywhereTrustAnchor, "trust-anchor", "trust anchors"},
		{"ListSubjects", TypeRolesAnywhereSubject, "subject", "subjects"},
	}
}

func raTestKindOf(t *testing.T, op string) raTestKind {
	t.Helper()
	for _, k := range raTestKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no Roles Anywhere listing", op)
	return raTestKind{}
}

func (k raTestKind) id(suffix string) string { return k.kind + "-" + suffix }

// name is the stored Name of the row with suffix: a subject's SubjectId,
// any other element's Name.
func (k raTestKind) name(suffix string) string {
	if k.op == "ListSubjects" {
		return k.id(suffix)
	}
	return "name-" + k.kind + "-" + suffix
}

func (k raTestKind) arnIn(partition, region, suffix string) string {
	return "arn:" + partition + ":rolesanywhere:" + region + ":" + testAccountID + ":" + k.kind + "/" + k.id(suffix)
}

func (k raTestKind) arn(suffix string) string { return k.arnIn("aws", testRegion, suffix) }

// element is k's listed element with suffix, carrying arn (nil for an
// element listed without one).
func (k raTestKind) element(arn *string, suffix string) any {
	id, name := sdkaws.String(k.id(suffix)), sdkaws.String(k.name(suffix))
	enabled := sdkaws.Bool(true)
	switch k.op {
	case "ListCrls":
		return rolesanywheretypes.CrlDetail{
			CrlArn: arn, CrlId: id, Name: name, Enabled: enabled, CreatedAt: &raTestCreated,
			TrustAnchorArn: sdkaws.String("arn:aws:rolesanywhere:" + testRegion + ":" + testAccountID + ":trust-anchor/ta-x"),
		}
	case "ListProfiles":
		return rolesanywheretypes.ProfileDetail{
			ProfileArn: arn, ProfileId: id, Name: name, Enabled: enabled, CreatedAt: &raTestCreated,
			RoleArns: []string{"arn:aws:iam::" + testAccountID + ":role/r-" + suffix},
		}
	case "ListTrustAnchors":
		return rolesanywheretypes.TrustAnchorDetail{
			TrustAnchorArn: arn, TrustAnchorId: id, Name: name, Enabled: enabled, CreatedAt: &raTestCreated,
			Source: &rolesanywheretypes.Source{SourceType: rolesanywheretypes.TrustAnchorTypeAwsAcmPca},
		}
	default:
		return rolesanywheretypes.SubjectSummary{
			SubjectArn: arn, SubjectId: id, Enabled: enabled, CreatedAt: &raTestCreated,
			X509Subject: sdkaws.String("CN=" + suffix),
		}
	}
}

// page is k's output holding element e, and next.
func (k raTestKind) page(e any, next *string) any {
	switch e := e.(type) {
	case rolesanywheretypes.CrlDetail:
		return &rolesanywhere.ListCrlsOutput{NextToken: next, Crls: []rolesanywheretypes.CrlDetail{e}}
	case rolesanywheretypes.ProfileDetail:
		return &rolesanywhere.ListProfilesOutput{NextToken: next, Profiles: []rolesanywheretypes.ProfileDetail{e}}
	case rolesanywheretypes.TrustAnchorDetail:
		return &rolesanywhere.ListTrustAnchorsOutput{NextToken: next, TrustAnchors: []rolesanywheretypes.TrustAnchorDetail{e}}
	default:
		return &rolesanywhere.ListSubjectsOutput{NextToken: next, Subjects: []rolesanywheretypes.SubjectSummary{e.(rolesanywheretypes.SubjectSummary)}}
	}
}

// raTestCalls records the calls of a scan and answers the tag reads, by ARN,
// with tags or tagErrs. partition and region are those of the ARNs the
// listings return.
type raTestCalls struct {
	mu                sync.Mutex
	partition, region string
	calls             map[string]int
	pageSizes         map[string][]*int32
	tagARNs           []string
	tags              map[string]map[string]string
	tagErrs           map[string]error
}

// newRATestCalls tags every "-tagged" row with its type.
func newRATestCalls() *raTestCalls {
	return newRATestCallsIn("aws", testRegion)
}

func newRATestCallsIn(partition, region string) *raTestCalls {
	c := &raTestCalls{
		partition: partition, region: region,
		calls: map[string]int{}, pageSizes: map[string][]*int32{},
		tags: map[string]map[string]string{}, tagErrs: map[string]error{},
	}
	for _, k := range raTestKinds() {
		c.tags[k.arnIn(partition, region, "tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

// record notes op's call and the PageSize it was sent.
func (c *raTestCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[op]++
	if op != "ListTagsForResource" {
		c.pageSizes[op] = append(c.pageSizes[op], bacField[*int32](in, "PageSize"))
	}
}

func (c *raTestCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

func (c *raTestCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *raTestCalls) listTags(in any) (any, error) {
	arn := sv(in.(*rolesanywhere.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.tagErrs[arn]; err != nil {
		return nil, err
	}
	var tags []rolesanywheretypes.Tag
	for k, v := range c.tags[arn] {
		tags = append(tags, rolesanywheretypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &rolesanywhere.ListTagsForResourceOutput{Tags: tags}, nil
}

// twoPages answers k's op with "-tagged" on the first page and "-bare" on
// the second.
func (c *raTestCalls) twoPages(k raTestKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.page(k.element(sdkaws.String(k.arnIn(c.partition, c.region, suffix)), suffix), next), nil
	}
}

// raTestHandlers answers every op the scan calls from c.
func raTestHandlers(c *raTestCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags}
	for _, k := range raTestKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// raTestScan runs the scan's real entry point against handlers; every op
// they do not answer is denied.
func raTestScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, raTestDenied)
	return scanRolesAnywhere(ctx, acct, testRegion, st, testScanID)
}

// raAllTagARNs is the ARN of every row of the fixture, sorted.
func raAllTagARNs() []string {
	var arns []string
	for _, k := range raTestKinds() {
		arns = append(arns, k.arn("tagged"), k.arn("bare"))
	}
	slices.Sort(arns)
	return arns
}

func raWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// raJSONValue is v's JSON form decoded, to compare with a row's decoded
// attributes.
func raJSONValue(t *testing.T, v any) map[string]any {
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

// Every listing stores both its pages, each row under its listed ARN, in the
// scanned account's name, named by its listed Name (a subject by its
// SubjectId), with no Status and no CreatedAt, not managed, its listed
// element as its attributes, and the tags its tag read returns (nil for
// none). ListTagsForResource is sent exactly the ARNs of every row; no
// listing is sent a PageSize. No error, no warning.
func TestScanRolesAnywhere_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newRATestCalls()

	total, inserted, err := raTestScan(context.Background(), st, raTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != raTestTotal || inserted != raTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, raTestTotal, raTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range raTestKinds() {
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
			if sv(r.Name) != k.name(suffix) || sv(r.Region) != testRegion || r.Status != nil || r.CreatedAt != nil || r.ManagedByProvider {
				t.Errorf("%s Name, Region, Status, CreatedAt, Managed = %q, %q, %v, %v, %v; want %q, %q, no Status, no CreatedAt, false",
					arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, r.ManagedByProvider, k.name(suffix), testRegion)
			}
			if got, want := sv(r.AccountName), newTestAccount(testAccountID).Name; got != want {
				t.Errorf("%s AccountName = %q; want %q", arn, got, want)
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("%s attrs %s: %v", arn, r.AttributesJSON, err)
			}
			if listed := raJSONValue(t, k.element(sdkaws.String(arn), suffix)); !reflect.DeepEqual(attrs, listed) {
				t.Errorf("%s attrs = %s; want its listed element %v", arn, r.AttributesJSON, listed)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		if got := calls.pageSizes[k.op]; len(got) != 2 || got[0] != nil || got[1] != nil {
			t.Errorf("%s PageSizes = %v; want two calls without one", k.op, got)
		}
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, raAllTagARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, raAllTagARNs())
	}
}

// A subject listed without a SubjectId is named by its ARN, the only other
// identifier SubjectSummary carries, so the row keeps a non-empty Name.
func TestScanRolesAnywhere_SubjectWithoutIDNamedByARN(t *testing.T) {
	st := newTestStore(t)
	calls := newRATestCalls()
	handlers := raTestHandlers(calls)
	subjects := raTestKindOf(t, "ListSubjects")
	arn := subjects.arn("noid")
	handlers["ListSubjects"] = func(in any) (any, error) {
		calls.record("ListSubjects", in)
		e := subjects.element(sdkaws.String(arn), "noid").(rolesanywheretypes.SubjectSummary)
		e.SubjectId = nil
		return subjects.page(e, nil), nil
	}

	if _, _, err := raTestScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	r, ok := rowsByNativeID(t, st, TypeRolesAnywhereSubject)[arn]
	if !ok {
		t.Fatalf("%s missing", arn)
	}
	if sv(r.Name) != arn {
		t.Errorf("Name = %q; want %q", sv(r.Name), arn)
	}
}

// raStoredWithTags reports whether the row under arn is stored with tags. It
// does not fail the test, so a listing's handler can call it.
func raStoredWithTags(st *store.Store, arn string) bool {
	r, err := st.GetResource(store.ResourceID("aws", testAccountID, arn))
	return err == nil && r != nil && r.TagsJSON != nil
}

// Every listing writes its first page, with its tags, before it reads the
// second.
func TestScanRolesAnywhere_PagesWrittenAsRead(t *testing.T) {
	st := newTestStore(t)
	handlers := raTestHandlers(newRATestCalls())
	var (
		mu          sync.Mutex
		storedFirst = map[string]bool{}
	)
	for _, k := range raTestKinds() {
		list := handlers[k.op]
		handlers[k.op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				mu.Lock()
				storedFirst[k.op] = raStoredWithTags(st, k.arn("tagged"))
				mu.Unlock()
			}
			return list(in)
		}
	}

	if _, _, err := raTestScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, k := range raTestKinds() {
		if !storedFirst[k.op] {
			t.Errorf("%s: %s not stored with its tags when the second page was read", k.op, k.arn("tagged"))
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanRolesAnywhere_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := raTestScan(context.Background(), st, raTestHandlers(newRATestCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := raTestScan(context.Background(), st, raTestHandlers(newRATestCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != raTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, raTestTotal)
	}
}

// Empty listings: each listed once, nothing stored, no tag read, no error,
// no warning.
func TestScanRolesAnywhere_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newRATestCalls()
	handlers := raTestHandlers(calls)
	empty := map[string]any{
		"ListCrls":         &rolesanywhere.ListCrlsOutput{},
		"ListProfiles":     &rolesanywhere.ListProfilesOutput{},
		"ListTrustAnchors": &rolesanywhere.ListTrustAnchorsOutput{},
		"ListSubjects":     &rolesanywhere.ListSubjectsOutput{},
	}
	for op, out := range empty {
		handlers[op] = func(in any) (any, error) {
			calls.record(op, in)
			return out, nil
		}
	}

	total, inserted, err := raTestScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range raTestKinds() {
		if n := calls.count(k.op); n != 1 {
			t.Errorf("%s called %d times; want 1", k.op, n)
		}
	}
	if n := calls.count("ListTagsForResource"); n != 0 {
		t.Errorf("ListTagsForResource called %d times; want 0", n)
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently. A denial keeps the row
// untagged and warns once under the tag op with the scan's scope; a throttle
// keeps it and is transient; any other error keeps it and is an error.
func TestScanRolesAnywhere_TagReadFailures(t *testing.T) {
	type tagFailure struct {
		name      string
		op        string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}
	var cases []tagFailure
	for _, k := range raTestKinds() {
		cases = append(cases,
			tagFailure{k.kind + "/not found", k.op, raTestGone, -1, "", raTestTotal - 1, false, false},
			tagFailure{k.kind + "/denied", k.op, raTestDenied, -1, "", raTestTotal, true, true},
			tagFailure{k.kind + "/transient", k.op, raTestThrottle, outcomeTransient, "ThrottlingException", raTestTotal, true, false},
			tagFailure{k.kind + "/other error", k.op, raTestBad, outcomeError, "ValidationException", raTestTotal, true, false})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newRATestCalls()
			k := raTestKindOf(t, c.op)
			arn := k.arn("tagged")
			calls.tagErrs[arn] = c.err

			total, _, err := raTestScan(context.Background(), st, raTestHandlers(calls))
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
			r, ok := rows[arn]
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
			if c.warn {
				want = []string{"rolesanywhere:ListTagsForResource"}
			}
			if got := raWarnOps(w); !slices.Equal(got, want) {
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

// Denied tag reads in three phases send one warning for the whole scan; the
// rows are kept.
func TestScanRolesAnywhere_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newRATestCalls()
	calls.tagErrs[raTestKindOf(t, "ListCrls").arn("bare")] = raTestDenied
	calls.tagErrs[raTestKindOf(t, "ListTrustAnchors").arn("tagged")] = raTestDenied
	calls.tagErrs[raTestKindOf(t, "ListSubjects").arn("bare")] = raTestDenied

	total, _, err := raTestScan(context.Background(), st, raTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != raTestTotal {
		t.Errorf("total = %d; want %d", total, raTestTotal)
	}
	if got := raWarnOps(warns()); !slices.Equal(got, []string{"rolesanywhere:ListTagsForResource"}) {
		t.Errorf("warnings = %v; want one under rolesanywhere:ListTagsForResource", got)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanRolesAnywhere_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "rolesanywhere:ListTagsForResource")
	calls := newRATestCalls()
	calls.tagErrs[raTestKindOf(t, "ListProfiles").arn("bare")] = raTestThrottle
	calls.tagErrs[raTestKindOf(t, "ListSubjects").arn("tagged")] = raTestBad

	_, _, err := raTestScan(context.Background(), st, raTestHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("rolesanywhere:ListTagsForResource warnings = %d; want 1", *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored.
func TestScanRolesAnywhere_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeRolesAnywhereProfile)
	calls := newRATestCalls()
	calls.tagErrs[raTestKindOf(t, "ListProfiles").arn("tagged")] = raTestDenied

	total, _, err := raTestScan(context.Background(), st, raTestHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListTrustAnchors", "ListSubjects"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0 after the failed write", op, n)
		}
	}
	if total != 2 {
		t.Errorf("total = %d; want the 2 CRLs", total)
	}
	if n := len(rowsByNativeID(t, st, TypeRolesAnywhereCRL)); n != 2 {
		t.Errorf("CRLs = %d; want 2", n)
	}
}

// A denied tag read is still warned about when a later phase fails: its row
// was stored, so the scan's error does not hide why it has no tags.
func TestScanRolesAnywhere_TagDenialWarnedBesideLaterFailure(t *testing.T) {
	for _, c := range []struct {
		name string
		fail func(t *testing.T, st *store.Store, calls *raTestCalls, handlers map[string]func(any) (any, error))
		want func(t *testing.T, err error)
	}{
		{"later listing page fails", func(t *testing.T, _ *store.Store, calls *raTestCalls, handlers map[string]func(any) (any, error)) {
			list := handlers["ListProfiles"]
			handlers["ListProfiles"] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.record("ListProfiles", in)
					return nil, raTestBad
				}
				return list(in)
			}
		}, func(t *testing.T, err error) {
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
		}},
		{"later tag read fails", func(t *testing.T, _ *store.Store, calls *raTestCalls, _ map[string]func(any) (any, error)) {
			calls.tagErrs[raTestKindOf(t, "ListSubjects").arn("tagged")] = raTestBad
		}, func(t *testing.T, err error) {
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
		}},
		{"later write fails", func(t *testing.T, st *store.Store, _ *raTestCalls, _ map[string]func(any) (any, error)) {
			refuseStoreWrites(t, st, TypeRolesAnywhereProfile)
		}, func(t *testing.T, err error) {
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newRATestCalls()
			denied := raTestKindOf(t, "ListCrls").arn("bare")
			calls.tagErrs[denied] = raTestDenied
			handlers := raTestHandlers(calls)
			c.fail(t, st, calls, handlers)

			_, _, err := raTestScan(context.Background(), st, handlers)
			c.want(t, err)
			w := warns()
			if got := raWarnOps(w); !slices.Equal(got, []string{"rolesanywhere:ListTagsForResource"}) {
				t.Fatalf("warnings = %v; want one under rolesanywhere:ListTagsForResource", w)
			}
			if x := w[0]; x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, "first "+denied) {
				t.Errorf("warning = %+v; want Scope %s/%s, keyed %s", x, testAccountID, testRegion, denied)
			}
			r, ok := rowsByNativeID(t, st, TypeRolesAnywhereCRL)[denied]
			if !ok {
				t.Fatalf("%s missing", denied)
			}
			if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", denied, *r.TagsJSON)
			}
		})
	}
}

// A listing's failed later page keeps the page read before it, and its error
// does not stop the other phases. Every listing's first page denied is
// warned about under its op, and the other phases run too.
func TestScanRolesAnywhere_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	type listFailure struct {
		name      string
		op        string
		err       error
		laterPage bool
		wantKept  int
		wantWarn  bool
	}
	var cases []listFailure
	for _, k := range raTestKinds() {
		cases = append(cases,
			listFailure{"later page fails/" + k.op, k.op, raTestBad, true, 1, false},
			listFailure{"denied/" + k.op, k.op, raTestDenied, false, 0, true})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newRATestCalls()
			handlers := raTestHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if later := bacField[*string](in, "NextToken") != nil; later == c.laterPage {
					calls.record(c.op, in)
					return nil, c.err
				}
				return list(in)
			}

			total, _, err := raTestScan(context.Background(), st, handlers)
			if c.wantWarn {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "ValidationException")
			}
			var wantWarn []string
			if c.wantWarn {
				wantWarn = []string{"rolesanywhere:" + c.op}
			}
			if got := raWarnOps(warns()); !slices.Equal(got, wantWarn) {
				t.Errorf("warnings = %v; want %v", got, wantWarn)
			}
			if want := raTestTotal - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			for _, k := range raTestKinds() {
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

// A repeated NextToken ends the listing as an error, keeping its pages, and
// the later phases run.
func TestScanRolesAnywhere_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	calls := newRATestCalls()
	handlers := raTestHandlers(calls)
	twoPages := handlers["ListTrustAnchors"]
	handlers["ListTrustAnchors"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*rolesanywhere.ListTrustAnchorsOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, _, err := raTestScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != raTestTotal {
		t.Errorf("total = %d; want %d", total, raTestTotal)
	}
	if n := len(rowsByNativeID(t, st, TypeRolesAnywhereTrustAnchor)); n != 2 {
		t.Errorf("trust anchors = %d; want 2", n)
	}
	if n := len(rowsByNativeID(t, st, TypeRolesAnywhereSubject)); n != 2 {
		t.Errorf("subjects = %d; want 2", n)
	}
}

// An element listed without an ARN is not stored, not read, and warned about
// once under its listing with the scan's scope, keyed by its Name (a subject
// by its SubjectId); the rest of the listing is stored. raRow's own test pins
// the noun the warning names the listing's elements by.
func TestScanRolesAnywhere_ElementWithoutARN(t *testing.T) {
	for _, k := range raTestKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newRATestCalls()
			handlers := raTestHandlers(calls)
			twoPages := handlers[k.op]
			handlers[k.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.record(k.op, in)
					return k.page(k.element(nil, "noarn"), nil), nil
				}
				return twoPages(in)
			}

			total, _, err := raTestScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := raTestTotal - 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if n := len(rowsByNativeID(t, st, k.typ)); n != 1 {
				t.Errorf("%s rows = %d; want the 1 with an ARN", k.typ, n)
			}
			want := slices.DeleteFunc(raAllTagARNs(), func(a string) bool { return a == k.arn("bare") })
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			w := warns()
			if got := raWarnOps(w); !slices.Equal(got, []string{"rolesanywhere:" + k.op}) {
				t.Fatalf("warnings = %v; want one under rolesanywhere:%s", w, k.op)
			}
			if x := w[0]; x.Scope != testAccountID+"/"+testRegion || !strings.HasSuffix(x.Message, "first "+k.name("noarn")) {
				t.Errorf("warning = %+v; want Scope %s/%s, keyed %q", x, testAccountID, testRegion, k.name("noarn"))
			}
		})
	}
}

// In GovCloud and China the listed ARN of every row is its NativeID and
// reaches the tag op unchanged.
func TestScanRolesAnywhere_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newRATestCallsIn(c.partition, c.region)
			acct := newTestAccount(testAccountID)
			acct.Partition = c.partition
			acct.cfg = opStubConfig(raTestHandlers(calls), raTestDenied)
			if _, _, err := scanRolesAnywhere(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			var want []string
			for _, k := range raTestKinds() {
				want = append(want, k.arnIn(c.partition, c.region, "tagged"), k.arnIn(c.partition, c.region, "bare"))
			}
			slices.Sort(want)
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			trustAnchors := raTestKindOf(t, "ListTrustAnchors")
			arn := trustAnchors.arnIn(c.partition, c.region, "tagged")
			assertTagsMap(t, rowsByNativeID(t, st, TypeRolesAnywhereTrustAnchor)[arn].TagsJSON, map[string]string{"type": TypeRolesAnywhereTrustAnchor})
		})
	}
}

// Once ctx ends, the stop is one member of the error, naming the phases not
// run, although the phase in flight saw it in its listing; the later phases
// are not run, and the rows written before the stop stay stored.
func TestScanRolesAnywhere_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newRATestCalls()
	handlers := raTestHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListProfiles"]
	handlers["ListProfiles"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := raTestScan(ctx, st, handlers)
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
	for _, op := range []string{"ListTrustAnchors", "ListSubjects"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
	// Profiles is phase 2 of 4, so trust anchors and subjects never started.
	if !strings.Contains(err.Error(), "rolesanywhere: 2 phases not run") {
		t.Errorf("err = %v; want it to say rolesanywhere: 2 phases not run", err)
	}
	if n := len(rowsByNativeID(t, st, TypeRolesAnywhereCRL)); n != 2 {
		t.Errorf("CRLs = %d; want 2", n)
	}
	if profiles := rowsByNativeID(t, st, TypeRolesAnywhereProfile); len(profiles) != 1 || !raStoredWithTags(st, raTestKindOf(t, "ListProfiles").arn("tagged")) {
		t.Errorf("profiles = %v; want only the first page's, with its tags", profiles)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and no later phase runs.
func TestScanRolesAnywhere_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "rolesanywhere.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := raTestHandlers(newRATestCalls())
		handlers["ListSubjects"] = func(any) (any, error) { return nil, nx }
		total, _, err := raTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := raTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newRATestCalls()
		calls.tagErrs[raTestKindOf(t, "ListCrls").arn("tagged")] = nx
		_, _, err := raTestScan(context.Background(), st, raTestHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newRATestCalls()
		handlers := map[string]func(in any) (any, error){}
		for _, k := range raTestKinds() {
			handlers[k.op] = func(in any) (any, error) {
				calls.record(k.op, in)
				return nil, nx
			}
		}
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(handlers, nx)
		_, _, err := scanRolesAnywhere(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("ListProfiles"); n != 0 {
			t.Errorf("ListProfiles called %d times; want 0 once the region proved unavailable", n)
		}
	})
}

// raRow stores an element with an ARN under it, named name, with the element
// as its attributes. One without an ARN is no row; its warning names the
// type's elements and keys it by name, or by noIdentifier without one.
func TestRARow(t *testing.T) {
	acct := newTestAccount(testAccountID)
	for _, k := range raTestKinds() {
		arn := k.arn("x")
		element := k.element(&arn, "x")
		for _, c := range []struct {
			name         string
			arn, rowName *string
			why, key     string
		}{
			{"ARN", &arn, sdkaws.String("n"), "", ""},
			{"no ARN, named", nil, sdkaws.String("n"), k.what + " have no ARN", "n"},
			{"empty ARN, unnamed", sdkaws.String(""), nil, k.what + " have no ARN", noIdentifier},
		} {
			t.Run(k.kind+"/"+c.name, func(t *testing.T) {
				r, why, key := raRow(acct, testRegion, testScanID, k.typ, c.arn, c.rowName, element)
				if why != c.why || key != c.key {
					t.Errorf("why, key = %q, %q; want %q, %q", why, key, c.why, c.key)
				}
				if c.why != "" {
					if r != nil {
						t.Errorf("row = %+v; want none", r)
					}
					return
				}
				if r == nil {
					t.Fatal("row = nil; want one")
				}
				if r.Provider != "aws" || r.AccountID != testAccountID || sv(r.AccountName) != acct.Name || r.Type != k.typ || r.NativeID != arn ||
					sv(r.Name) != "n" || sv(r.Region) != testRegion || r.DiscoveredBy != testScanID {
					t.Errorf("row = %+v; want %s under %s named n in %s, found by %s", r, k.typ, arn, testRegion, testScanID)
				}
				if r.AttributesJSON != mustJSON(element) {
					t.Errorf("attrs = %s; want the element verbatim", r.AttributesJSON)
				}
			})
		}
	}
}
