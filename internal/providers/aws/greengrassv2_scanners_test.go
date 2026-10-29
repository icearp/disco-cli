package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/greengrassv2"
	ggv2types "github.com/aws/aws-sdk-go-v2/service/greengrassv2/types"
	"github.com/icearp/disco-cli/store"
)

// ggv2TestTotal is every row of the fixture: two components, core devices
// and deployments (one per page), and two versions (one per page) of each
// component.
const ggv2TestTotal = 2*3 + 2*2

var (
	ggv2TestDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: greengrass:List")
	ggv2TestThrottle   = apiErr("ThrottlingException", "Rate exceeded")
	ggv2TestBad        = apiErr("ValidationException", "bad")
	ggv2TestGone       = apiErr("ResourceNotFoundException", "gone")
	ggv2TestComponents = []string{"comp-bare", "comp-tagged"}
	ggv2TestCreated    = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

// ggv2TestARN is the commercial ARN of the resource {kind}:{name}, kind
// components, coreDevices or deployments.
func ggv2TestARN(kind, name string) string {
	return "arn:aws:greengrass:" + testRegion + ":" + testAccountID + ":" + kind + ":" + name
}

// ggv2TestVersionARN is the ARN of version of component.
func ggv2TestVersionARN(component, version string) string {
	return ggv2TestARN("components", component) + ":versions:" + version
}

// ggv2TestVersion is the second page's version for a "-bare" suffix and the
// first page's otherwise.
func ggv2TestVersion(suffix string) string {
	if suffix == "bare" {
		return "2.0.0"
	}
	return "1.0.0"
}

// ggv2Kind is one account-wide listing: its op, the type it stores, its ARN's
// resource kind, the prefix of its elements' ids, the Status its elements
// carry ("" for none) and the attribute holding an element's id.
type ggv2Kind struct {
	op, typ, kind, short, status, idField string
}

func ggv2Kinds() []ggv2Kind {
	return []ggv2Kind{
		{"ListComponents", TypeGreengrassV2Component, "components", "comp", "", "Arn"},
		{"ListCoreDevices", TypeGreengrassV2CoreDevice, "coreDevices", "thing", "HEALTHY", "CoreDeviceThingName"},
		{"ListDeployments", TypeGreengrassV2Deployment, "deployments", "dep", "ACTIVE", "DeploymentId"},
	}
}

func ggv2KindOf(t *testing.T, op string) ggv2Kind {
	t.Helper()
	for _, k := range ggv2Kinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no Greengrass v2 account-wide listing", op)
	return ggv2Kind{}
}

// ggv2ID is the id of k's element with suffix.
func (k ggv2Kind) ggv2ID(suffix string) string { return k.short + "-" + suffix }

// ggv2RowARN is the ARN of k's element with suffix.
func (k ggv2Kind) ggv2RowARN(suffix string) string { return ggv2TestARN(k.kind, k.ggv2ID(suffix)) }

// ggv2Name is the Name of k's row with suffix.
func (k ggv2Kind) ggv2Name(suffix string) string {
	if k.op == "ListDeployments" {
		return "name-" + k.ggv2ID(suffix)
	}
	return k.ggv2ID(suffix)
}

// ggv2Page is k's output holding the element with suffix (its ARN arn for
// a component), and next.
func (k ggv2Kind) ggv2Page(suffix string, arn, next *string) any {
	id := k.ggv2ID(suffix)
	switch k.op {
	case "ListComponents":
		return &greengrassv2.ListComponentsOutput{NextToken: next, Components: []ggv2types.Component{
			{Arn: arn, ComponentName: &id},
		}}
	case "ListCoreDevices":
		return &greengrassv2.ListCoreDevicesOutput{NextToken: next, CoreDevices: []ggv2types.CoreDevice{
			{CoreDeviceThingName: &id, Status: ggv2types.CoreDeviceStatus(k.status), Platform: sdkaws.String("linux")},
		}}
	default:
		return &greengrassv2.ListDeploymentsOutput{NextToken: next, Deployments: []ggv2types.Deployment{
			{DeploymentId: &id, DeploymentName: sdkaws.String(k.ggv2Name(suffix)), DeploymentStatus: ggv2types.DeploymentStatus(k.status), CreationTimestamp: &ggv2TestCreated},
		}}
	}
}

// ggv2Calls records the calls of a scan and answers the tag reads, by ARN,
// with tags or errs.
type ggv2Calls struct {
	mu         sync.Mutex
	maxResults map[string][]int32
	calls      map[string]int
	scopes     []ggv2types.ComponentVisibilityScope
	deployIns  []greengrassv2.ListDeploymentsInput
	versionsOf []string
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newGGV2Calls tags every "-tagged" row and each component's version 1.0.0
// with its type.
func newGGV2Calls() *ggv2Calls {
	c := &ggv2Calls{maxResults: map[string][]int32{}, calls: map[string]int{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range ggv2Kinds() {
		c.tags[k.ggv2RowARN("tagged")] = map[string]string{"type": k.typ}
	}
	for _, comp := range ggv2TestComponents {
		c.tags[ggv2TestVersionARN(comp, "1.0.0")] = map[string]string{"type": TypeGreengrassV2ComponentVersion}
	}
	return c
}

// ggv2Record notes op's call and its MaxResults, a ListComponents call's
// Scope, a ListDeployments call's input, and on a first
// ListComponentVersions page the component it lists.
func (c *ggv2Calls) ggv2Record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[op]++
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		c.maxResults[op] = append(c.maxResults[op], *n)
	}
	switch v := in.(type) {
	case *greengrassv2.ListComponentsInput:
		c.scopes = append(c.scopes, v.Scope)
	case *greengrassv2.ListDeploymentsInput:
		c.deployIns = append(c.deployIns, *v)
	case *greengrassv2.ListComponentVersionsInput:
		if v.NextToken == nil {
			c.versionsOf = append(c.versionsOf, sv(v.Arn))
		}
	}
}

func (c *ggv2Calls) ggv2Count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

func (c *ggv2Calls) ggv2SortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *ggv2Calls) ggv2ListTags(in any) (any, error) {
	arn := sv(in.(*greengrassv2.ListTagsForResourceInput).ResourceArn)
	c.ggv2Record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	return &greengrassv2.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

// ggv2TwoPages answers k's op with "-tagged" on the first page and "-bare"
// on the second.
func (c *ggv2Calls) ggv2TwoPages(k ggv2Kind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.ggv2Record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.ggv2Page(suffix, sdkaws.String(k.ggv2RowARN(suffix)), next), nil
	}
}

// ggv2Versions answers ListComponentVersions with version 1.0.0 of the
// component on the first page and 2.0.0 on the second.
func (c *ggv2Calls) ggv2Versions(in any) (any, error) {
	c.ggv2Record("ListComponentVersions", in)
	req := in.(*greengrassv2.ListComponentVersionsInput)
	suffix, next := "tagged", sdkaws.String("p2")
	if req.NextToken != nil {
		suffix, next = "bare", nil
	}
	return &greengrassv2.ListComponentVersionsOutput{NextToken: next, ComponentVersions: []ggv2types.ComponentVersionListItem{
		ggv2TestVersionItem(sv(req.Arn), ggv2TestVersion(suffix)),
	}}, nil
}

// ggv2TestVersionItem is the listed version of the component whose ARN is
// componentARN.
func ggv2TestVersionItem(componentARN, version string) ggv2types.ComponentVersionListItem {
	name := strings.TrimPrefix(componentARN, ggv2TestARN("components", ""))
	return ggv2types.ComponentVersionListItem{
		Arn: sdkaws.String(componentARN + ":versions:" + version), ComponentName: &name, ComponentVersion: &version,
	}
}

// ggv2Handlers answers every op the scan calls from c.
func ggv2Handlers(c *ggv2Calls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"ListTagsForResource":   c.ggv2ListTags,
		"ListComponentVersions": c.ggv2Versions,
	}
	for _, k := range ggv2Kinds() {
		h[k.op] = c.ggv2TwoPages(k)
	}
	return h
}

// ggv2Scan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func ggv2Scan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, ggv2TestDenied)
	return scanGreengrassV2(ctx, acct, testRegion, st, testScanID)
}

// ggv2AllTagARNs is the ARN of every row of the fixture, sorted.
func ggv2AllTagARNs() []string {
	var arns []string
	for _, k := range ggv2Kinds() {
		arns = append(arns, k.ggv2RowARN("tagged"), k.ggv2RowARN("bare"))
	}
	for _, comp := range ggv2TestComponents {
		arns = append(arns, ggv2TestVersionARN(comp, "1.0.0"), ggv2TestVersionARN(comp, "2.0.0"))
	}
	slices.Sort(arns)
	return arns
}

func ggv2WarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// ggv2Attrs decodes a row's attributes.
func ggv2Attrs(t *testing.T, r store.Resource) map[string]any {
	t.Helper()
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("%s attrs %s: %v", r.NativeID, r.AttributesJSON, err)
	}
	return attrs
}

// Greengrass V2 makes no call in a region its SDK table omits, where AWS would
// answer with retried 429s, and does call in one the table lists.
func TestScanGreengrassV2_GatesOnSDKTable(t *testing.T) {
	for region, wantCalls := range map[string]bool{"eu-west-3": false, "us-gov-west-1": true} {
		t.Run(region, func(t *testing.T) {
			var regions parentCalls
			acct := testAccountIn("aws-us-gov")
			if region == "eu-west-3" {
				acct = testAccountIn("aws")
			}
			acct.cfg = recordCallRegions(opStubConfig(nil, apiErr("AccessDeniedException", "denied")), &regions)
			if _, _, err := scanGreengrassV2(context.Background(), acct, region, newTestStore(t), testScanID); err != nil {
				t.Fatalf("scanGreengrassV2(%s): %v", region, err)
			}
			if got := len(regions.distinct()) > 0; got != wantCalls {
				t.Errorf("scanGreengrassV2(%s) made calls = %v; want %v", region, got, wantCalls)
			}
		})
	}
}

// Every listing stores both its pages, each row under its ARN (a core
// device's and a deployment's built as coreDevices:{thing} and
// deployments:{id}), named by its name, with its Status (none for a
// component), no CreatedAt, its listed element as attributes, and the tags
// its tag read returns (nil for none). A component version is named
// {component}:{version}. ListTagsForResource is sent exactly the ARNs of
// every row; every listing is sent MaxResults 100, ListComponents Scope
// PRIVATE; versions are listed for both components. No error, no warning.
func TestScanGreengrassV2_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newGGV2Calls()

	total, inserted, err := ggv2Scan(context.Background(), st, ggv2Handlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != ggv2TestTotal || inserted != ggv2TestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, ggv2TestTotal, ggv2TestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range ggv2Kinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", k.typ, len(rows))
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := k.ggv2RowARN(suffix)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if want := k.ggv2Name(suffix); sv(r.Name) != want || sv(r.Region) != testRegion || sv(r.Status) != k.status || r.CreatedAt != nil {
				t.Errorf("%s Name, Region, Status, CreatedAt = %q, %q, %v, %v; want %q, %q, %q, nil", arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, want, testRegion, k.status)
			}
			if k.status == "" && r.Status != nil {
				t.Errorf("%s Status = %q; want nil", arn, *r.Status)
			}
			wantID := k.ggv2ID(suffix)
			if k.idField == "Arn" {
				wantID = arn
			}
			if attrs := ggv2Attrs(t, r); attrs[k.idField] != wantID {
				t.Errorf("%s attrs = %s; want its element", arn, r.AttributesJSON)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	if attrs := ggv2Attrs(t, rowsByNativeID(t, st, TypeGreengrassV2Deployment)[ggv2TestARN("deployments", "dep-tagged")]); attrs["CreationTimestamp"] != ggv2TestCreated.Format(time.RFC3339) {
		t.Errorf("deployment CreationTimestamp attr = %v; want %s", attrs["CreationTimestamp"], ggv2TestCreated.Format(time.RFC3339))
	}
	versions := rowsByNativeID(t, st, TypeGreengrassV2ComponentVersion)
	if len(versions) != 4 {
		t.Errorf("component versions = %d; want 4", len(versions))
	}
	for _, comp := range ggv2TestComponents {
		for _, v := range []string{"1.0.0", "2.0.0"} {
			arn := ggv2TestVersionARN(comp, v)
			r, ok := versions[arn]
			if !ok {
				t.Errorf("component version %s missing", arn)
				continue
			}
			if want := comp + ":" + v; sv(r.Name) != want || sv(r.Region) != testRegion || r.Status != nil || r.CreatedAt != nil {
				t.Errorf("%s Name, Region, Status, CreatedAt = %q, %q, %v, %v; want %q, %q, nil, nil", arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, want, testRegion)
			}
			if attrs := ggv2Attrs(t, r); attrs["Arn"] != arn || attrs["ComponentVersion"] != v {
				t.Errorf("%s attrs = %s; want its element", arn, r.AttributesJSON)
			}
			if v == "1.0.0" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeGreengrassV2ComponentVersion})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	if got := calls.ggv2SortedTagARNs(); !slices.Equal(got, ggv2AllTagARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, ggv2AllTagARNs())
	}
	wantVersionsOf := []string{ggv2TestARN("components", "comp-bare"), ggv2TestARN("components", "comp-tagged")}
	if got := slices.Sorted(slices.Values(calls.versionsOf)); !slices.Equal(got, wantVersionsOf) {
		t.Errorf("ListComponentVersions components = %v; want %v", got, wantVersionsOf)
	}
	if want := []ggv2types.ComponentVisibilityScope{"PRIVATE", "PRIVATE"}; !slices.Equal(calls.scopes, want) {
		t.Errorf("ListComponents Scope = %v; want %v", calls.scopes, want)
	}
	// No HistoryFilter, TargetArn or ParentTargetArn: each target's latest
	// deployment only, from every target.
	if len(calls.deployIns) == 0 {
		t.Error("ListDeployments not called")
	}
	for _, in := range calls.deployIns {
		in.MaxResults, in.NextToken = nil, nil
		if in != (greengrassv2.ListDeploymentsInput{}) {
			t.Errorf("ListDeployments input = %+v; want only MaxResults and NextToken set", in)
		}
	}
	for _, op := range []string{"ListComponents", "ListComponentVersions", "ListCoreDevices", "ListDeployments"} {
		if got, want := calls.maxResults[op], calls.ggv2Count(op); len(got) != want || want == 0 {
			t.Errorf("%s sent MaxResults on %d of %d calls", op, len(got), want)
		}
		for _, n := range calls.maxResults[op] {
			if n != 100 {
				t.Errorf("%s MaxResults = %d; want 100", op, n)
			}
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanGreengrassV2_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := ggv2Scan(context.Background(), st, ggv2Handlers(newGGV2Calls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := ggv2Scan(context.Background(), st, ggv2Handlers(newGGV2Calls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != ggv2TestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, ggv2TestTotal)
	}
}

// Empty listings: nothing stored, no version listing or tag read, no error,
// no warning.
func TestScanGreengrassV2_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newGGV2Calls()
	handlers := ggv2Handlers(calls)
	handlers["ListComponents"] = func(in any) (any, error) {
		calls.ggv2Record("ListComponents", in)
		return &greengrassv2.ListComponentsOutput{}, nil
	}
	handlers["ListCoreDevices"] = func(in any) (any, error) {
		calls.ggv2Record("ListCoreDevices", in)
		return &greengrassv2.ListCoreDevicesOutput{}, nil
	}
	handlers["ListDeployments"] = func(in any) (any, error) {
		calls.ggv2Record("ListDeployments", in)
		return &greengrassv2.ListDeploymentsOutput{}, nil
	}

	total, inserted, err := ggv2Scan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range ggv2Kinds() {
		if n := calls.ggv2Count(k.op); n != 1 {
			t.Errorf("%s called %d times; want 1", k.op, n)
		}
	}
	for _, op := range []string{"ListComponentVersions", "ListTagsForResource"} {
		if n := calls.ggv2Count(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently (a component with its
// versions, which are then not listed). A denial keeps the row untagged and
// warns once under the tag op with the scan's scope; a throttle keeps it and
// is transient; any other error keeps it and is an error.
func TestScanGreengrassV2_TagReadFailures(t *testing.T) {
	version := ggv2TestVersionARN("comp-tagged", "1.0.0")
	component := ggv2KindOf(t, "ListComponents").ggv2RowARN("bare")
	device := ggv2KindOf(t, "ListCoreDevices").ggv2RowARN("tagged")
	deployment := ggv2KindOf(t, "ListDeployments").ggv2RowARN("bare")
	for _, c := range []struct {
		name      string
		arn       string
		typ       string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"component version/not found", version, TypeGreengrassV2ComponentVersion, ggv2TestGone, -1, "", ggv2TestTotal - 1, false, false},
		{"component version/denied", version, TypeGreengrassV2ComponentVersion, ggv2TestDenied, -1, "", ggv2TestTotal, true, true},
		{"component version/transient", version, TypeGreengrassV2ComponentVersion, ggv2TestThrottle, outcomeTransient, "ThrottlingException", ggv2TestTotal, true, false},
		{"component version/other error", version, TypeGreengrassV2ComponentVersion, ggv2TestBad, outcomeError, "ValidationException", ggv2TestTotal, true, false},
		{"component/not found", component, TypeGreengrassV2Component, ggv2TestGone, -1, "", ggv2TestTotal - 1 - 2, false, false},
		{"core device/denied", device, TypeGreengrassV2CoreDevice, ggv2TestDenied, -1, "", ggv2TestTotal, true, true},
		{"core device/not found", device, TypeGreengrassV2CoreDevice, ggv2TestGone, -1, "", ggv2TestTotal - 1, false, false},
		{"deployment/other error", deployment, TypeGreengrassV2Deployment, ggv2TestBad, outcomeError, "ValidationException", ggv2TestTotal, true, false},
		{"deployment/transient", deployment, TypeGreengrassV2Deployment, ggv2TestThrottle, outcomeTransient, "ThrottlingException", ggv2TestTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newGGV2Calls()
			calls.errs[c.arn] = c.err

			total, _, err := ggv2Scan(context.Background(), st, ggv2Handlers(calls))
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
				want = []string{"greengrass-v2:ListTagsForResource"}
			}
			if got := ggv2WarnOps(w); !slices.Equal(got, want) {
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
func TestScanGreengrassV2_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newGGV2Calls()
	calls.errs[ggv2KindOf(t, "ListComponents").ggv2RowARN("bare")] = ggv2TestDenied
	calls.errs[ggv2TestVersionARN("comp-tagged", "2.0.0")] = ggv2TestDenied
	calls.errs[ggv2KindOf(t, "ListDeployments").ggv2RowARN("tagged")] = ggv2TestDenied

	total, _, err := ggv2Scan(context.Background(), st, ggv2Handlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != ggv2TestTotal {
		t.Errorf("total = %d; want %d", total, ggv2TestTotal)
	}
	if got, want := ggv2WarnOps(warns()), []string{"greengrass-v2:ListTagsForResource"}; !slices.Equal(got, want) {
		t.Errorf("warnings = %v; want %v", got, want)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanGreengrassV2_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "greengrass-v2:ListTagsForResource")
	calls := newGGV2Calls()
	calls.errs[ggv2KindOf(t, "ListCoreDevices").ggv2RowARN("bare")] = ggv2TestThrottle
	calls.errs[ggv2TestVersionARN("comp-bare", "2.0.0")] = ggv2TestBad

	_, _, err := ggv2Scan(context.Background(), st, ggv2Handlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("ListTagsForResource warnings = %d; want 1", *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored.
func TestScanGreengrassV2_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeGreengrassV2CoreDevice)
	calls := newGGV2Calls()
	calls.errs[ggv2KindOf(t, "ListCoreDevices").ggv2RowARN("tagged")] = ggv2TestDenied

	total, _, err := ggv2Scan(context.Background(), st, ggv2Handlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.ggv2Count("ListDeployments"); n != 0 {
		t.Errorf("ListDeployments called %d times; want 0 after the failed write", n)
	}
	if n := calls.ggv2Count("ListCoreDevices"); n != 1 {
		t.Errorf("ListCoreDevices called %d times; want 1 (no page after the failed write)", n)
	}
	if total != 2+4 {
		t.Errorf("total = %d; want the 2 components and 4 versions", total)
	}
	if n := len(rowsByNativeID(t, st, TypeGreengrassV2ComponentVersion)); n != 4 {
		t.Errorf("component versions = %d; want 4", n)
	}
}

// A listing's failed later page keeps the page read before it, and its error
// does not stop the later phases. A component not stored has no versions
// read.
func TestScanGreengrassV2_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		op                     string
		wantTotal, wantDeploys int
	}{
		{"ListComponents", ggv2TestTotal - 1 - 2, 2},
		{"ListCoreDevices", ggv2TestTotal - 1, 2},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newGGV2Calls()
			handlers := ggv2Handlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.ggv2Record(c.op, in)
					return nil, ggv2TestBad
				}
				return list(in)
			}

			total, _, err := ggv2Scan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			k := ggv2KindOf(t, c.op)
			rows := rowsByNativeID(t, st, k.typ)
			if _, ok := rows[k.ggv2RowARN("tagged")]; !ok {
				t.Errorf("%s, read before the failure, missing", k.ggv2RowARN("tagged"))
			}
			if _, ok := rows[k.ggv2RowARN("bare")]; ok {
				t.Errorf("%s stored; its page failed", k.ggv2RowARN("bare"))
			}
			if got := len(rowsByNativeID(t, st, TypeGreengrassV2Deployment)); got != c.wantDeploys {
				t.Errorf("deployments (the last phase) = %d; want %d", got, c.wantDeploys)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanGreengrassV2_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	calls := newGGV2Calls()
	handlers := ggv2Handlers(calls)
	twoPages := handlers["ListDeployments"]
	handlers["ListDeployments"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*greengrassv2.ListDeploymentsOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, _, err := ggv2Scan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != ggv2TestTotal {
		t.Errorf("total = %d; want %d", total, ggv2TestTotal)
	}
	if n := len(rowsByNativeID(t, st, TypeGreengrassV2Deployment)); n != 2 {
		t.Errorf("deployments = %d; want 2", n)
	}
}

// One component's failed version listing costs the other component none of
// its rows, nor the later phases theirs: a component deleted since it was
// listed is skipped silently, or warned about when part of its listing was
// read; a denial warns once under the op; any other error is returned.
func TestScanGreengrassV2_PerComponentVersionFailures(t *testing.T) {
	const versionsOp = "greengrass-v2:ListComponentVersions"
	for _, c := range []struct {
		name       string
		laterPage  bool
		err        error
		outcome    serviceOutcome // -1: no error
		wantTagged int
		warn       bool
	}{
		{"not found", false, ggv2TestGone, -1, 0, false},
		{"not found mid-listing", true, ggv2TestGone, -1, 1, true},
		{"denied", false, ggv2TestDenied, -1, 0, true},
		{"other error", false, ggv2TestBad, outcomeError, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newGGV2Calls()
			handlers := ggv2Handlers(calls)
			handlers["ListComponentVersions"] = func(in any) (any, error) {
				req := in.(*greengrassv2.ListComponentVersionsInput)
				if sv(req.Arn) == ggv2TestARN("components", "comp-tagged") && (req.NextToken != nil) == c.laterPage {
					calls.ggv2Record("ListComponentVersions", in)
					return nil, c.err
				}
				return calls.ggv2Versions(in)
			}

			total, _, err := ggv2Scan(context.Background(), st, handlers)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, "ValidationException")
			}
			if want := ggv2TestTotal - 2 + c.wantTagged; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			rows := rowsByNativeID(t, st, TypeGreengrassV2ComponentVersion)
			for _, arn := range []string{ggv2TestVersionARN("comp-bare", "1.0.0"), ggv2TestVersionARN("comp-bare", "2.0.0")} {
				if _, ok := rows[arn]; !ok {
					t.Errorf("%s of the other component missing", arn)
				}
			}
			if _, ok := rows[ggv2TestVersionARN("comp-tagged", "1.0.0")]; ok != (c.wantTagged == 1) {
				t.Errorf("comp-tagged version 1.0.0 stored = %v; want %v", ok, c.wantTagged == 1)
			}
			if n := len(rowsByNativeID(t, st, TypeGreengrassV2Deployment)); n != 2 {
				t.Errorf("deployments (the last phase) = %d; want 2", n)
			}
			var want []string
			if c.warn {
				want = []string{versionsOp}
			}
			if got := ggv2WarnOps(warns()); !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

// Listed elements without the id a row is stored under (a component or a
// component version without an Arn, a core device without a thing name, a
// deployment without an id) are not stored, not sent to the tag op, and
// warned about once under their listing, naming the element by what else it
// carries, or by noIdentifier when it carries nothing; a component without
// an Arn has no versions listed.
func TestScanGreengrassV2_ElementsWithoutIDs(t *testing.T) {
	for _, c := range []struct {
		name, op, key string
		extra         func(out any)
	}{
		{"ListComponents", "ListComponents", "comp-noarn", func(out any) {
			o := out.(*greengrassv2.ListComponentsOutput)
			o.Components = append(o.Components, ggv2types.Component{ComponentName: sdkaws.String("comp-noarn")})
		}},
		{"ListComponentVersions/named", "ListComponentVersions", "comp-x:9.0.0", func(out any) {
			o := out.(*greengrassv2.ListComponentVersionsOutput)
			o.ComponentVersions = append(o.ComponentVersions, ggv2types.ComponentVersionListItem{ComponentName: sdkaws.String("comp-x"), ComponentVersion: sdkaws.String("9.0.0")})
		}},
		{"ListComponentVersions/nameless", "ListComponentVersions", noIdentifier, func(out any) {
			o := out.(*greengrassv2.ListComponentVersionsOutput)
			o.ComponentVersions = append(o.ComponentVersions, ggv2types.ComponentVersionListItem{})
		}},
		{"ListCoreDevices", "ListCoreDevices", noIdentifier, func(out any) {
			o := out.(*greengrassv2.ListCoreDevicesOutput)
			o.CoreDevices = append(o.CoreDevices, ggv2types.CoreDevice{Status: ggv2types.CoreDeviceStatusHealthy})
		}},
		{"ListDeployments", "ListDeployments", "dep-noid", func(out any) {
			o := out.(*greengrassv2.ListDeploymentsOutput)
			o.Deployments = append(o.Deployments, ggv2types.Deployment{DeploymentName: sdkaws.String("dep-noid")})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newGGV2Calls()
			handlers := ggv2Handlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				out, err := list(in)
				c.extra(out)
				return out, err
			}

			total, _, err := ggv2Scan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != ggv2TestTotal {
				t.Errorf("total = %d; want %d", total, ggv2TestTotal)
			}
			if got := calls.ggv2SortedTagARNs(); !slices.Equal(got, ggv2AllTagARNs()) {
				t.Errorf("ListTagsForResource ARNs = %v; want only the fixture's", got)
			}
			if n := len(calls.versionsOf); n != 2 {
				t.Errorf("ListComponentVersions listed %d components; want the fixture's 2", n)
			}
			w := warns()
			if got, want := ggv2WarnOps(w), []string{"greengrass-v2:" + c.op}; !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", got, want)
			}
			// The key closes the message, so a suffix match pins it whole.
			if !strings.HasSuffix(w[0].Message, " "+c.key) {
				t.Errorf("warning %q; want it naming %q", w[0].Message, c.key)
			}
		})
	}
}

// A GovCloud or China component's ARN, and a core device's and a
// deployment's ARN built in the account's partition, reach the tag op
// unchanged and are the rows' NativeIDs.
func TestScanGreengrassV2_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newGGV2Calls()
			handlers := ggv2Handlers(calls)
			prefix := "arn:" + c.partition + ":greengrass:" + c.region + ":" + testAccountID + ":"
			componentARN := prefix + "components:comp-p"
			versionARN := componentARN + ":versions:1.0.0"
			deviceARN := prefix + "coreDevices:thing-p"
			deploymentARN := prefix + "deployments:dep-p"
			for _, arn := range []string{componentARN, versionARN, deviceARN, deploymentARN} {
				calls.tags[arn] = map[string]string{"arn": arn}
			}
			handlers["ListComponents"] = func(in any) (any, error) {
				calls.ggv2Record("ListComponents", in)
				return &greengrassv2.ListComponentsOutput{Components: []ggv2types.Component{{Arn: &componentARN, ComponentName: sdkaws.String("comp-p")}}}, nil
			}
			handlers["ListComponentVersions"] = func(in any) (any, error) {
				calls.ggv2Record("ListComponentVersions", in)
				return &greengrassv2.ListComponentVersionsOutput{ComponentVersions: []ggv2types.ComponentVersionListItem{
					{Arn: &versionARN, ComponentName: sdkaws.String("comp-p"), ComponentVersion: sdkaws.String("1.0.0")},
				}}, nil
			}
			handlers["ListCoreDevices"] = func(in any) (any, error) {
				calls.ggv2Record("ListCoreDevices", in)
				return &greengrassv2.ListCoreDevicesOutput{CoreDevices: []ggv2types.CoreDevice{{CoreDeviceThingName: sdkaws.String("thing-p")}}}, nil
			}
			handlers["ListDeployments"] = func(in any) (any, error) {
				calls.ggv2Record("ListDeployments", in)
				return &greengrassv2.ListDeploymentsOutput{Deployments: []ggv2types.Deployment{{DeploymentId: sdkaws.String("dep-p")}}}, nil
			}
			acct := testAccountIn(c.partition)
			acct.cfg = opStubConfig(handlers, ggv2TestDenied)
			if _, _, err := scanGreengrassV2(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			want := slices.Sorted(slices.Values([]string{componentARN, versionARN, deviceARN, deploymentARN}))
			if got := calls.ggv2SortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
			}
			for typ, arn := range map[string]string{
				TypeGreengrassV2Component: componentARN, TypeGreengrassV2ComponentVersion: versionARN,
				TypeGreengrassV2CoreDevice: deviceARN, TypeGreengrassV2Deployment: deploymentARN,
			} {
				assertTagsMap(t, rowsByNativeID(t, st, typ)[arn].TagsJSON, map[string]string{"arn": arn})
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanGreengrassV2_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newGGV2Calls()
	handlers := ggv2Handlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListComponents"]
	handlers["ListComponents"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := ggv2Scan(ctx, st, handlers)
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
	for _, op := range []string{"ListComponentVersions", "ListCoreDevices", "ListDeployments"} {
		if n := calls.ggv2Count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and no later phase runs.
func TestScanGreengrassV2_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "greengrass.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := ggv2Handlers(newGGV2Calls())
		handlers["ListDeployments"] = func(any) (any, error) { return nil, nx }
		total, _, err := ggv2Scan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := ggv2TestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newGGV2Calls()
		calls.errs[ggv2KindOf(t, "ListComponents").ggv2RowARN("tagged")] = nx
		_, _, err := ggv2Scan(context.Background(), st, ggv2Handlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newGGV2Calls()
		handlers := map[string]func(in any) (any, error){}
		for _, op := range []string{"ListComponents", "ListCoreDevices"} {
			handlers[op] = func(in any) (any, error) {
				calls.ggv2Record(op, in)
				return nil, nx
			}
		}
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(handlers, nx)
		_, _, err := scanGreengrassV2(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.ggv2Count("ListCoreDevices"); n != 0 {
			t.Errorf("ListCoreDevices called %d times; want 0 once the region proved unavailable", n)
		}
	})
}
