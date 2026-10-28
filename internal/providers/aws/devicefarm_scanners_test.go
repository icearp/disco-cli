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
	"github.com/aws/aws-sdk-go-v2/service/devicefarm"
	dftypes "github.com/aws/aws-sdk-go-v2/service/devicefarm/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

const testDFRegion = "us-west-2"

func testDFProjectARN(guid string) string {
	return "arn:aws:devicefarm:" + testDFRegion + ":" + testAccountID + ":project:" + guid
}

func testDFUploadARN(projectGUID, uploadGUID string) string {
	return "arn:aws:devicefarm:" + testDFRegion + ":" + testAccountID + ":upload:" + projectGUID + "/" + uploadGUID
}

func testDFUpload(projectGUID, uploadGUID string) dftypes.Upload {
	created := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	return dftypes.Upload{
		Arn:      sdkaws.String(testDFUploadARN(projectGUID, uploadGUID)),
		Name:     sdkaws.String(uploadGUID + ".apk"),
		Type:     dftypes.UploadTypeAndroidApp,
		Status:   dftypes.UploadStatusSucceeded,
		Category: dftypes.UploadCategoryPrivate,
		Created:  &created,
		Url:      sdkaws.String("https://prod-us-west-2-uploads.s3-us-west-2.amazonaws.com/x?X-Amz-Signature=deadbeef"),
	}
}

// fakeDeviceFarmUploads serves ListUploads pages keyed by project ARN then
// NextToken ("" for the first page), and records every project queried.
type fakeDeviceFarmUploads struct {
	deviceFarmAPI
	pages   map[string]map[string]*devicefarm.ListUploadsOutput
	errs    map[string]error
	queried []string
}

func (f *fakeDeviceFarmUploads) ListUploads(_ context.Context, in *devicefarm.ListUploadsInput, _ ...func(*devicefarm.Options)) (*devicefarm.ListUploadsOutput, error) {
	arn := sdkaws.ToString(in.Arn)
	f.queried = append(f.queried, arn)
	if err := f.errs[arn]; err != nil {
		return nil, err
	}
	if out := f.pages[arn][sdkaws.ToString(in.NextToken)]; out != nil {
		return out, nil
	}
	return &devicefarm.ListUploadsOutput{}, nil
}

func deviceFarmUploadRows(t *testing.T, st *store.Store) map[string]store.Resource {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{Types: []string{TypeDeviceFarmUpload}, Limit: util.AllResources})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	byNativeID := make(map[string]store.Resource, len(rows))
	for _, r := range rows {
		byNativeID[r.NativeID] = r
	}
	return byNativeID
}

func TestScanDeviceFarmUploads_PaginatesEveryProject(t *testing.T) {
	st := newTestStore(t)
	projA, projB := testDFProjectARN("PA"), testDFProjectARN("PB")
	fake := &fakeDeviceFarmUploads{pages: map[string]map[string]*devicefarm.ListUploadsOutput{
		projA: {
			"":   {Uploads: []dftypes.Upload{testDFUpload("PA", "U1")}, NextToken: sdkaws.String("p2")},
			"p2": {Uploads: []dftypes.Upload{testDFUpload("PA", "U2"), {Name: sdkaws.String("no-arn")}}},
		},
		projB: {"": {Uploads: []dftypes.Upload{testDFUpload("PB", "U3")}}},
	}}

	total, _, err := scanDeviceFarmUploads(context.Background(), fake, newTestAccount(testAccountID), testDFRegion, st, testScanID, []string{projA, projB})
	if err != nil {
		t.Fatalf("scanDeviceFarmUploads: %v", err)
	}
	if want := []string{projA, projA, projB}; !slices.Equal(fake.queried, want) {
		t.Errorf("queried projects = %v; want %v (two pages of PA, one of PB)", fake.queried, want)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3 (both pages of PA plus PB; ARN-less upload skipped)", total)
	}
	rows := deviceFarmUploadRows(t, st)
	if len(rows) != 3 {
		t.Errorf("stored %d upload rows; want 3", len(rows))
	}
	for _, arn := range []string{testDFUploadARN("PA", "U1"), testDFUploadARN("PA", "U2"), testDFUploadARN("PB", "U3")} {
		if _, ok := rows[arn]; !ok {
			t.Errorf("upload %s not stored", arn)
		}
	}
	r := rows[testDFUploadARN("PA", "U1")]
	if r.Type != TypeDeviceFarmUpload {
		t.Errorf("Type = %q; want %q", r.Type, TypeDeviceFarmUpload)
	}
	if got := sdkaws.ToString(r.Region); got != testDFRegion {
		t.Errorf("Region = %q; want %q", got, testDFRegion)
	}
	if got := sdkaws.ToString(r.Name); got != "U1.apk" {
		t.Errorf("Name = %q; want U1.apk", got)
	}
	if got := sdkaws.ToString(r.Status); got != string(dftypes.UploadStatusSucceeded) {
		t.Errorf("Status = %q; want %q", got, dftypes.UploadStatusSucceeded)
	}
	if got := sdkaws.ToString(r.CreatedAt); got != "2026-09-01T12:00:00Z" {
		t.Errorf("CreatedAt = %q; want 2026-09-01T12:00:00Z", got)
	}
	var attrs dftypes.Upload
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("unmarshal attrs: %v", err)
	}
	if attrs.Type != dftypes.UploadTypeAndroidApp {
		t.Errorf("attrs Type = %q; want %q", attrs.Type, dftypes.UploadTypeAndroidApp)
	}
}

func TestScanDeviceFarmUploads_Empty(t *testing.T) {
	st := newTestStore(t)
	fake := &fakeDeviceFarmUploads{}

	total, _, err := scanDeviceFarmUploads(context.Background(), fake, newTestAccount(testAccountID), testDFRegion, st, testScanID, []string{testDFProjectARN("PA")})
	if err != nil {
		t.Fatalf("scanDeviceFarmUploads: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d; want 0", total)
	}
	if n := len(deviceFarmUploadRows(t, st)); n != 0 {
		t.Errorf("stored %d upload rows; want 0", n)
	}
}

// A project deleted between ListProjects and ListUploads is skipped without a
// warning; its siblings are still listed.
func TestScanDeviceFarmUploads_VanishedProjectSkipped(t *testing.T) {
	st := newTestStore(t)
	warned := false
	st.OnWarn = func(store.ScanWarning) { warned = true }
	gone, live := testDFProjectARN("GONE"), testDFProjectARN("LIVE")
	fake := &fakeDeviceFarmUploads{
		errs:  map[string]error{gone: apiErr("NotFoundException", "project not found")},
		pages: map[string]map[string]*devicefarm.ListUploadsOutput{live: {"": {Uploads: []dftypes.Upload{testDFUpload("LIVE", "U1")}}}},
	}

	total, _, err := scanDeviceFarmUploads(context.Background(), fake, newTestAccount(testAccountID), testDFRegion, st, testScanID, []string{gone, live})
	if err != nil {
		t.Fatalf("a vanished project must not fail the phase, got %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1 from the live project", total)
	}
	if _, ok := deviceFarmUploadRows(t, st)[testDFUploadARN("LIVE", "U1")]; !ok {
		t.Error("live project's upload not stored")
	}
	if warned {
		t.Error("a vanished project must not record a scan warning")
	}
}

// ListUploads can be denied per project, so a denial warns once and the
// remaining projects are still listed.
func TestScanDeviceFarmUploads_AccessDeniedWarnsOnceAndContinues(t *testing.T) {
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	deny := apiErr("AccessDeniedException", "User: arn:aws:sts::123456789012:assumed-role/x is not authorized to perform: devicefarm:ListUploads")
	d1, d2, permitted := testDFProjectARN("D1"), testDFProjectARN("D2"), testDFProjectARN("OK")
	fake := &fakeDeviceFarmUploads{
		errs:  map[string]error{d1: deny, d2: deny},
		pages: map[string]map[string]*devicefarm.ListUploadsOutput{permitted: {"": {Uploads: []dftypes.Upload{testDFUpload("OK", "U1")}}}},
	}

	total, _, err := scanDeviceFarmUploads(context.Background(), fake, newTestAccount(testAccountID), testDFRegion, st, testScanID, []string{d1, d2, permitted})
	if err != nil {
		t.Fatalf("access denied must not fail the phase, got %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1 from the permitted project", total)
	}
	if len(warnings) != 1 {
		t.Errorf("recorded %d warnings; want exactly 1", len(warnings))
	}
}

func TestScanDeviceFarmUploads_UnexpectedErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	boom := apiErr("ServiceAccountException", "boom")
	proj := testDFProjectARN("PA")
	fake := &fakeDeviceFarmUploads{errs: map[string]error{proj: boom}}

	_, _, err := scanDeviceFarmUploads(context.Background(), fake, newTestAccount(testAccountID), testDFRegion, st, testScanID, []string{proj})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v; want it to wrap %v", err, boom)
	}
}

// scanDeviceFarm hands the projects it listed to the uploads phase, and the
// stored upload carries the redacted presigned URL rather than its signature.
func TestScanDeviceFarm_ListsUploadsOfListedProjects(t *testing.T) {
	st := newTestStore(t)
	proj := testDFProjectARN("PA")
	stub := stubResponses(t, map[string][]stubCall{
		"ListProjects":           {{Output: &devicefarm.ListProjectsOutput{Projects: []dftypes.Project{{Arn: sdkaws.String(proj), Name: sdkaws.String("p")}}}}},
		"ListDevicePools":        {{Output: &devicefarm.ListDevicePoolsOutput{}}},
		"ListNetworkProfiles":    {{Output: &devicefarm.ListNetworkProfilesOutput{}}},
		"ListInstanceProfiles":   {{Output: &devicefarm.ListInstanceProfilesOutput{}}},
		"ListDeviceInstances":    {{Output: &devicefarm.ListDeviceInstancesOutput{}}},
		"ListVPCEConfigurations": {{Output: &devicefarm.ListVPCEConfigurationsOutput{}}},
		"ListTestGridProjects":   {{Output: &devicefarm.ListTestGridProjectsOutput{}}},
		"ListUploads":            {{Output: &devicefarm.ListUploadsOutput{Uploads: []dftypes.Upload{testDFUpload("PA", "U1")}}}},
		"ListTagsForResource":    {{Output: &devicefarm.ListTagsForResourceOutput{}}},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cfgWithStub(stub, testDFRegion)}

	total, _, err := scanDeviceFarm(context.Background(), acct, "", st, testScanID)
	if err != nil {
		t.Fatalf("scanDeviceFarm: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (project + upload)", total)
	}
	r, ok := deviceFarmUploadRows(t, st)[testDFUploadARN("PA", "U1")]
	if !ok {
		t.Fatal("upload row not stored by scanDeviceFarm")
	}
	var attrs dftypes.Upload
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("unmarshal attrs: %v", err)
	}
	if got := sdkaws.ToString(attrs.Url); got != redact.Placeholder {
		t.Errorf("stored Url = %q; want %q", got, redact.Placeholder)
	}
}

// Category CURATED marks an upload AWS manages; PRIVATE is the customer's own.
func TestScanDeviceFarmUploads_CuratedIsManagedByProvider(t *testing.T) {
	st := newTestStore(t)
	proj := testDFProjectARN("PA")
	curated := testDFUpload("PA", "CUR")
	curated.Category = dftypes.UploadCategoryCurated
	fake := &fakeDeviceFarmUploads{pages: map[string]map[string]*devicefarm.ListUploadsOutput{
		proj: {"": {Uploads: []dftypes.Upload{curated, testDFUpload("PA", "PRIV")}}},
	}}

	if _, _, err := scanDeviceFarmUploads(context.Background(), fake, newTestAccount(testAccountID), testDFRegion, st, testScanID, []string{proj}); err != nil {
		t.Fatalf("scanDeviceFarmUploads: %v", err)
	}
	rows, err := st.ListResources(store.ResourceFilter{Types: []string{TypeDeviceFarmUpload}, IncludeManaged: true, Limit: util.AllResources})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	managed := make(map[string]bool, len(rows))
	for _, r := range rows {
		managed[r.NativeID] = r.ManagedByProvider
	}
	for arn, want := range map[string]bool{testDFUploadARN("PA", "CUR"): true, testDFUploadARN("PA", "PRIV"): false} {
		got, ok := managed[arn]
		if !ok {
			t.Errorf("upload %s not stored", arn)
			continue
		}
		if got != want {
			t.Errorf("upload %s ManagedByProvider = %v; want %v", arn, got, want)
		}
	}
}

// The same upload ARN listed under two projects is stored and counted once.
func TestScanDeviceFarmUploads_DuplicateARNAcrossProjectsStoredOnce(t *testing.T) {
	st := newTestStore(t)
	projA, projB := testDFProjectARN("PA"), testDFProjectARN("PB")
	shared := testDFUpload("PA", "SHARED")
	fake := &fakeDeviceFarmUploads{pages: map[string]map[string]*devicefarm.ListUploadsOutput{
		projA: {"": {Uploads: []dftypes.Upload{shared}}},
		projB: {"": {Uploads: []dftypes.Upload{shared}}},
	}}

	total, _, err := scanDeviceFarmUploads(context.Background(), fake, newTestAccount(testAccountID), testDFRegion, st, testScanID, []string{projA, projB})
	if err != nil {
		t.Fatalf("scanDeviceFarmUploads: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1", total)
	}
	if n := len(deviceFarmUploadRows(t, st)); n != 1 {
		t.Errorf("stored %d upload rows; want 1", n)
	}
}

var (
	dfTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: devicefarm:List")
	dfTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	dfTestBad      = apiErr("ArgumentException", "bad")
	dfTestGone     = apiErr("NotFoundException", "gone")
	dfTestProjects = []string{"project-bare", "project-tagged"}
)

// dfTestTotal is every row of the fixture: two projects; two device pools
// and two network profiles (one per page) in each project, plus one curated
// pool and one curated network profile in project-tagged; and two instance
// profiles, device instances, VPC endpoint configurations and test-grid
// projects.
const dfTestTotal = 2 + 2*(2*2+1) + 4*2

// dfEl is one listed element of the fixture.
type dfEl struct {
	arn, name string
	curated   bool
}

// dfKind is one listing whose rows have their tags read: the List op, the
// type it stores, the resource segment of its ARNs, the short name its rows
// are named by, whether it is listed per project, whether a curated element
// joins project-tagged's second page, el, which builds the listed element of
// e, and page, which builds the op's output holding the elements els and
// next.
type dfKind struct {
	op, typ, seg, short string
	perProject, curated bool
	el                  func(e dfEl) any
	page                func(els []any, next *string) any
}

func dfKinds() []dfKind {
	return []dfKind{
		{
			"ListProjects", TypeDeviceFarmProject, "project", "project", false, false,
			func(e dfEl) any { return dftypes.Project{Arn: sdkaws.String(e.arn), Name: sdkaws.String(e.name)} },
			func(els []any, next *string) any {
				out := &devicefarm.ListProjectsOutput{NextToken: next}
				for _, e := range els {
					out.Projects = append(out.Projects, e.(dftypes.Project))
				}
				return out
			},
		},
		{
			"ListDevicePools", TypeDeviceFarmDevicePool, "devicepool", "pool", true, true,
			func(e dfEl) any {
				typ := dftypes.DevicePoolTypePrivate
				if e.curated {
					typ = dftypes.DevicePoolTypeCurated
				}
				return dftypes.DevicePool{Arn: sdkaws.String(e.arn), Name: sdkaws.String(e.name), Type: typ}
			},
			func(els []any, next *string) any {
				out := &devicefarm.ListDevicePoolsOutput{NextToken: next}
				for _, e := range els {
					out.DevicePools = append(out.DevicePools, e.(dftypes.DevicePool))
				}
				return out
			},
		},
		{
			"ListNetworkProfiles", TypeDeviceFarmNetworkProfile, "networkprofile", "np", true, true,
			func(e dfEl) any {
				typ := dftypes.NetworkProfileTypePrivate
				if e.curated {
					typ = dftypes.NetworkProfileTypeCurated
				}
				return dftypes.NetworkProfile{Arn: sdkaws.String(e.arn), Name: sdkaws.String(e.name), Type: typ}
			},
			func(els []any, next *string) any {
				out := &devicefarm.ListNetworkProfilesOutput{NextToken: next}
				for _, e := range els {
					out.NetworkProfiles = append(out.NetworkProfiles, e.(dftypes.NetworkProfile))
				}
				return out
			},
		},
		{
			"ListInstanceProfiles", TypeDeviceFarmInstanceProfile, "instanceprofile", "ip", false, false,
			func(e dfEl) any {
				return dftypes.InstanceProfile{Arn: sdkaws.String(e.arn), Name: sdkaws.String(e.name)}
			},
			func(els []any, next *string) any {
				out := &devicefarm.ListInstanceProfilesOutput{NextToken: next}
				for _, e := range els {
					out.InstanceProfiles = append(out.InstanceProfiles, e.(dftypes.InstanceProfile))
				}
				return out
			},
		},
		{
			"ListDeviceInstances", TypeDeviceFarmDeviceInstance, "deviceinstance", "di", false, false,
			func(e dfEl) any {
				return dftypes.DeviceInstance{Arn: sdkaws.String(e.arn), Udid: sdkaws.String(e.name), Status: dftypes.InstanceStatusAvailable}
			},
			func(els []any, next *string) any {
				out := &devicefarm.ListDeviceInstancesOutput{NextToken: next}
				for _, e := range els {
					out.DeviceInstances = append(out.DeviceInstances, e.(dftypes.DeviceInstance))
				}
				return out
			},
		},
		{
			"ListVPCEConfigurations", TypeDeviceFarmVPCEConfiguration, "vpceconfiguration", "vpce", false, false,
			func(e dfEl) any {
				return dftypes.VPCEConfiguration{Arn: sdkaws.String(e.arn), VpceConfigurationName: sdkaws.String(e.name)}
			},
			func(els []any, next *string) any {
				out := &devicefarm.ListVPCEConfigurationsOutput{NextToken: next}
				for _, e := range els {
					out.VpceConfigurations = append(out.VpceConfigurations, e.(dftypes.VPCEConfiguration))
				}
				return out
			},
		},
		{
			"ListTestGridProjects", TypeDeviceFarmTestGridProject, "testgrid-project", "tg", false, false,
			func(e dfEl) any {
				return dftypes.TestGridProject{Arn: sdkaws.String(e.arn), Name: sdkaws.String(e.name)}
			},
			func(els []any, next *string) any {
				out := &devicefarm.ListTestGridProjectsOutput{NextToken: next}
				for _, e := range els {
					out.TestGridProjects = append(out.TestGridProjects, e.(dftypes.TestGridProject))
				}
				return out
			},
		},
	}
}

// pageOf is k's output holding the listed elements of els, and next.
func (k dfKind) pageOf(els []dfEl, next *string) any {
	listed := make([]any, len(els))
	for i, e := range els {
		listed[i] = k.el(e)
	}
	return k.page(listed, next)
}

func dfKindOf(t *testing.T, op string) dfKind {
	t.Helper()
	for _, k := range dfKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no Device Farm tagged listing", op)
	return dfKind{}
}

func (k dfKind) name(suffix string) string { return k.short + "-" + suffix }

// arn is the ARN of k's row on page suffix of project's listing (project ""
// for an account-wide listing). A device instance's ARN carries no account,
// as the Service Reference's deviceinstance ARN format has none.
func (k dfKind) arn(project, suffix string) string {
	acct := testAccountID
	if k.typ == TypeDeviceFarmDeviceInstance {
		acct = ""
	}
	id := k.name(suffix)
	if project != "" {
		id = project + "/" + id
	}
	return "arn:aws:devicefarm:" + testDFRegion + ":" + acct + ":" + k.seg + ":" + id
}

// curatedARN is the ARN of k's curated element, which names no account.
func (k dfKind) curatedARN() string {
	return "arn:aws:devicefarm:" + testDFRegion + "::" + k.seg + ":curated"
}

// ownARNs is the ARN of every row of k the fixture lists, curated ones
// excepted.
func (k dfKind) ownARNs() []string {
	if !k.perProject {
		return []string{k.arn("", "tagged"), k.arn("", "bare")}
	}
	var arns []string
	for _, p := range dfTestProjects {
		arns = append(arns, k.arn(p, "tagged"), k.arn(p, "bare"))
	}
	return arns
}

// dfCalls records the calls of a scan and answers the tag reads, by ARN,
// with tags or errs.
type dfCalls struct {
	mu      sync.Mutex
	inputs  map[string][]any
	tagARNs []string
	tags    map[string]map[string]string
	errs    map[string]error
}

// newDFCalls tags every "-tagged" row with its type.
func newDFCalls() *dfCalls {
	c := &dfCalls{inputs: map[string][]any{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range dfKinds() {
		for _, arn := range k.ownARNs() {
			if strings.HasSuffix(arn, "-tagged") {
				c.tags[arn] = map[string]string{"type": k.typ}
			}
		}
	}
	return c
}

func (c *dfCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inputs[op] = append(c.inputs[op], in)
}

func (c *dfCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.inputs[op])
}

// listedProjects is the project ARNs op's first pages were sent, sorted.
func (c *dfCalls) listedProjects(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var arns []string
	for _, in := range c.inputs[op] {
		if bacField[*string](in, "NextToken") == nil {
			arns = append(arns, sv(bacField[*string](in, "Arn")))
		}
	}
	slices.Sort(arns)
	return arns
}

func (c *dfCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *dfCalls) listTags(in any) (any, error) {
	arn := sv(in.(*devicefarm.ListTagsForResourceInput).ResourceARN)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	out := &devicefarm.ListTagsForResourceOutput{}
	for k, v := range c.tags[arn] {
		out.Tags = append(out.Tags, dftypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return out, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second, under the project its input names; project-tagged's second page
// also lists k's curated element.
func (c *dfCalls) twoPages(k dfKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		project := ""
		if k.perProject {
			arn := sv(bacField[*string](in, "Arn"))
			project = arn[strings.LastIndex(arn, ":")+1:]
		}
		els := []dfEl{{arn: k.arn(project, suffix), name: k.name(suffix)}}
		if k.curated && project == "project-tagged" && next == nil {
			els = append(els, dfEl{arn: k.curatedARN(), name: "curated", curated: true})
		}
		return k.pageOf(els, next), nil
	}
}

// dfHandlers answers every op the scan calls: each tagged listing in two
// pages, the tag reads from c, and an empty upload listing per project.
func dfHandlers(c *dfCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListUploads": func(in any) (any, error) {
			c.record("ListUploads", in)
			return &devicefarm.ListUploadsOutput{}, nil
		},
	}
	for _, k := range dfKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// dfScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func dfScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, dfTestDenied)
	return scanDeviceFarm(ctx, acct, "", st, testScanID)
}

// dfOwnARNs is the ARN of every row of the fixture but the curated ones,
// sorted.
func dfOwnARNs() []string {
	var arns []string
	for _, k := range dfKinds() {
		arns = append(arns, k.ownARNs()...)
	}
	slices.Sort(arns)
	return arns
}

// Every listing stores both its pages, for every project, each row under its
// ARN, in Device Farm's home region, under the account's name, named by its
// name (a device instance by its ARN), its attributes the listed element,
// with no CreatedAt and no Status but a device instance's, not
// ManagedByProvider, and with the tags its tag read returns (nil for none).
// The curated pool and network profile are ManagedByProvider and stored
// untagged. ListTagsForResource is sent exactly the ARNs of every row but
// those two.
// ListTestGridProjects is sent its model's cap of 1000, the other listings
// no page size. Pools, network profiles and uploads are listed per project.
// No error, no warning.
func TestScanDeviceFarm_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newDFCalls()

	total, inserted, err := dfScan(context.Background(), st, dfHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != dfTestTotal || inserted != dfTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, dfTestTotal, dfTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	wantAccount := newTestAccount(testAccountID).Name
	for _, k := range dfKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		want := len(k.ownARNs())
		if k.curated {
			want++
		}
		if len(rows) != want {
			t.Errorf("%s rows = %d; want %d", k.typ, len(rows), want)
		}
		for _, arn := range k.ownARNs() {
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			elName := arn[strings.LastIndex(arn, "/")+1:]
			if !k.perProject {
				elName = arn[strings.LastIndex(arn, ":")+1:]
			}
			wantName, wantStatus := elName, ""
			if k.typ == TypeDeviceFarmDeviceInstance {
				wantName, wantStatus = arn, string(dftypes.InstanceStatusAvailable)
			}
			if sv(r.Name) != wantName || sv(r.Region) != testDFRegion || sv(r.Status) != wantStatus || r.CreatedAt != nil {
				t.Errorf("%s Name, Region, Status, CreatedAt = %q, %q, %q, %v; want %q, %q, %q, nil", arn, sv(r.Name), sv(r.Region), sv(r.Status), r.CreatedAt, wantName, testDFRegion, wantStatus)
			}
			if wantStatus == "" && r.Status != nil {
				t.Errorf("%s Status = %q; want nil", arn, *r.Status)
			}
			if sv(r.AccountName) != wantAccount || r.ManagedByProvider {
				t.Errorf("%s AccountName, ManagedByProvider = %q, %v; want %q, false", arn, sv(r.AccountName), r.ManagedByProvider, wantAccount)
			}
			assertAttrsOf(t, r.AttributesJSON, k.el(dfEl{arn: arn, name: elName}))
			if strings.HasSuffix(arn, "-tagged") {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		if k.curated {
			r, ok := rows[k.curatedARN()]
			if !ok || sv(r.Name) != "curated" || sv(r.AccountName) != wantAccount || r.TagsJSON != nil || !r.ManagedByProvider {
				t.Errorf("curated %s stored %v, Name %q, AccountName %q, TagsJSON %v, ManagedByProvider %v; want stored, named curated, %q, untagged, true", k.typ, ok, sv(r.Name), sv(r.AccountName), r.TagsJSON, r.ManagedByProvider, wantAccount)
			}
			assertAttrsOf(t, r.AttributesJSON, k.el(dfEl{arn: k.curatedARN(), name: "curated", curated: true}))
		}
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, dfOwnARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, dfOwnARNs())
	}
	wantProjects := []string{testDFProjectARN("project-bare"), testDFProjectARN("project-tagged")}
	for _, op := range []string{"ListDevicePools", "ListNetworkProfiles", "ListUploads"} {
		if got := calls.listedProjects(op); !slices.Equal(got, wantProjects) {
			t.Errorf("%s projects = %v; want %v", op, got, wantProjects)
		}
	}
	for _, k := range dfKinds() {
		if calls.count(k.op) == 0 {
			t.Errorf("%s not called", k.op)
		}
		for _, in := range calls.inputs[k.op] {
			got := bacField[*int32](in, "MaxResults")
			if k.op == "ListTestGridProjects" {
				got = bacField[*int32](in, "MaxResult")
				if got == nil || *got != 1000 {
					t.Errorf("%s MaxResult = %v; want 1000", k.op, got)
				}
			} else if got != nil {
				t.Errorf("%s MaxResults = %d; want none", k.op, *got)
			}
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanDeviceFarm_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := dfScan(context.Background(), st, dfHandlers(newDFCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := dfScan(context.Background(), st, dfHandlers(newDFCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != dfTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, dfTestTotal)
	}
}

// Every listing empty: nothing stored, no per-project listing or tag read,
// no error, no warning.
func TestScanDeviceFarm_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newDFCalls()
	handlers := dfHandlers(calls)
	for _, k := range dfKinds() {
		handlers[k.op] = func(in any) (any, error) {
			calls.record(k.op, in)
			return k.pageOf(nil, nil), nil
		}
	}

	total, inserted, err := dfScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListDevicePools", "ListNetworkProfiles", "ListUploads", "ListTagsForResource"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
}

// A failed tag read. NotFoundException is modeled, so a resource deleted
// since it was listed is dropped silently (a project with its pools,
// network profiles and uploads, which are not listed). A denial keeps the
// row untagged and warns once under the tag op with the scan's scope; a
// throttle keeps it and is transient; any other error keeps it and is an
// error.
func TestScanDeviceFarm_TagReadFailures(t *testing.T) {
	pool := dfKindOf(t, "ListDevicePools").arn("project-bare", "tagged")
	instance := dfKindOf(t, "ListDeviceInstances").arn("", "tagged")
	project := testDFProjectARN("project-bare")
	// project-bare and its two pools and two network profiles.
	projectAndChildren := 1 + 2 + 2
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
		{"pool/not found", pool, TypeDeviceFarmDevicePool, dfTestGone, -1, "", dfTestTotal - 1, false, false},
		{"pool/denied", pool, TypeDeviceFarmDevicePool, dfTestDenied, -1, "", dfTestTotal, true, true},
		{"pool/transient", pool, TypeDeviceFarmDevicePool, dfTestThrottle, outcomeTransient, "ThrottlingException", dfTestTotal, true, false},
		{"pool/other error", pool, TypeDeviceFarmDevicePool, dfTestBad, outcomeError, "ArgumentException", dfTestTotal, true, false},
		{"instance/not found", instance, TypeDeviceFarmDeviceInstance, dfTestGone, -1, "", dfTestTotal - 1, false, false},
		{"instance/denied", instance, TypeDeviceFarmDeviceInstance, dfTestDenied, -1, "", dfTestTotal, true, true},
		{"project/not found", project, TypeDeviceFarmProject, dfTestGone, -1, "", dfTestTotal - projectAndChildren, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newDFCalls()
			calls.errs[c.arn] = c.err

			total, _, err := dfScan(context.Background(), st, dfHandlers(calls))
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
				want = []string{dfTagsOp}
			}
			if got := amWarnings(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testDFRegion || !strings.Contains(x.Message, c.arn) {
					t.Errorf("warning Scope %q, Message %q; want %s/%s naming %s", x.Scope, x.Message, testAccountID, testDFRegion, c.arn)
				}
			}
			if c.typ == TypeDeviceFarmProject {
				if got := calls.listedProjects("ListDevicePools"); !slices.Equal(got, []string{testDFProjectARN("project-tagged")}) {
					t.Errorf("ListDevicePools projects = %v; want only the stored project", got)
				}
			}
		})
	}
}

// Denied tag reads in several phases send one warning for the whole scan;
// the rows are kept.
func TestScanDeviceFarm_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newDFCalls()
	for _, op := range []string{"ListProjects", "ListNetworkProfiles", "ListTestGridProjects"} {
		for _, arn := range dfKindOf(t, op).ownARNs() {
			calls.errs[arn] = dfTestDenied
		}
	}

	total, _, err := dfScan(context.Background(), st, dfHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != dfTestTotal {
		t.Errorf("total = %d; want %d", total, dfTestTotal)
	}
	if got := amWarnings(warns()); !slices.Equal(got, []string{dfTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, dfTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op, in another
// phase, is demoted to one warning, so the hard error decides the outcome.
func TestScanDeviceFarm_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, dfTagsOp)
	calls := newDFCalls()
	calls.errs[dfKindOf(t, "ListInstanceProfiles").arn("", "tagged")] = dfTestThrottle
	calls.errs[dfKindOf(t, "ListTestGridProjects").arn("", "bare")] = dfTestBad

	_, _, err := dfScan(context.Background(), st, dfHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ArgumentException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", dfTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored. A failed project write lists no project's children.
func TestScanDeviceFarm_FailedWriteStopsScan(t *testing.T) {
	t.Run("instance profiles", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		refuseStoreWrites(t, st, TypeDeviceFarmInstanceProfile)
		calls := newDFCalls()
		calls.errs[dfKindOf(t, "ListInstanceProfiles").arn("", "tagged")] = dfTestDenied

		total, _, err := dfScan(context.Background(), st, dfHandlers(calls))
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		for _, op := range []string{"ListDeviceInstances", "ListVPCEConfigurations", "ListTestGridProjects", "ListUploads"} {
			if n := calls.count(op); n != 0 {
				t.Errorf("%s called %d times; want 0 after the failed write", op, n)
			}
		}
		// The projects with their pools and network profiles.
		if want := 2 + 2*(2*2+1); total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
	})
	t.Run("projects", func(t *testing.T) {
		st := newTestStore(t)
		refuseStoreWrites(t, st, TypeDeviceFarmProject)
		calls := newDFCalls()

		_, _, err := dfScan(context.Background(), st, dfHandlers(calls))
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		for _, op := range []string{"ListDevicePools", "ListNetworkProfiles", "ListInstanceProfiles", "ListUploads"} {
			if n := calls.count(op); n != 0 {
				t.Errorf("%s called %d times after the failed project write", op, n)
			}
		}
	})
}

// A listing's failed later page keeps the pages read before it, and its
// error does not stop the later phases. A project not stored has no
// children read.
func TestScanDeviceFarm_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		op, project         string
		kept, lost          string
		wantTotal, wantLast int
	}{
		{
			"ListInstanceProfiles", "",
			dfKindOf(t, "ListInstanceProfiles").arn("", "tagged"), dfKindOf(t, "ListInstanceProfiles").arn("", "bare"),
			dfTestTotal - 1, 2,
		},
		{
			"ListNetworkProfiles", testDFProjectARN("project-tagged"),
			dfKindOf(t, "ListNetworkProfiles").arn("project-tagged", "tagged"), dfKindOf(t, "ListNetworkProfiles").arn("project-tagged", "bare"),
			// The bare profile and the curated one on the failed page.
			dfTestTotal - 2, 2,
		},
		// The projects' second page: project-bare and its children are lost.
		{
			"ListProjects", "",
			testDFProjectARN("project-tagged"), testDFProjectARN("project-bare"),
			dfTestTotal - 1 - 2 - 2, 2,
		},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newDFCalls()
			handlers := dfHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil && sv(bacField[*string](in, "Arn")) == c.project {
					calls.record(c.op, in)
					return nil, dfTestBad
				}
				return list(in)
			}

			total, _, err := dfScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ArgumentException")
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			rows := rowsByNativeID(t, st, dfKindOf(t, c.op).typ)
			if _, ok := rows[c.kept]; !ok {
				t.Errorf("%s, read before the failure, missing", c.kept)
			}
			if _, ok := rows[c.lost]; ok {
				t.Errorf("%s stored; its page failed", c.lost)
			}
			if got := len(rowsByNativeID(t, st, TypeDeviceFarmTestGridProject)); got != c.wantLast {
				t.Errorf("test-grid projects (the last tagged phase) = %d; want %d", got, c.wantLast)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanDeviceFarm_RepeatedTokenKeepsPages(t *testing.T) {
	for _, op := range []string{"ListDeviceInstances", "ListDevicePools"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newDFCalls()
			handlers := dfHandlers(calls)
			twoPages := handlers[op]
			handlers[op] = func(in any) (any, error) {
				out, err := twoPages(in)
				switch o := out.(type) {
				case *devicefarm.ListDeviceInstancesOutput:
					o.NextToken = sdkaws.String("same")
				case *devicefarm.ListDevicePoolsOutput:
					o.NextToken = sdkaws.String("same")
				}
				return out, err
			}

			total, _, err := dfScan(context.Background(), st, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != dfTestTotal {
				t.Errorf("total = %d; want %d", total, dfTestTotal)
			}
			k := dfKindOf(t, op)
			want := len(k.ownARNs())
			if k.curated {
				want++
			}
			if n := len(rowsByNativeID(t, st, k.typ)); n != want {
				t.Errorf("%s rows = %d; want %d", k.typ, n, want)
			}
		})
	}
}

// One project's failed pool or network profile listing costs the other
// project none of its rows: a project deleted since it was listed is
// skipped silently, a denial warns once under the op, and any other error is
// returned.
func TestScanDeviceFarm_PerProjectListingFailures(t *testing.T) {
	failed := testDFProjectARN("project-tagged")
	for _, op := range []string{"ListDevicePools", "ListNetworkProfiles"} {
		for _, e := range []struct {
			name      string
			err       error
			code      string
			wantWarns int
		}{
			{"project deleted", dfTestGone, "", 0},
			{"denied", dfTestDenied, "", 1},
			{"other error", dfTestBad, "ArgumentException", 0},
		} {
			t.Run(op+"/"+e.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "devicefarm:"+op)
				calls := newDFCalls()
				handlers := dfHandlers(calls)
				list := handlers[op]
				handlers[op] = func(in any) (any, error) {
					if sv(bacField[*string](in, "Arn")) == failed {
						calls.record(op, in)
						return nil, e.err
					}
					return list(in)
				}

				total, _, err := dfScan(context.Background(), st, handlers)
				if e.code == "" {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, outcomeError)
					assertAPICode(t, err, e.code)
				}
				k := dfKindOf(t, op)
				rows := rowsByNativeID(t, st, k.typ)
				for _, s := range []string{"tagged", "bare"} {
					if _, ok := rows[k.arn("project-bare", s)]; !ok {
						t.Errorf("project-bare's %s missing", k.arn("project-bare", s))
					}
				}
				if len(rows) != 2 {
					t.Errorf("%s rows = %d; want project-bare's 2", k.typ, len(rows))
				}
				// project-tagged's two own rows and its curated one.
				if want := dfTestTotal - 3; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				if *warns != e.wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, e.wantWarns)
				}
			})
		}
	}
}

// An element without an ARN is not stored and not sent to the tag op, and
// is warned about once under its listing, keyed by its name, or by the
// fallback text when it has none.
func TestScanDeviceFarm_ElementWithoutARN(t *testing.T) {
	for _, c := range []struct {
		name, op, key string
		el            dfEl
		lost          int
	}{
		{"test-grid project", "ListTestGridProjects", "tg-x", dfEl{name: "tg-x"}, 2},
		{"device instance", "ListDeviceInstances", "udid-x", dfEl{name: "udid-x"}, 2},
		{"pool with no name", "ListDevicePools", dfNoIdentifier, dfEl{}, 2*2 + 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newDFCalls()
			handlers := dfHandlers(calls)
			k := dfKindOf(t, c.op)
			handlers[c.op] = func(in any) (any, error) {
				calls.record(c.op, in)
				el := c.el
				if el.name == "" {
					// A pool with neither ARN nor name: the page builder sets
					// both, so clear them on the built element.
					out := k.pageOf([]dfEl{el}, nil).(*devicefarm.ListDevicePoolsOutput)
					out.DevicePools[0].Arn, out.DevicePools[0].Name = nil, nil
					return out, nil
				}
				return k.pageOf([]dfEl{el}, nil), nil
			}

			total, _, err := dfScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := dfTestTotal - c.lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "devicefarm:"+c.op || !strings.Contains(w[0].Message, c.key) {
				t.Fatalf("warnings = %v; want one under devicefarm:%s naming %s", w, c.op, c.key)
			}
			if n := len(rowsByNativeID(t, st, k.typ)); n != 0 {
				t.Errorf("%s rows = %d; want 0", k.typ, n)
			}
			if slices.Contains(calls.sortedTagARNs(), "") {
				t.Error("an empty ARN was sent to ListTagsForResource")
			}
		})
	}
}

// An account not allowlisted for VPC endpoint configurations has none: on
// the first page that answer stores nothing, silently. On a later page it
// follows a page the op served, so it is an error and the first page is
// kept.
func TestScanDeviceFarm_VPCENotAllowlisted(t *testing.T) {
	notAllowlisted := apiErr("ServiceAccountException", "Account 123 is not allowlisted for VPC endpoint configurations")
	for _, c := range []struct {
		name      string
		onPage    int
		wantRows  int
		wantError bool
	}{
		{"first page", 1, 0, false},
		{"later page", 2, 1, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newDFCalls()
			handlers := dfHandlers(calls)
			list := handlers["ListVPCEConfigurations"]
			handlers["ListVPCEConfigurations"] = func(in any) (any, error) {
				later := bacField[*string](in, "NextToken") != nil
				if later == (c.onPage == 2) {
					calls.record("ListVPCEConfigurations", in)
					return nil, notAllowlisted
				}
				return list(in)
			}

			total, _, err := dfScan(context.Background(), st, handlers)
			if c.wantError {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "ServiceAccountException")
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if want := dfTestTotal - 2 + c.wantRows; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if n := len(rowsByNativeID(t, st, TypeDeviceFarmVPCEConfiguration)); n != c.wantRows {
				t.Errorf("VPCE rows = %d; want %d", n, c.wantRows)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
		})
	}
}

// A denied ListProjects warns under it, and the account-wide phases still
// run and store their rows.
func TestScanDeviceFarm_ProjectsDeniedLaterPhasesRun(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newDFCalls()
	handlers := dfHandlers(calls)
	delete(handlers, "ListProjects")

	total, _, err := dfScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := 4 * 2; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	if got := amWarnings(warns()); !slices.Equal(got, []string{"devicefarm:ListProjects"}) {
		t.Errorf("warnings = %v; want one under devicefarm:ListProjects", got)
	}
	if n := calls.count("ListDevicePools"); n != 0 {
		t.Errorf("ListDevicePools called %d times; want 0 without projects", n)
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing and its tag reads, and the later phases are
// not run.
func TestScanDeviceFarm_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newDFCalls()
	handlers := dfHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListNetworkProfiles"]
	handlers["ListNetworkProfiles"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := dfScan(ctx, st, handlers)
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
	if n := calls.count("ListInstanceProfiles"); n != 0 {
		t.Errorf("ListInstanceProfiles called %d times after ctx ended", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanDeviceFarm_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "devicefarm.us-west-2.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := dfHandlers(newDFCalls())
		handlers["ListTestGridProjects"] = func(any) (any, error) { return nil, nx }
		total, _, err := dfScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := dfTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newDFCalls()
		calls.errs[dfKindOf(t, "ListVPCEConfigurations").arn("", "bare")] = nx
		_, _, err := dfScan(context.Background(), st, dfHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newDFCalls()
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(map[string]func(any) (any, error){
			"ListProjects": func(in any) (any, error) {
				calls.record("ListProjects", in)
				return nil, nx
			},
		}, nx)
		_, _, err := scanDeviceFarm(context.Background(), acct, "", st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("ListProjects"); n != 1 {
			t.Errorf("ListProjects called %d times; want 1", n)
		}
	})
}
