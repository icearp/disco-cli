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

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/appfabric"
	aftypes "github.com/aws/aws-sdk-go-v2/service/appfabric/types"
	"github.com/icearp/disco-cli/store"
)

const (
	// afTestTotal is every row of the fixture: two app bundles (one per
	// page); two app authorizations and two ingestions (one per page) in
	// each bundle; two destinations (one per page) of each of the four
	// ingestions.
	afTestTotal = 2 + 2*2 + 2*2 + 4*2
	afTestKMS   = "arn:aws:kms:us-east-1:123456789012:key/k-1"
)

var (
	afTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: appfabric:Op")
	afTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	afTestBad      = apiErr("ValidationException", "bad")
	afTestGone     = apiErr("ResourceNotFoundException", "gone")
)

// afKind is one listing of the fixture: its op, the type it stores, the ARN
// segment of its rows, the name prefix of its rows, the listing its parent
// comes from ("" for the bundles), and element, which builds the listed
// element of the row arn named name under parent (an ARN).
type afKind struct {
	op, typ, seg, short, parentOp string
	element                       func(parent, arn, name string) any
}

func afKinds() []afKind {
	return []afKind{
		{"ListAppBundles", TypeAppFabricAppBundle, "appbundle", "b", "", func(_, arn, _ string) any {
			return aftypes.AppBundleSummary{Arn: &arn}
		}},
		{"ListAppAuthorizations", TypeAppFabricAppAuthorization, "appauthorization", "a", "ListAppBundles", func(parent, arn, name string) any {
			return aftypes.AppAuthorizationSummary{
				AppAuthorizationArn: &arn, AppBundleArn: &parent, App: &name,
				Status: aftypes.AppAuthorizationStatusConnected, Tenant: &aftypes.Tenant{TenantIdentifier: sdkaws.String("t-1")},
			}
		}},
		{"ListIngestions", TypeAppFabricIngestion, "ingestion", "i", "ListAppBundles", func(_, arn, name string) any {
			return aftypes.IngestionSummary{Arn: &arn, App: &name, TenantId: sdkaws.String("t-1"), State: aftypes.IngestionStateEnabled}
		}},
		{"ListIngestionDestinations", TypeAppFabricIngestionDestination, "ingestiondestination", "d", "ListIngestions", func(_, arn, _ string) any {
			return aftypes.IngestionDestinationSummary{Arn: &arn}
		}},
	}
}

// afKindOf is the fixture's listing op; an unknown op is a fixture bug.
func afKindOf(op string) afKind {
	for _, k := range afKinds() {
		if k.op == op {
			return k
		}
	}
	panic(op + " is no AppFabric listing")
}

// afPage is op's output holding elements and next.
func afPage(op string, next *string, elements ...any) any {
	switch op {
	case "ListAppBundles":
		out := &appfabric.ListAppBundlesOutput{NextToken: next}
		for _, e := range elements {
			out.AppBundleSummaryList = append(out.AppBundleSummaryList, e.(aftypes.AppBundleSummary))
		}
		return out
	case "ListAppAuthorizations":
		out := &appfabric.ListAppAuthorizationsOutput{NextToken: next}
		for _, e := range elements {
			out.AppAuthorizationSummaryList = append(out.AppAuthorizationSummaryList, e.(aftypes.AppAuthorizationSummary))
		}
		return out
	case "ListIngestions":
		out := &appfabric.ListIngestionsOutput{NextToken: next}
		for _, e := range elements {
			out.Ingestions = append(out.Ingestions, e.(aftypes.IngestionSummary))
		}
		return out
	default:
		out := &appfabric.ListIngestionDestinationsOutput{NextToken: next}
		for _, e := range elements {
			out.IngestionDestinations = append(out.IngestionDestinations, e.(aftypes.IngestionDestinationSummary))
		}
		return out
	}
}

// afCalls records the calls of a scan and answers them for the fixture of
// one account and region (prefix: the start of every ARN); tagErrs and
// getErrs fail the tag read and the Get of an ARN, and the listing
// withoutARN names adds an element with an empty ARN to every first page.
type afCalls struct {
	prefix     string
	withoutARN string
	mu         sync.Mutex
	maxResults map[string][]int32
	parents    map[string][]string
	gets       map[string][]string
	tagARNs    []string
	tags       map[string]map[string]string
	tagErrs    map[string]error
	getErrs    map[string]error
}

func newAFCalls() *afCalls {
	return newAFCallsIn("aws", testRegion)
}

// newAFCallsIn tags every "-tagged" row with its type.
func newAFCallsIn(partition, region string) *afCalls {
	c := &afCalls{
		prefix:     "arn:" + partition + ":appfabric:" + region + ":" + testAccountID + ":",
		maxResults: map[string][]int32{}, parents: map[string][]string{}, gets: map[string][]string{},
		tags: map[string]map[string]string{}, tagErrs: map[string]error{}, getErrs: map[string]error{},
	}
	for _, k := range afKinds() {
		for _, arn := range c.arns(k) {
			if strings.HasSuffix(arn, "-tagged") {
				c.tags[arn] = map[string]string{"type": k.typ}
			}
		}
	}
	return c
}

// arn is the ARN of k's row on page suffix of the listing of parent, in the
// Service Reference's ARN formats.
func (c *afCalls) arn(k afKind, parent, suffix string) string {
	name := k.short + "-" + suffix
	if k.parentOp == "" {
		return c.prefix + k.seg + "/" + name
	}
	return parent + "/" + k.seg + "/" + name
}

// parentARNs is the ARN of every parent k's listing is called for; one ""
// for the bundles.
func (c *afCalls) parentARNs(k afKind) []string {
	if k.parentOp == "" {
		return []string{""}
	}
	var arns []string
	for _, p := range c.parentARNs(afKindOf(k.parentOp)) {
		pk := afKindOf(k.parentOp)
		arns = append(arns, c.arn(pk, p, "tagged"), c.arn(pk, p, "bare"))
	}
	return arns
}

// arns is the ARN of every row k's listing returns.
func (c *afCalls) arns(k afKind) []string {
	var arns []string
	for _, p := range c.parentARNs(k) {
		arns = append(arns, c.arn(k, p, "tagged"), c.arn(k, p, "bare"))
	}
	return arns
}

// allARNs is the ARN of every row of the fixture, sorted.
func (c *afCalls) allARNs() []string {
	var arns []string
	for _, k := range afKinds() {
		arns = append(arns, c.arns(k)...)
	}
	slices.Sort(arns)
	return arns
}

// record notes a listing call's MaxResults (0 for none) and, on a first
// page, the parent it names: "bundle" or "bundle ingestion".
func (c *afCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := int32(0)
	if m := bacField[*int32](in, "MaxResults"); m != nil {
		n = *m
	}
	c.maxResults[op] = append(c.maxResults[op], n)
	if bacField[*string](in, "NextToken") == nil {
		parent := strings.TrimSpace(sv(bacField[*string](in, "AppBundleIdentifier")) + " " + sv(bacField[*string](in, "IngestionIdentifier")))
		c.parents[op] = append(c.parents[op], parent)
	}
}

func (c *afCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.maxResults[op]) + len(c.gets[op])
}

func (c *afCalls) sorted(of []string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(of))
}

// list answers k's op with "tagged" on the first page and "bare" on the
// second, under the parent its input names.
func (c *afCalls) list(k afKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		parent := sv(bacField[*string](in, "IngestionIdentifier"))
		if parent == "" {
			parent = sv(bacField[*string](in, "AppBundleIdentifier"))
		}
		arn := c.arn(k, parent, suffix)
		elements := []any{k.element(parent, arn, k.short+"-"+suffix)}
		if k.op == c.withoutARN && suffix == "tagged" {
			elements = append(elements, k.element(parent, "", "x"))
		}
		return afPage(k.op, next, elements...), nil
	}
}

// get records a Get of arn under key and returns its getErrs entry.
func (c *afCalls) get(op, key, arn string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gets[op] = append(c.gets[op], key)
	return c.getErrs[arn]
}

func (c *afCalls) getBundle(in any) (any, error) {
	arn := sv(in.(*appfabric.GetAppBundleInput).AppBundleIdentifier)
	if err := c.get("GetAppBundle", arn, arn); err != nil {
		return nil, err
	}
	return &appfabric.GetAppBundleOutput{AppBundle: &aftypes.AppBundle{Arn: &arn, CustomerManagedKeyArn: sdkaws.String(afTestKMS)}}, nil
}

func (c *afCalls) getDestination(in any) (any, error) {
	req := in.(*appfabric.GetIngestionDestinationInput)
	arn := sv(req.IngestionDestinationIdentifier)
	if err := c.get("GetIngestionDestination", sv(req.AppBundleIdentifier)+" "+sv(req.IngestionIdentifier)+" "+arn, arn); err != nil {
		return nil, err
	}
	return &appfabric.GetIngestionDestinationOutput{IngestionDestination: afTestDestination(arn, sv(req.IngestionIdentifier))}, nil
}

// afTestDestination is the GetIngestionDestination body of destination arn
// of ingestion ing: an audit log sent to S3 bucket "bkt".
func afTestDestination(arn, ing string) *aftypes.IngestionDestination {
	return &aftypes.IngestionDestination{
		Arn: &arn, IngestionArn: &ing, Status: aftypes.IngestionDestinationStatusActive,
		DestinationConfiguration: &aftypes.DestinationConfigurationMemberAuditLog{Value: aftypes.AuditLogDestinationConfiguration{
			Destination: &aftypes.DestinationMemberS3Bucket{Value: aftypes.S3Bucket{BucketName: sdkaws.String("bkt")}},
		}},
	}
}

func (c *afCalls) listTags(in any) (any, error) {
	arn := sv(in.(*appfabric.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.tagErrs[arn]; err != nil {
		return nil, err
	}
	out := &appfabric.ListTagsForResourceOutput{Tags: []aftypes.Tag{}}
	for k, v := range c.tags[arn] {
		out.Tags = append(out.Tags, aftypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return out, nil
}

// handlers answers every op the scan calls.
func (c *afCalls) handlers() map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"GetAppBundle":            c.getBundle,
		"GetIngestionDestination": c.getDestination,
		"ListTagsForResource":     c.listTags,
	}
	for _, k := range afKinds() {
		h[k.op] = c.list(k)
	}
	return h
}

// afScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func afScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, afTestDenied)
	return scanAppFabric(ctx, acct, testRegion, st, testScanID)
}

func afWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// afJSON is v's JSON decoded into a map, for comparing attributes.
func afJSON(t *testing.T, v string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(v), &m); err != nil {
		t.Fatalf("decode %s: %v", v, err)
	}
	return m
}

// afSamePtr reports whether a and b are both nil or point to equal strings.
func afSamePtr(a, b *string) bool { return (a == nil) == (b == nil) && sv(a) == sv(b) }

// afSuffix is the page suffix ("tagged" or "bare") of a fixture ARN.
func afSuffix(arn string) string { return arn[strings.LastIndex(arn, "-")+1:] }

// afParent is the parent ARN of a fixture child ARN.
func afParent(k afKind, arn string) string { return arn[:strings.LastIndex(arn, "/"+k.seg+"/")] }

// Every listing stores both its pages, for every parent, each row under its
// ARN. A bundle is stored as its GetAppBundle body and a destination as its
// GetIngestionDestination body with the bucket lifted out; an authorization
// and an ingestion are stored as their listed element, named by their app,
// with their status. Each row has the tags its tag read returns (nil for
// none). ListTagsForResource is sent exactly the ARNs of every row, every
// listing the model's maxResults cap of 100, every child listing its
// parent's ARNs, and every Get its row's ARNs. No error, no warning.
func TestScanAppFabric_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newAFCalls()

	total, inserted, err := afScan(context.Background(), st, calls.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != afTestTotal || inserted != afTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, afTestTotal, afTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range afKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != len(calls.arns(k)) {
			t.Errorf("%s rows = %d; want %d", k.typ, len(rows), len(calls.arns(k)))
		}
		for _, arn := range calls.arns(k) {
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			name := k.short + "-" + afSuffix(arn)
			var wantName, wantStatus *string
			var wantAttrs any
			switch k.typ {
			case TypeAppFabricAppBundle:
				wantAttrs = aftypes.AppBundle{Arn: &arn, CustomerManagedKeyArn: sdkaws.String(afTestKMS)}
			case TypeAppFabricAppAuthorization:
				wantName, wantStatus = &name, sdkaws.String("Connected")
				wantAttrs = k.element(afParent(k, arn), arn, name)
			case TypeAppFabricIngestion:
				wantName, wantStatus = &name, sdkaws.String("enabled")
				wantAttrs = k.element(afParent(k, arn), arn, name)
			default:
				wantAttrs = ingestionDestAttrs{IngestionDestination: *afTestDestination(arn, afParent(k, arn)), S3BucketName: sdkaws.String("bkt")}
			}
			if !afSamePtr(r.Name, wantName) || !afSamePtr(r.Status, wantStatus) {
				t.Errorf("%s Name, Status = %v, %v; want %v, %v", arn, r.Name, r.Status, wantName, wantStatus)
			}
			if sv(r.Region) != testRegion || r.CreatedAt != nil {
				t.Errorf("%s Region, CreatedAt = %q, %v; want %q, nil", arn, sv(r.Region), r.CreatedAt, testRegion)
			}
			if got, want := afJSON(t, r.AttributesJSON), afJSON(t, mustJSON(wantAttrs)); !reflect.DeepEqual(got, want) {
				t.Errorf("%s attrs = %v\nwant %v", arn, got, want)
			}
			if afSuffix(arn) == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	if got := calls.sorted(calls.tagARNs); !slices.Equal(got, calls.allARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, calls.allARNs())
	}
	bundles := calls.arns(afKindOf("ListAppBundles"))
	ingestions := calls.arns(afKindOf("ListIngestions"))
	var perIngestion, destGets []string
	for _, ing := range ingestions {
		perIngestion = append(perIngestion, afParent(afKindOf("ListIngestions"), ing)+" "+ing)
	}
	for _, dest := range calls.arns(afKindOf("ListIngestionDestinations")) {
		ing := afParent(afKindOf("ListIngestionDestinations"), dest)
		destGets = append(destGets, afParent(afKindOf("ListIngestions"), ing)+" "+ing+" "+dest)
	}
	slices.Sort(bundles)
	slices.Sort(perIngestion)
	slices.Sort(destGets)
	for op, want := range map[string][]string{
		"ListAppBundles":            {""},
		"ListAppAuthorizations":     bundles,
		"ListIngestions":            bundles,
		"ListIngestionDestinations": perIngestion,
	} {
		if got := calls.sorted(calls.parents[op]); !slices.Equal(got, want) {
			t.Errorf("%s parents = %v; want %v", op, got, want)
		}
		if n := calls.count(op); n != 2*len(want) {
			t.Errorf("%s calls = %d; want %d", op, n, 2*len(want))
		}
		for _, n := range calls.maxResults[op] {
			if n != 100 {
				t.Errorf("%s MaxResults = %d; want 100", op, n)
			}
		}
	}
	if got := calls.sorted(calls.gets["GetAppBundle"]); !slices.Equal(got, bundles) {
		t.Errorf("GetAppBundle ARNs = %v; want %v", got, bundles)
	}
	if got := calls.sorted(calls.gets["GetIngestionDestination"]); !slices.Equal(got, destGets) {
		t.Errorf("GetIngestionDestination inputs = %v\nwant %v", got, destGets)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanAppFabric_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := afScan(context.Background(), st, newAFCalls().handlers()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := afScan(context.Background(), st, newAFCalls().handlers())
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != afTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, afTestTotal)
	}
}

// No bundle: nothing stored, no other call, no error, no warning.
func TestScanAppFabric_NoBundles(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newAFCalls()
	handlers := calls.handlers()
	handlers["ListAppBundles"] = func(in any) (any, error) {
		calls.record("ListAppBundles", in)
		return &appfabric.ListAppBundlesOutput{}, nil
	}

	total, inserted, err := afScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListAppAuthorizations", "ListIngestions", "ListIngestionDestinations", "GetAppBundle", "GetIngestionDestination"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
	if len(calls.tagARNs) != 0 {
		t.Errorf("ListTagsForResource called for %v; want no call", calls.tagARNs)
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently (a bundle with its
// children, which are not listed). A denial keeps the row untagged and warns
// once under the tag op with the scan's scope; a throttle keeps it and is
// transient; any other error keeps it and is an error.
func TestScanAppFabric_TagReadFailures(t *testing.T) {
	c0 := newAFCalls()
	dest := c0.arns(afKindOf("ListIngestionDestinations"))[0]
	auth := c0.arns(afKindOf("ListAppAuthorizations"))[1]
	bundle := c0.arns(afKindOf("ListAppBundles"))[1]
	// A bundle's two authorizations, two ingestions and their four
	// destinations.
	bundleAndChildren := 1 + 2 + 2 + 2*2
	for _, c := range []struct {
		name      string
		arn, typ  string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"destination/not found", dest, TypeAppFabricIngestionDestination, afTestGone, -1, "", afTestTotal - 1, false, false},
		{"destination/denied", dest, TypeAppFabricIngestionDestination, afTestDenied, -1, "", afTestTotal, true, true},
		{"destination/transient", dest, TypeAppFabricIngestionDestination, afTestThrottle, outcomeTransient, "ThrottlingException", afTestTotal, true, false},
		{"destination/other error", dest, TypeAppFabricIngestionDestination, afTestBad, outcomeError, "ValidationException", afTestTotal, true, false},
		{"authorization/not found", auth, TypeAppFabricAppAuthorization, afTestGone, -1, "", afTestTotal - 1, false, false},
		{"authorization/other error", auth, TypeAppFabricAppAuthorization, afTestBad, outcomeError, "ValidationException", afTestTotal, true, false},
		{"bundle/not found", bundle, TypeAppFabricAppBundle, afTestGone, -1, "", afTestTotal - bundleAndChildren, false, false},
		{"bundle/denied", bundle, TypeAppFabricAppBundle, afTestDenied, -1, "", afTestTotal, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newAFCalls()
			calls.tagErrs[c.arn] = c.err

			total, _, err := afScan(context.Background(), st, calls.handlers())
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
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{afTagsOp}
			}
			if got := afWarnOps(w); !slices.Equal(got, want) {
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
func TestScanAppFabric_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newAFCalls()
	for _, op := range []string{"ListAppBundles", "ListIngestions", "ListIngestionDestinations"} {
		for _, arn := range calls.arns(afKindOf(op)) {
			calls.tagErrs[arn] = afTestDenied
		}
	}

	total, _, err := afScan(context.Background(), st, calls.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != afTestTotal {
		t.Errorf("total = %d; want %d", total, afTestTotal)
	}
	if got := afWarnOps(warns()); !slices.Equal(got, []string{afTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, afTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanAppFabric_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, afTagsOp)
	calls := newAFCalls()
	calls.tagErrs[calls.arns(afKindOf("ListIngestions"))[0]] = afTestThrottle
	calls.tagErrs[calls.arns(afKindOf("ListIngestionDestinations"))[3]] = afTestBad

	_, _, err := afScan(context.Background(), st, calls.handlers())
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", afTagsOp, *warns)
	}
}

// A failed GetAppBundle or GetIngestionDestination. ResourceNotFoundException
// is modeled, so the resource was deleted since it was listed: its row is
// dropped without a tag read (a bundle's children are not listed). Any other
// failure, or a body-less answer, stores the row as its listed element with
// its tags: a denial warns once under the Get op, a throttle is transient,
// any other error is an error.
func TestScanAppFabric_GetFailures(t *testing.T) {
	c0 := newAFCalls()
	bundle := c0.arns(afKindOf("ListAppBundles"))[0]
	dest := c0.arns(afKindOf("ListIngestionDestinations"))[2]
	bundleAndChildren := 1 + 2 + 2 + 2*2
	for _, getter := range []struct {
		op, arn, typ string
		lost         int
		empty        func() any
	}{
		{"GetAppBundle", bundle, TypeAppFabricAppBundle, bundleAndChildren, func() any { return &appfabric.GetAppBundleOutput{} }},
		{"GetIngestionDestination", dest, TypeAppFabricIngestionDestination, 1, func() any { return &appfabric.GetIngestionDestinationOutput{} }},
	} {
		for _, c := range []struct {
			name     string
			err      error
			outcome  serviceOutcome // -1: no error
			code     string
			wantKept bool
			warn     bool
		}{
			{"not found", afTestGone, -1, "", false, false},
			{"denied", afTestDenied, -1, "", true, true},
			{"transient", afTestThrottle, outcomeTransient, "ThrottlingException", true, false},
			{"other error", afTestBad, outcomeError, "ValidationException", true, false},
			{"no body", nil, -1, "", true, false},
		} {
			t.Run(getter.op+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newAFCalls()
				handlers := calls.handlers()
				if c.err != nil {
					calls.getErrs[getter.arn] = c.err
				} else {
					get := handlers[getter.op]
					handlers[getter.op] = func(in any) (any, error) {
						if sv(bacField[*string](in, "AppBundleIdentifier")) == getter.arn || sv(bacField[*string](in, "IngestionDestinationIdentifier")) == getter.arn {
							return getter.empty(), nil
						}
						return get(in)
					}
				}

				total, _, err := afScan(context.Background(), st, handlers)
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				}
				wantTotal := afTestTotal
				if !c.wantKept {
					wantTotal -= getter.lost
				}
				if total != wantTotal {
					t.Errorf("total = %d; want %d", total, wantTotal)
				}
				r, ok := rowsByNativeID(t, st, getter.typ)[getter.arn]
				if ok != c.wantKept {
					t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
				}
				if read := slices.Contains(calls.sorted(calls.tagARNs), getter.arn); read != c.wantKept {
					t.Errorf("tags read = %v; want %v", read, c.wantKept)
				}
				if ok {
					if got, want := afJSON(t, r.AttributesJSON), map[string]any{"Arn": getter.arn}; !reflect.DeepEqual(got, want) {
						t.Errorf("attrs = %v; want the listed element %v", got, want)
					}
					assertTagsMap(t, r.TagsJSON, map[string]string{"type": getter.typ})
				}
				var want []string
				if c.warn {
					want = []string{"appfabric:" + getter.op}
				}
				if got := afWarnOps(warns()); !slices.Equal(got, want) {
					t.Errorf("warnings = %v; want %v", got, want)
				}
			})
		}
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read or Get among them), no later phase runs, and the earlier phases'
// rows stay stored. Where the failing phase's first page is written and only
// a later page refused, the phase has rows to be the later phases' parents,
// so it is the failure alone that keeps those phases from running.
func TestScanAppFabric_FailedWriteStopsScan(t *testing.T) {
	c0 := newAFCalls()
	bundles := c0.arns(afKindOf("ListAppBundles"))
	ingestions := c0.arns(afKindOf("ListIngestions"))
	for _, c := range []struct {
		name, typ, op string
		// onlyBare refuses only the "-bare" rows, of the second pages.
		onlyBare  bool
		kept      string
		notCalled []string
		wantTotal int
	}{
		{"bundle second page", TypeAppFabricAppBundle, "ListAppBundles", true, bundles[0], []string{"ListAppAuthorizations", "ListIngestions", "ListIngestionDestinations"}, 1},
		// The bundles.
		{"authorizations", TypeAppFabricAppAuthorization, "ListAppAuthorizations", false, "", []string{"ListIngestions", "ListIngestionDestinations", "GetIngestionDestination"}, 2},
		// The bundles, the authorizations and b-tagged's first ingestion;
		// b-bare lists no ingestion, so which bundle's write fails first
		// cannot change what is stored.
		{"ingestion second page", TypeAppFabricIngestion, "ListIngestions", true, ingestions[0], []string{"ListIngestionDestinations", "GetIngestionDestination"}, 2 + 4 + 1},
		{"destinations", TypeAppFabricIngestionDestination, "ListIngestionDestinations", false, "", nil, afTestTotal - 8},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			if c.onlyBare {
				if _, err := st.DB().Exec(`CREATE TRIGGER refuse_bare BEFORE INSERT ON resources
					WHEN NEW.type = '` + c.typ + `' AND NEW.native_id LIKE '%-bare'
					BEGIN SELECT RAISE(ABORT, 'test: write refused'); END`); err != nil {
					t.Fatalf("create trigger: %v", err)
				}
			} else {
				refuseStoreWrites(t, st, c.typ)
			}
			calls := newAFCalls()
			handlers := calls.handlers()
			if c.op == "ListIngestions" {
				list := handlers[c.op]
				handlers[c.op] = func(in any) (any, error) {
					if sv(bacField[*string](in, "AppBundleIdentifier")) == bundles[1] {
						calls.record(c.op, in)
						return afPage(c.op, nil), nil
					}
					return list(in)
				}
			}
			// A row the failed write lost: the second of the first parent.
			lost := calls.arns(afKindOf(c.op))[1]
			calls.tagErrs[lost] = afTestDenied
			calls.getErrs[lost] = afTestDenied

			total, _, err := afScan(context.Background(), st, handlers)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			rows := rowsByNativeID(t, st, c.typ)
			if _, ok := rows[lost]; ok {
				t.Errorf("%s stored; its write was refused", lost)
			}
			if _, ok := rows[c.kept]; c.kept != "" && !ok {
				t.Errorf("%s, of the page written before the failure, missing", c.kept)
			}
			for _, op := range c.notCalled {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times; want 0 after the failed write", op, n)
				}
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
		})
	}
}

// Several elements on one page each become a parent: every bundle of the
// one page of bundles has its authorizations and ingestions listed, and
// every ingestion of a bundle's one page its destinations, sent with the
// bundle it was listed under.
func TestScanAppFabric_EveryElementOfAPageIsAParent(t *testing.T) {
	st := newTestStore(t)
	calls := newAFCalls()
	handlers := calls.handlers()
	bundleKind, ingestionKind := afKindOf("ListAppBundles"), afKindOf("ListIngestions")
	names := []string{"one", "two", "three"}
	var bundles, perIngestion []string
	var bundleElements []any
	for _, n := range names {
		arn := calls.arn(bundleKind, "", n)
		bundles = append(bundles, arn)
		bundleElements = append(bundleElements, bundleKind.element("", arn, n))
		for _, in := range names {
			perIngestion = append(perIngestion, arn+" "+calls.arn(ingestionKind, arn, in))
		}
	}
	handlers["ListAppBundles"] = func(in any) (any, error) {
		calls.record("ListAppBundles", in)
		return afPage("ListAppBundles", nil, bundleElements...), nil
	}
	handlers["ListIngestions"] = func(in any) (any, error) {
		calls.record("ListIngestions", in)
		bundle := sv(bacField[*string](in, "AppBundleIdentifier"))
		var elements []any
		for _, n := range names {
			elements = append(elements, ingestionKind.element(bundle, calls.arn(ingestionKind, bundle, n), n))
		}
		return afPage("ListIngestions", nil, elements...), nil
	}

	if _, _, err := afScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	slices.Sort(bundles)
	slices.Sort(perIngestion)
	for op, want := range map[string][]string{
		"ListAppAuthorizations":     bundles,
		"ListIngestions":            bundles,
		"ListIngestionDestinations": perIngestion,
	} {
		if got := calls.sorted(calls.parents[op]); !slices.Equal(got, want) {
			t.Errorf("%s parents = %v\nwant %v", op, got, want)
		}
	}
}

// A listing's failed later page keeps the pages read before it, and its
// error does not stop the later phases. A bundle not stored has no children
// read.
func TestScanAppFabric_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	c0 := newAFCalls()
	bundles := c0.arns(afKindOf("ListAppBundles"))
	ingestions := c0.arns(afKindOf("ListIngestions"))
	for _, c := range []struct {
		op, parent, kept, lost string
		wantTotal, wantDests   int
	}{
		// The bundles' second page: b-bare and its children are lost.
		{"ListAppBundles", "", bundles[0], bundles[1], afTestTotal - (1 + 2 + 2 + 4), 4},
		// b-tagged's second page of ingestions: i-bare and its two
		// destinations are lost.
		{"ListIngestions", bundles[0], ingestions[0], ingestions[1], afTestTotal - 3, 6},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newAFCalls()
			handlers := calls.handlers()
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil && sv(bacField[*string](in, "AppBundleIdentifier")) == c.parent {
					calls.record(c.op, in)
					return nil, afTestBad
				}
				return list(in)
			}

			total, _, err := afScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			rows := rowsByNativeID(t, st, afKindOf(c.op).typ)
			if _, ok := rows[c.kept]; !ok {
				t.Errorf("%s, read before the failure, missing", c.kept)
			}
			if _, ok := rows[c.lost]; ok {
				t.Errorf("%s stored; its page failed", c.lost)
			}
			if got := len(rowsByNativeID(t, st, TypeAppFabricIngestionDestination)); got != c.wantDests {
				t.Errorf("destinations (the last phase) = %d; want %d", got, c.wantDests)
			}
		})
	}
}

// A repeated nextToken ends the listing as an error, keeping its pages.
func TestScanAppFabric_RepeatedTokenKeepsPages(t *testing.T) {
	for _, op := range []string{"ListAppAuthorizations", "ListIngestionDestinations"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newAFCalls()
			handlers := calls.handlers()
			list := handlers[op]
			handlers[op] = func(in any) (any, error) {
				out, err := list(in)
				switch o := out.(type) {
				case *appfabric.ListAppAuthorizationsOutput:
					o.NextToken = sdkaws.String("same")
				case *appfabric.ListIngestionDestinationsOutput:
					o.NextToken = sdkaws.String("same")
				}
				return out, err
			}

			total, _, err := afScan(context.Background(), st, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != afTestTotal {
				t.Errorf("total = %d; want %d", total, afTestTotal)
			}
			k := afKindOf(op)
			if n := len(rowsByNativeID(t, st, k.typ)); n != len(calls.arns(k)) {
				t.Errorf("%s rows = %d; want %d", k.typ, n, len(calls.arns(k)))
			}
		})
	}
}

// One parent's failed child listing costs the other parents none of their
// rows: a parent deleted since it was listed is skipped silently, a denial
// warns once under the op, and any other error is returned.
func TestScanAppFabric_PerParentListingFailures(t *testing.T) {
	c0 := newAFCalls()
	bundle := c0.arns(afKindOf("ListAppBundles"))[0]
	ingestion := c0.arns(afKindOf("ListIngestions"))[3]
	for _, c := range []struct {
		op, field, parent string
		// alsoLost counts the rows of later phases the failed parent's
		// children would have parented.
		alsoLost int
	}{
		{"ListAppAuthorizations", "AppBundleIdentifier", bundle, 0},
		{"ListIngestions", "AppBundleIdentifier", bundle, 2 * 2},
		{"ListIngestionDestinations", "IngestionIdentifier", ingestion, 0},
	} {
		for _, e := range []struct {
			name   string
			err    error
			code   string
			denied bool
		}{
			{"parent deleted", afTestGone, "", false},
			{"denied", afTestDenied, "", true},
			{"other error", afTestBad, "ValidationException", false},
		} {
			t.Run(c.op+"/"+e.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newAFCalls()
				handlers := calls.handlers()
				list := handlers[c.op]
				handlers[c.op] = func(in any) (any, error) {
					if sv(bacField[*string](in, c.field)) == c.parent {
						calls.record(c.op, in)
						return nil, e.err
					}
					return list(in)
				}

				total, _, err := afScan(context.Background(), st, handlers)
				if e.code == "" {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, outcomeError)
					assertAPICode(t, err, e.code)
				}
				k := afKindOf(c.op)
				var lost []string
				for _, arn := range calls.arns(k) {
					if strings.HasPrefix(arn, c.parent+"/") {
						lost = append(lost, arn)
					}
				}
				rows := rowsByNativeID(t, st, k.typ)
				if len(lost) != 2 || len(rows) != len(calls.arns(k))-len(lost) {
					t.Errorf("%s rows = %d, lost %d; want %d, 2", k.typ, len(rows), len(lost), len(calls.arns(k))-2)
				}
				for _, arn := range lost {
					if _, ok := rows[arn]; ok {
						t.Errorf("%s stored; its parent failed", arn)
					}
				}
				if want := afTestTotal - len(lost) - c.alsoLost; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				var want []string
				if e.denied {
					want = []string{"appfabric:" + c.op}
				}
				if got := afWarnOps(warns()); !slices.Equal(got, want) {
					t.Errorf("warnings = %v; want %v", got, want)
				}
			})
		}
	}
}

// An element without an ARN is not stored, has no Get or tag read, and is
// warned about once under its listing; its siblings are stored. The fixture
// adds one to the first page of every parent's listing.
func TestScanAppFabric_ElementWithoutARN(t *testing.T) {
	for _, k := range afKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newAFCalls()
			calls.withoutARN = k.op

			total, _, err := afScan(context.Background(), st, calls.handlers())
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != afTestTotal {
				t.Errorf("total = %d; want %d", total, afTestTotal)
			}
			if _, ok := rowsByNativeID(t, st, k.typ)[""]; ok {
				t.Error("a row with an empty NativeID was stored")
			}
			if got := afWarnOps(warns()); !slices.Equal(got, []string{"appfabric:" + k.op}) {
				t.Errorf("warnings = %v; want one under appfabric:%s", got, k.op)
			}
			if got := calls.sorted(calls.tagARNs); !slices.Equal(got, calls.allARNs()) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, calls.allARNs())
			}
			if n := calls.count("GetAppBundle"); n != 2 {
				t.Errorf("GetAppBundle calls = %d; want 2, one per bundle stored", n)
			}
			if n := calls.count("GetIngestionDestination"); n != 8 {
				t.Errorf("GetIngestionDestination calls = %d; want 8, one per destination stored", n)
			}
		})
	}
}

// A GovCloud scan sends its rows' ARNs to every child listing, Get and tag
// read unchanged.
func TestScanAppFabric_GovCloudARNsUnchanged(t *testing.T) {
	const region = "us-gov-west-1"
	st := newTestStore(t)
	calls := newAFCallsIn("aws-us-gov", region)
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(calls.handlers(), afTestDenied)

	total, _, err := scanAppFabric(context.Background(), acct, region, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != afTestTotal {
		t.Errorf("total = %d; want %d", total, afTestTotal)
	}
	all := calls.allARNs()
	if !strings.HasPrefix(all[0], "arn:aws-us-gov:appfabric:us-gov-west-1:") {
		t.Fatalf("fixture ARN %s is not a GovCloud ARN", all[0])
	}
	if got := calls.sorted(calls.tagARNs); !slices.Equal(got, all) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, all)
	}
	if got, want := calls.sorted(calls.gets["GetAppBundle"]), calls.sorted(calls.arns(afKindOf("ListAppBundles"))); !slices.Equal(got, want) {
		t.Errorf("GetAppBundle ARNs = %v; want %v", got, want)
	}
	dest := calls.arns(afKindOf("ListIngestionDestinations"))[0]
	r := rowsByNativeID(t, st, TypeAppFabricIngestionDestination)[dest]
	assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeAppFabricIngestionDestination})
	if sv(r.Region) != region {
		t.Errorf("Region = %q; want %s", sv(r.Region), region)
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing and its tag reads, and the later phases are
// not run.
func TestScanAppFabric_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newAFCalls()
	handlers := calls.handlers()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListAppAuthorizations"]
	handlers["ListAppAuthorizations"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := afScan(ctx, st, handlers)
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
	if n := calls.count("ListIngestions"); n != 0 {
		t.Errorf("ListIngestions called %d times after ctx ended", n)
	}
}

// Once ctx ends while the destination listings run, the ingestions not yet
// read are reported as the stop, although every listing that ran answered
// without error. More ingestions than fanoutMed are listed, so the one after
// the first fanoutMed waits for a slot, which only a finished listing frees,
// and every listing cancels ctx before it finishes.
func TestScanAppFabric_ContextEndDuringDestinations(t *testing.T) {
	st := newTestStore(t)
	calls := newAFCalls()
	handlers := calls.handlers()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handlers["ListIngestions"] = func(in any) (any, error) {
		bundle := sv(bacField[*string](in, "AppBundleIdentifier"))
		var many []any
		for i := range fanoutMed + 2 {
			arn := bundle + "/ingestion/i-" + string(rune('a'+i))
			many = append(many, afKindOf("ListIngestions").element(bundle, arn, "i"))
		}
		return afPage("ListIngestions", nil, many...), nil
	}
	handlers["ListIngestionDestinations"] = func(in any) (any, error) {
		calls.record("ListIngestionDestinations", in)
		cancel()
		return &appfabric.ListIngestionDestinationsOutput{}, nil
	}

	_, _, err := afScan(ctx, st, handlers)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	if n := calls.count("ListIngestionDestinations"); n >= 2*(fanoutMed+2) {
		t.Errorf("ListIngestionDestinations called %d times; want fewer than every ingestion", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanAppFabric_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "appfabric.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := newAFCalls().handlers()
		handlers["ListIngestionDestinations"] = func(any) (any, error) { return nil, nx }
		total, _, err := afScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := afTestTotal - 8; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("Get after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newAFCalls()
		calls.getErrs[calls.arns(afKindOf("ListIngestionDestinations"))[1]] = nx
		total, _, err := afScan(context.Background(), st, calls.handlers())
		assertGlueOutcome(t, err, outcomeError)
		if total != afTestTotal {
			t.Errorf("total = %d; want %d", total, afTestTotal)
		}
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanAppFabric(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
