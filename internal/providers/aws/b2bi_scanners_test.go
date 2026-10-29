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
	"github.com/aws/aws-sdk-go-v2/service/b2bi"
	b2bitypes "github.com/aws/aws-sdk-go-v2/service/b2bi/types"
	"github.com/icearp/disco-cli/store"
)

// b2biTestTotal is every row of the fixture: two of each of the four types,
// one per page.
const b2biTestTotal = 2 * 4

var (
	b2biTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: b2bi:List")
	b2biTestClosed   = apiErr("AccessDeniedException", "")
	b2biTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	b2biTestBad      = apiErr("ValidationException", "bad")
	b2biTestGone     = apiErr("ResourceNotFoundException", "gone")
	b2biTestCreated  = time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
)

// b2biTestKind is one listing: its op, the type it stores, its ARN's
// resource kind, the element field holding the id, and the noun its
// warnings name its elements by.
type b2biTestKind struct {
	op, typ, kind, idField, what string
}

func b2biTestKinds() []b2biTestKind {
	return []b2biTestKind{
		{"ListCapabilities", TypeB2BICapability, "capability", "CapabilityId", "capabilities"},
		{"ListPartnerships", TypeB2BIPartnership, "partnership", "PartnershipId", "partnerships"},
		{"ListProfiles", TypeB2BIProfile, "profile", "ProfileId", "profiles"},
		{"ListTransformers", TypeB2BITransformer, "transformer", "TransformerId", "transformers"},
	}
}

func b2biTestKindOf(t *testing.T, op string) b2biTestKind {
	t.Helper()
	for _, k := range b2biTestKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no B2BI listing", op)
	return b2biTestKind{}
}

func (k b2biTestKind) id(suffix string) string   { return k.kind + "-" + suffix }
func (k b2biTestKind) name(suffix string) string { return "name-" + k.kind + "-" + suffix }

func (k b2biTestKind) arn(suffix string) string {
	return "arn:aws:b2bi:" + testRegion + ":" + testAccountID + ":" + k.kind + "/" + k.id(suffix)
}

// element is k's listed element with suffix under id (nil for an element
// listed without one).
func (k b2biTestKind) element(id *string, suffix string) any {
	name := sdkaws.String(k.name(suffix))
	switch k.op {
	case "ListCapabilities":
		return b2bitypes.CapabilitySummary{CapabilityId: id, Name: name, Type: b2bitypes.CapabilityTypeEdi, CreatedAt: &b2biTestCreated}
	case "ListPartnerships":
		return b2bitypes.PartnershipSummary{PartnershipId: id, Name: name, ProfileId: sdkaws.String("profile-x"), Capabilities: []string{"capability-x"}, CreatedAt: &b2biTestCreated}
	case "ListProfiles":
		return b2bitypes.ProfileSummary{ProfileId: id, Name: name, BusinessName: sdkaws.String("biz"), LogGroupName: sdkaws.String("lg-" + suffix), Logging: b2bitypes.LoggingEnabled, CreatedAt: &b2biTestCreated}
	default:
		return b2bitypes.TransformerSummary{TransformerId: id, Name: name, Status: b2bitypes.TransformerStatusActive, CreatedAt: &b2biTestCreated}
	}
}

// page is k's output holding its element with suffix under id, and next.
func (k b2biTestKind) page(id *string, suffix string, next *string) any {
	switch e := k.element(id, suffix).(type) {
	case b2bitypes.CapabilitySummary:
		return &b2bi.ListCapabilitiesOutput{NextToken: next, Capabilities: []b2bitypes.CapabilitySummary{e}}
	case b2bitypes.PartnershipSummary:
		return &b2bi.ListPartnershipsOutput{NextToken: next, Partnerships: []b2bitypes.PartnershipSummary{e}}
	case b2bitypes.ProfileSummary:
		return &b2bi.ListProfilesOutput{NextToken: next, Profiles: []b2bitypes.ProfileSummary{e}}
	default:
		return &b2bi.ListTransformersOutput{NextToken: next, Transformers: []b2bitypes.TransformerSummary{e.(b2bitypes.TransformerSummary)}}
	}
}

// b2biTestCalls records the calls of a scan and answers the tag reads, by
// ARN, with tags or tagErrs, and GetCapability, by id, with a body or
// getErrs.
type b2biTestCalls struct {
	mu         sync.Mutex
	maxResults map[string][]int32
	calls      map[string]int
	tagARNs    []string
	getIDs     []string
	tags       map[string]map[string]string
	tagErrs    map[string]error
	getErrs    map[string]error
}

// newB2BITestCalls tags every "-tagged" row with its type.
func newB2BITestCalls() *b2biTestCalls {
	c := &b2biTestCalls{
		maxResults: map[string][]int32{}, calls: map[string]int{},
		tags: map[string]map[string]string{}, tagErrs: map[string]error{}, getErrs: map[string]error{},
	}
	for _, k := range b2biTestKinds() {
		c.tags[k.arn("tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

// record notes op's call and its MaxResults.
func (c *b2biTestCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[op]++
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		c.maxResults[op] = append(c.maxResults[op], *n)
	}
}

func (c *b2biTestCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

func (c *b2biTestCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *b2biTestCalls) sortedGetIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.getIDs))
}

func (c *b2biTestCalls) listTags(in any) (any, error) {
	arn := sv(in.(*b2bi.ListTagsForResourceInput).ResourceARN)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.tagErrs[arn]; err != nil {
		return nil, err
	}
	var tags []b2bitypes.Tag
	for k, v := range c.tags[arn] {
		tags = append(tags, b2bitypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &b2bi.ListTagsForResourceOutput{Tags: tags}, nil
}

// getCapability answers with the capability's ARN and an instructions
// document in bucket "bucket-<id>".
func (c *b2biTestCalls) getCapability(in any) (any, error) {
	id := sv(in.(*b2bi.GetCapabilityInput).CapabilityId)
	c.record("GetCapability", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getIDs = append(c.getIDs, id)
	if err := c.getErrs[id]; err != nil {
		return nil, err
	}
	return &b2bi.GetCapabilityOutput{
		CapabilityArn: sdkaws.String("arn:aws:b2bi:" + testRegion + ":" + testAccountID + ":capability/" + id),
		CapabilityId:  sdkaws.String(id), Name: sdkaws.String("get-" + id), Type: b2bitypes.CapabilityTypeEdi,
		InstructionsDocuments: []b2bitypes.S3Location{{BucketName: sdkaws.String("bucket-" + id), Key: sdkaws.String("doc.json")}},
	}, nil
}

// twoPages answers k's op with "-tagged" on the first page and "-bare" on
// the second.
func (c *b2biTestCalls) twoPages(k b2biTestKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.page(sdkaws.String(k.id(suffix)), suffix, next), nil
	}
}

// b2biTestHandlers answers every op the scan calls from c.
func b2biTestHandlers(c *b2biTestCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags, "GetCapability": c.getCapability}
	for _, k := range b2biTestKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// b2biTestScan runs the scan's real entry point against handlers; every op
// they do not answer is denied.
func b2biTestScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, b2biTestDenied)
	return scanB2BI(ctx, acct, testRegion, st, testScanID)
}

// b2biAllTagARNs is the ARN of every row of the fixture, sorted.
func b2biAllTagARNs() []string {
	var arns []string
	for _, k := range b2biTestKinds() {
		arns = append(arns, k.arn("tagged"), k.arn("bare"))
	}
	slices.Sort(arns)
	return arns
}

func b2biWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// b2biJSONValue is v's JSON form decoded, to compare with a row's decoded
// attributes.
func b2biJSONValue(t *testing.T, v any) map[string]any {
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

func b2biAttrs(t *testing.T, r store.Resource) map[string]any {
	t.Helper()
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("%s attrs %s: %v", r.NativeID, r.AttributesJSON, err)
	}
	return attrs
}

// Every listing stores both its pages, each row under the ARN built from its
// listed id, in the scanned account's name, named by its listed Name, with no
// CreatedAt, a transformer with
// its status as Status and the others with none, and the tags its tag read
// returns (nil for none). A capability's attributes are its GetCapability
// body (its ARN and instructions documents), sent its listed id; the others'
// are their listed element. ListTagsForResource is sent exactly the ARNs of
// every row; every listing is sent MaxResults 100, after the gate's probe of
// ListProfiles with 1. No error, no warning.
func TestScanB2BI_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newB2BITestCalls()

	total, inserted, err := b2biTestScan(context.Background(), st, b2biTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != b2biTestTotal || inserted != b2biTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, b2biTestTotal, b2biTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range b2biTestKinds() {
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
			wantStatus := ""
			if k.typ == TypeB2BITransformer {
				wantStatus = "active"
			}
			if sv(r.Name) != k.name(suffix) || sv(r.Region) != testRegion || sv(r.Status) != wantStatus || r.CreatedAt != nil {
				t.Errorf("%s Name, Region, Status, CreatedAt = %q, %q, %q, %v; want %q, %q, %q, nil", arn, sv(r.Name), sv(r.Region), sv(r.Status), r.CreatedAt, k.name(suffix), testRegion, wantStatus)
			}
			if k.typ != TypeB2BITransformer && r.Status != nil {
				t.Errorf("%s Status = %q; want nil", arn, *r.Status)
			}
			if got, want := sv(r.AccountName), newTestAccount(testAccountID).Name; got != want {
				t.Errorf("%s AccountName = %q; want %q", arn, got, want)
			}
			attrs := b2biAttrs(t, r)
			if attrs[k.idField] != k.id(suffix) {
				t.Errorf("%s attrs %s = %v; want %s", arn, k.idField, attrs[k.idField], k.id(suffix))
			}
			if k.typ != TypeB2BICapability {
				listed := b2biJSONValue(t, k.element(sdkaws.String(k.id(suffix)), suffix))
				if !reflect.DeepEqual(attrs, listed) {
					t.Errorf("%s attrs = %s; want its listed element %v", arn, r.AttributesJSON, listed)
				}
			} else {
				docs, _ := attrs["InstructionsDocuments"].([]any)
				if attrs["CapabilityArn"] != arn || len(docs) != 1 || docs[0].(map[string]any)["BucketName"] != "bucket-"+k.id(suffix) {
					t.Errorf("%s attrs = %s; want its GetCapability body", arn, r.AttributesJSON)
				}
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		want := []int32{100, 100}
		if k.op == "ListProfiles" {
			want = []int32{1, 100, 100}
		}
		if got := calls.maxResults[k.op]; !slices.Equal(got, want) {
			t.Errorf("%s MaxResults = %v; want %v", k.op, got, want)
		}
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, b2biAllTagARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, b2biAllTagARNs())
	}
	if got, want := calls.sortedGetIDs(), []string{"capability-bare", "capability-tagged"}; !slices.Equal(got, want) {
		t.Errorf("GetCapability ids = %v; want %v", got, want)
	}
}

// b2biStoredWithTags reports whether the row under arn is stored with tags. It does not fail the test, so a listing's handler can call it.
func b2biStoredWithTags(st *store.Store, arn string) bool {
	r, err := st.GetResource(store.ResourceID("aws", testAccountID, arn))
	return err == nil && r != nil && r.TagsJSON != nil
}

// Every listing writes its first page, with its tags, before it reads the
// second.
func TestScanB2BI_PagesWrittenAsRead(t *testing.T) {
	st := newTestStore(t)
	handlers := b2biTestHandlers(newB2BITestCalls())
	var (
		mu          sync.Mutex
		storedFirst = map[string]bool{}
	)
	for _, k := range b2biTestKinds() {
		list := handlers[k.op]
		handlers[k.op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				mu.Lock()
				storedFirst[k.op] = b2biStoredWithTags(st, k.arn("tagged"))
				mu.Unlock()
			}
			return list(in)
		}
	}

	if _, _, err := b2biTestScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, k := range b2biTestKinds() {
		if !storedFirst[k.op] {
			t.Errorf("%s: %s not stored with its tags when the second page was read", k.op, k.arn("tagged"))
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanB2BI_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := b2biTestScan(context.Background(), st, b2biTestHandlers(newB2BITestCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := b2biTestScan(context.Background(), st, b2biTestHandlers(newB2BITestCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != b2biTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, b2biTestTotal)
	}
}

// Empty listings: each listed once (ListProfiles also by the gate), nothing
// stored, no detail or tag read, no error, no warning.
func TestScanB2BI_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newB2BITestCalls()
	handlers := b2biTestHandlers(calls)
	empty := map[string]any{
		"ListCapabilities": &b2bi.ListCapabilitiesOutput{},
		"ListPartnerships": &b2bi.ListPartnershipsOutput{},
		"ListProfiles":     &b2bi.ListProfilesOutput{},
		"ListTransformers": &b2bi.ListTransformersOutput{},
	}
	for op, out := range empty {
		handlers[op] = func(in any) (any, error) {
			calls.record(op, in)
			return out, nil
		}
	}

	total, inserted, err := b2biTestScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range b2biTestKinds() {
		want := 1
		if k.op == "ListProfiles" {
			want = 2
		}
		if n := calls.count(k.op); n != want {
			t.Errorf("%s called %d times; want %d", k.op, n, want)
		}
	}
	for _, op := range []string{"ListTagsForResource", "GetCapability"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently. A denial keeps the row
// untagged and warns once under the tag op with the scan's scope; a throttle
// keeps it and is transient; any other error keeps it and is an error.
func TestScanB2BI_TagReadFailures(t *testing.T) {
	for _, c := range []struct {
		name      string
		op        string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"capability/not found", "ListCapabilities", b2biTestGone, -1, "", b2biTestTotal - 1, false, false},
		{"partnership/denied", "ListPartnerships", b2biTestDenied, -1, "", b2biTestTotal, true, true},
		{"profile/transient", "ListProfiles", b2biTestThrottle, outcomeTransient, "ThrottlingException", b2biTestTotal, true, false},
		{"transformer/other error", "ListTransformers", b2biTestBad, outcomeError, "ValidationException", b2biTestTotal, true, false},
		{"transformer/not found", "ListTransformers", b2biTestGone, -1, "", b2biTestTotal - 1, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newB2BITestCalls()
			k := b2biTestKindOf(t, c.op)
			arn := k.arn("tagged")
			calls.tagErrs[arn] = c.err

			total, _, err := b2biTestScan(context.Background(), st, b2biTestHandlers(calls))
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
				want = []string{"b2bi:ListTagsForResource"}
			}
			if got := b2biWarnOps(w); !slices.Equal(got, want) {
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
func TestScanB2BI_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newB2BITestCalls()
	calls.tagErrs[b2biTestKindOf(t, "ListCapabilities").arn("bare")] = b2biTestDenied
	calls.tagErrs[b2biTestKindOf(t, "ListProfiles").arn("tagged")] = b2biTestDenied
	calls.tagErrs[b2biTestKindOf(t, "ListTransformers").arn("bare")] = b2biTestDenied

	total, _, err := b2biTestScan(context.Background(), st, b2biTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != b2biTestTotal {
		t.Errorf("total = %d; want %d", total, b2biTestTotal)
	}
	if got := b2biWarnOps(warns()); !slices.Equal(got, []string{"b2bi:ListTagsForResource"}) {
		t.Errorf("warnings = %v; want one under b2bi:ListTagsForResource", got)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanB2BI_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "b2bi:ListTagsForResource")
	calls := newB2BITestCalls()
	calls.tagErrs[b2biTestKindOf(t, "ListPartnerships").arn("bare")] = b2biTestThrottle
	calls.tagErrs[b2biTestKindOf(t, "ListTransformers").arn("tagged")] = b2biTestBad

	_, _, err := b2biTestScan(context.Background(), st, b2biTestHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("b2bi:ListTagsForResource warnings = %d; want 1", *warns)
	}
}

// A failed GetCapability. ResourceNotFoundException is modeled, so a
// capability deleted since it was listed is dropped silently and its tags
// are not read. Any other failure keeps the CapabilitySummary as the row,
// tagged: a denial warns once under GetCapability with the scan's scope; a
// throttle is transient; any other error is an error. The sibling keeps its
// GetCapability body.
func TestScanB2BI_GetCapabilityFailures(t *testing.T) {
	caps := b2biTestKindOf(t, "ListCapabilities")
	for _, c := range []struct {
		name      string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"not found", b2biTestGone, -1, "", b2biTestTotal - 1, false, false},
		{"denied", b2biTestDenied, -1, "", b2biTestTotal, true, true},
		{"transient", b2biTestThrottle, outcomeTransient, "ThrottlingException", b2biTestTotal, true, false},
		{"other error", b2biTestBad, outcomeError, "ValidationException", b2biTestTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newB2BITestCalls()
			arn := caps.arn("tagged")
			calls.getErrs[caps.id("tagged")] = c.err

			total, _, err := b2biTestScan(context.Background(), st, b2biTestHandlers(calls))
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
			rows := rowsByNativeID(t, st, TypeB2BICapability)
			r, ok := rows[arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok {
				attrs := b2biAttrs(t, r)
				if _, hasARN := attrs["CapabilityArn"]; hasARN || attrs["CapabilityId"] != caps.id("tagged") || attrs["Type"] != "edi" {
					t.Errorf("attrs = %s; want the CapabilitySummary", r.AttributesJSON)
				}
				if sv(r.Name) != caps.name("tagged") {
					t.Errorf("Name = %q; want %q", sv(r.Name), caps.name("tagged"))
				}
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeB2BICapability})
			}
			if slices.Contains(calls.sortedTagARNs(), arn) != c.wantKept {
				t.Errorf("tags of %s read = %v; want %v", arn, !c.wantKept, c.wantKept)
			}
			if sib, ok := rows[caps.arn("bare")]; !ok || b2biAttrs(t, sib)["CapabilityArn"] != caps.arn("bare") {
				t.Errorf("sibling %s missing or without its GetCapability body", caps.arn("bare"))
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{"b2bi:GetCapability"}
			}
			if got := b2biWarnOps(w); !slices.Equal(got, want) {
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

// Both capabilities' GetCapability denied send one warning for the listing.
func TestScanB2BI_GetCapabilityDenialsWarnOnce(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "b2bi:GetCapability")
	calls := newB2BITestCalls()
	caps := b2biTestKindOf(t, "ListCapabilities")
	calls.getErrs[caps.id("tagged")] = b2biTestDenied
	calls.getErrs[caps.id("bare")] = b2biTestDenied

	if _, _, err := b2biTestScan(context.Background(), st, b2biTestHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if *warns != 1 {
		t.Errorf("b2bi:GetCapability warnings = %d; want 1", *warns)
	}
	if n := len(rowsByNativeID(t, st, TypeB2BICapability)); n != 2 {
		t.Errorf("capabilities = %d; want 2", n)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored.
func TestScanB2BI_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeB2BIPartnership)
	calls := newB2BITestCalls()
	calls.tagErrs[b2biTestKindOf(t, "ListPartnerships").arn("tagged")] = b2biTestDenied

	total, _, err := b2biTestScan(context.Background(), st, b2biTestHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count("ListTransformers"); n != 0 {
		t.Errorf("ListTransformers called %d times; want 0 after the failed write", n)
	}
	if n := calls.count("ListProfiles"); n != 1 {
		t.Errorf("ListProfiles called %d times; want only the gate's probe", n)
	}
	if total != 2 {
		t.Errorf("total = %d; want the 2 capabilities", total)
	}
	if n := len(rowsByNativeID(t, st, TypeB2BICapability)); n != 2 {
		t.Errorf("capabilities = %d; want 2", n)
	}
}

// A listing's failed later page keeps the page read before it, and its
// error does not stop the other phases. Every listing's first page denied
// (a message naming the action) is warned about under its op, and the other
// phases run too. Every listing's first page refused as closed to new
// customers (an empty message) is an empty listing: nothing stored, no
// warning, no error.
func TestScanB2BI_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	type listFailure struct {
		name      string
		op        string
		err       error
		laterPage bool
		wantKept  int
		wantWarn  bool
		wantErr   bool
	}
	cases := []listFailure{
		{"later page fails/ListCapabilities", "ListCapabilities", b2biTestBad, true, 1, false, true},
		{"later page fails mid-scan/ListProfiles", "ListProfiles", b2biTestBad, true, 1, false, true},
	}
	for _, k := range b2biTestKinds() {
		cases = append(cases,
			listFailure{"denied/" + k.op, k.op, b2biTestDenied, false, 0, true, false},
			listFailure{"closed/" + k.op, k.op, b2biTestClosed, false, 0, false, false})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newB2BITestCalls()
			handlers := b2biTestHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				later := bacField[*string](in, "NextToken") != nil
				// The gate's probe (MaxResults 1) is answered as usual.
				if probe := bacField[*int32](in, "MaxResults"); probe != nil && *probe == 1 {
					return list(in)
				}
				if later == c.laterPage {
					calls.record(c.op, in)
					return nil, c.err
				}
				return list(in)
			}

			total, _, err := b2biTestScan(context.Background(), st, handlers)
			if c.wantErr {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "ValidationException")
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			var wantWarn []string
			if c.wantWarn {
				wantWarn = []string{"b2bi:" + c.op}
			}
			if got := b2biWarnOps(warns()); !slices.Equal(got, wantWarn) {
				t.Errorf("warnings = %v; want %v", got, wantWarn)
			}
			if want := b2biTestTotal - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			for _, k := range b2biTestKinds() {
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

// The gate's probe refused as closed to new customers reports the service
// not entitled, with nothing listed, stored or warned about.
func TestScanB2BI_GateClosedNotEntitled(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newB2BITestCalls()
	handlers := b2biTestHandlers(calls)
	handlers["ListProfiles"] = func(in any) (any, error) {
		calls.record("ListProfiles", in)
		return nil, b2biTestClosed
	}

	total, inserted, err := b2biTestScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeNotEntitled)
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count("ListCapabilities"); n != 0 {
		t.Errorf("ListCapabilities called %d times; want 0", n)
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanB2BI_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	calls := newB2BITestCalls()
	handlers := b2biTestHandlers(calls)
	twoPages := handlers["ListPartnerships"]
	handlers["ListPartnerships"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*b2bi.ListPartnershipsOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, _, err := b2biTestScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != b2biTestTotal {
		t.Errorf("total = %d; want %d", total, b2biTestTotal)
	}
	if n := len(rowsByNativeID(t, st, TypeB2BIPartnership)); n != 2 {
		t.Errorf("partnerships = %d; want 2", n)
	}
}

// An element listed without an id is not stored, not read, and warned about
// once under its listing with the scan's scope, the warning naming the
// listing's elements and the element by its Name; the rest of the listing is
// stored.
func TestScanB2BI_ElementWithoutID(t *testing.T) {
	for _, k := range b2biTestKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newB2BITestCalls()
			handlers := b2biTestHandlers(calls)
			twoPages := handlers[k.op]
			handlers[k.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.record(k.op, in)
					return k.page(nil, "noid", nil), nil
				}
				return twoPages(in)
			}

			total, _, err := b2biTestScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := b2biTestTotal - 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if n := len(rowsByNativeID(t, st, k.typ)); n != 1 {
				t.Errorf("%s rows = %d; want the 1 with an id", k.typ, n)
			}
			want := slices.DeleteFunc(b2biAllTagARNs(), func(a string) bool { return a == k.arn("bare") })
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			w := warns()
			if got := b2biWarnOps(w); !slices.Equal(got, []string{"b2bi:" + k.op}) {
				t.Fatalf("warnings = %v; want one under b2bi:%s", w, k.op)
			}
			if x := w[0]; x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, k.what) || !strings.Contains(x.Message, k.name("noid")) {
				t.Errorf("warning = %+v; want Scope %s/%s, naming %s and %q", x, testAccountID, testRegion, k.what, k.name("noid"))
			}
		})
	}
}

// In GovCloud and China the ARN built for a row is in the account's
// partition and region, is the row's NativeID, and reaches the tag op
// unchanged.
func TestScanB2BI_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newB2BITestCalls()
			profiles := b2biTestKindOf(t, "ListProfiles")
			arn := "arn:" + c.partition + ":b2bi:" + c.region + ":" + testAccountID + ":profile/" + profiles.id("tagged")
			calls.tags[arn] = map[string]string{"k": "profile"}
			acct := newTestAccount(testAccountID)
			acct.Partition = c.partition
			acct.cfg = opStubConfig(b2biTestHandlers(calls), b2biTestDenied)
			if _, _, err := scanB2BI(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if !slices.Contains(calls.sortedTagARNs(), arn) {
				t.Errorf("ListTagsForResource ARNs %v lack %s", calls.sortedTagARNs(), arn)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeB2BIProfile)[arn].TagsJSON, map[string]string{"k": "profile"})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanB2BI_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newB2BITestCalls()
	handlers := b2biTestHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListPartnerships"]
	handlers["ListPartnerships"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := b2biTestScan(ctx, st, handlers)
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
	if n := calls.count("ListTransformers"); n != 0 {
		t.Errorf("ListTransformers called %d times after ctx ended", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and no later phase runs.
func TestScanB2BI_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "b2bi.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := b2biTestHandlers(newB2BITestCalls())
		handlers["ListTransformers"] = func(any) (any, error) { return nil, nx }
		total, _, err := b2biTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := b2biTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newB2BITestCalls()
		calls.tagErrs[b2biTestKindOf(t, "ListCapabilities").arn("tagged")] = nx
		_, _, err := b2biTestScan(context.Background(), st, b2biTestHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("a GetCapability after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newB2BITestCalls()
		calls.getErrs[b2biTestKindOf(t, "ListCapabilities").id("tagged")] = nx
		_, _, err := b2biTestScan(context.Background(), st, b2biTestHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newB2BITestCalls()
		handlers := map[string]func(in any) (any, error){}
		for _, k := range b2biTestKinds() {
			handlers[k.op] = func(in any) (any, error) {
				calls.record(k.op, in)
				return nil, nx
			}
		}
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(handlers, nx)
		_, _, err := scanB2BI(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("ListPartnerships"); n != 0 {
			t.Errorf("ListPartnerships called %d times; want 0 once the region proved unavailable", n)
		}
	})
}
