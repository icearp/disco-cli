package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lookoutequipment"
	letypes "github.com/aws/aws-sdk-go-v2/service/lookoutequipment/types"
	"github.com/icearp/disco-cli/store"
)

const (
	// leTestTotal is every row of the fixture: two inference schedulers,
	// datasets, label groups and models (one per page), and two versions
	// (one per page) of each model.
	leTestTotal = 2*4 + 2*2
	leTestRole  = "arn:aws:iam::123456789012:role/le-scheduler"
)

var (
	leTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: lookoutequipment:List")
	leTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	leTestBad      = apiErr("ValidationException", "bad")
	leTestGone     = apiErr("ResourceNotFoundException", "gone")
	leTestModels   = []string{"model-bare", "model-tagged"}
)

// leTestARN is the ARN of the kind (dataset, inference-scheduler,
// label-group, model) named name.
func leTestARN(kind, name string) string {
	return "arn:aws:lookoutequipment:" + testRegion + ":" + testAccountID + ":" + kind + "/" + name + "/id-" + name
}

// leTestVersionARN is the ARN of version n of model, as the SDK returns it.
func leTestVersionARN(model string, n int64) string {
	return leTestARN("model", model) + "/model-version/" + strconv.FormatInt(n, 10)
}

// leTestVersionID is the NativeID of version n of model.
func leTestVersionID(model string, n int64) string {
	return leTestARN("model", model) + "/version/" + strconv.FormatInt(n, 10)
}

func leTestVersion(model string, n int64) letypes.ModelVersionSummary {
	return letypes.ModelVersionSummary{
		ModelArn: sdkaws.String(leTestARN("model", model)), ModelName: sdkaws.String(model), ModelVersion: sdkaws.Int64(n),
		ModelVersionArn: sdkaws.String(leTestVersionARN(model, n)), Status: letypes.ModelVersionStatusSuccess,
	}
}

// leKind is one account-wide listing: its op, the type it stores, its ARN's
// resource kind, the prefix of its rows' names, the Status its elements
// carry ("" for none) and the attribute holding a row's ARN.
type leKind struct {
	op, typ, kind, short, status, arnField string
}

func leKinds() []leKind {
	return []leKind{
		{"ListInferenceSchedulers", TypeLookoutEquipmentInferenceScheduler, "inference-scheduler", "sched", "RUNNING", "InferenceSchedulerArn"},
		{"ListDatasets", TypeLookoutEquipmentDataset, "dataset", "ds", "ACTIVE", "DatasetArn"},
		{"ListLabelGroups", TypeLookoutEquipmentLabelGroup, "label-group", "lg", "", "LabelGroupArn"},
		{"ListModels", TypeLookoutEquipmentModel, "model", "model", "SUCCESS", "ModelArn"},
	}
}

func leKindOf(t *testing.T, op string) leKind {
	t.Helper()
	for _, k := range leKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no Lookout for Equipment listing", op)
	return leKind{}
}

func (k leKind) arn(suffix string) string { return leTestARN(k.kind, k.short+"-"+suffix) }

// page is k's output holding the element named name under arn, and next.
func (k leKind) page(arn, name, next *string) any {
	switch k.op {
	case "ListInferenceSchedulers":
		return &lookoutequipment.ListInferenceSchedulersOutput{NextToken: next, InferenceSchedulerSummaries: []letypes.InferenceSchedulerSummary{
			{InferenceSchedulerArn: arn, InferenceSchedulerName: name, Status: letypes.InferenceSchedulerStatus(k.status)},
		}}
	case "ListDatasets":
		return &lookoutequipment.ListDatasetsOutput{NextToken: next, DatasetSummaries: []letypes.DatasetSummary{
			{DatasetArn: arn, DatasetName: name, Status: letypes.DatasetStatus(k.status)},
		}}
	case "ListLabelGroups":
		return &lookoutequipment.ListLabelGroupsOutput{NextToken: next, LabelGroupSummaries: []letypes.LabelGroupSummary{
			{LabelGroupArn: arn, LabelGroupName: name},
		}}
	default:
		return &lookoutequipment.ListModelsOutput{NextToken: next, ModelSummaries: []letypes.ModelSummary{
			{ModelArn: arn, ModelName: name, Status: letypes.ModelStatus(k.status)},
		}}
	}
}

// leCalls records the calls of a scan and answers the tag reads, by ARN,
// with tags or errs, and the Describes, by name, with describeErrs.
type leCalls struct {
	mu           sync.Mutex
	maxResults   map[string][]int32
	calls        map[string]int
	versionsOf   []string
	described    []string
	tagARNs      []string
	tags         map[string]map[string]string
	errs         map[string]error
	describeErrs map[string]error
}

// newLECalls tags every "-tagged" row and each model's version 1 with its
// type.
func newLECalls() *leCalls {
	c := &leCalls{maxResults: map[string][]int32{}, calls: map[string]int{}, tags: map[string]map[string]string{}, errs: map[string]error{}, describeErrs: map[string]error{}}
	for _, k := range leKinds() {
		c.tags[k.arn("tagged")] = map[string]string{"type": k.typ}
	}
	for _, m := range leTestModels {
		c.tags[leTestVersionARN(m, 1)] = map[string]string{"type": TypeLookoutEquipmentModelVersion}
	}
	return c
}

// record notes op's call and its MaxResults, and on a first ListModelVersions
// page the model it lists.
func (c *leCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[op]++
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		c.maxResults[op] = append(c.maxResults[op], *n)
	}
	if v, ok := in.(*lookoutequipment.ListModelVersionsInput); ok && v.NextToken == nil {
		c.versionsOf = append(c.versionsOf, sv(v.ModelName))
	}
}

func (c *leCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

func (c *leCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *leCalls) listTags(in any) (any, error) {
	arn := sv(in.(*lookoutequipment.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	out := &lookoutequipment.ListTagsForResourceOutput{}
	for k, v := range c.tags[arn] {
		out.Tags = append(out.Tags, letypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return out, nil
}

// describe answers DescribeInferenceScheduler with a body carrying the
// RoleArn the list summary lacks.
func (c *leCalls) describe(in any) (any, error) {
	name := sv(in.(*lookoutequipment.DescribeInferenceSchedulerInput).InferenceSchedulerName)
	c.record("DescribeInferenceScheduler", in)
	c.mu.Lock()
	c.described = append(c.described, name)
	err := c.describeErrs[name]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	arn := leTestARN("inference-scheduler", name)
	return &lookoutequipment.DescribeInferenceSchedulerOutput{
		InferenceSchedulerArn: &arn, InferenceSchedulerName: &name, RoleArn: sdkaws.String(leTestRole), Status: letypes.InferenceSchedulerStatusRunning,
	}, nil
}

// twoPages answers k's op with "-tagged" on the first page and "-bare" on
// the second.
func (c *leCalls) twoPages(k leKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		name, arn := k.short+"-"+suffix, k.arn(suffix)
		return k.page(&arn, &name, next), nil
	}
}

// versions answers ListModelVersions with version 1 of the model on the
// first page and version 2 on the second; model-bare's version 2 is listed
// without a Status.
func (c *leCalls) versions(in any) (any, error) {
	c.record("ListModelVersions", in)
	req := in.(*lookoutequipment.ListModelVersionsInput)
	n, next := int64(1), sdkaws.String("p2")
	if req.NextToken != nil {
		n, next = 2, nil
	}
	v := leTestVersion(sv(req.ModelName), n)
	if sv(req.ModelName) == "model-bare" && n == 2 {
		v.Status = ""
	}
	return &lookoutequipment.ListModelVersionsOutput{NextToken: next, ModelVersionSummaries: []letypes.ModelVersionSummary{v}}, nil
}

// leHandlers answers every op the scan calls from c.
func leHandlers(c *leCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"ListTagsForResource":        c.listTags,
		"DescribeInferenceScheduler": c.describe,
		"ListModelVersions":          c.versions,
	}
	for _, k := range leKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// leScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func leScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, leTestDenied)
	return scanLookoutEquipment(ctx, acct, testRegion, st, testScanID)
}

// leAllTagARNs is the ARN of every row of the fixture, sorted: the model
// versions' as the SDK returns them, not their NativeIDs.
func leAllTagARNs() []string {
	var arns []string
	for _, k := range leKinds() {
		arns = append(arns, k.arn("tagged"), k.arn("bare"))
	}
	for _, m := range leTestModels {
		arns = append(arns, leTestVersionARN(m, 1), leTestVersionARN(m, 2))
	}
	slices.Sort(arns)
	return arns
}

func leWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// leAttrs decodes a row's attributes.
func leAttrs(t *testing.T, r store.Resource) map[string]any {
	t.Helper()
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("%s attrs %s: %v", r.NativeID, r.AttributesJSON, err)
	}
	return attrs
}

// Every listing stores both its pages, each row under its ARN, named by its
// name, with its Status (none for a label group), no CreatedAt, its listed
// element as attributes (an inference scheduler's Describe body), and the
// tags its tag read returns (nil for none). A model version is stored under
// {modelArn}/version/{n}, named {model}/{n}, and has its tags read with its
// listed ModelVersionArn. ListTagsForResource is sent exactly the ARNs of
// every row; every listing is sent MaxResults 500; versions are listed for
// both models and both schedulers described. No error, no warning.
func TestScanLookoutEquipment_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newLECalls()

	total, inserted, err := leScan(context.Background(), st, leHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != leTestTotal || inserted != leTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, leTestTotal, leTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range leKinds() {
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
			if want := k.short + "-" + suffix; sv(r.Name) != want || sv(r.Region) != testRegion || sv(r.Status) != k.status || r.CreatedAt != nil {
				t.Errorf("%s Name, Region, Status, CreatedAt = %q, %q, %v, %v; want %q, %q, %q, nil", arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, want, testRegion, k.status)
			}
			if k.status == "" && r.Status != nil {
				t.Errorf("%s Status = %q; want nil", arn, *r.Status)
			}
			attrs := leAttrs(t, r)
			if attrs[k.arnField] != arn {
				t.Errorf("%s attrs = %s; want its element", arn, r.AttributesJSON)
			}
			if _, described := attrs["RoleArn"]; described != (k.typ == TypeLookoutEquipmentInferenceScheduler) || (described && attrs["RoleArn"] != leTestRole) {
				t.Errorf("%s attrs = %s; want RoleArn %s only on an inference scheduler", arn, r.AttributesJSON, leTestRole)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	versions := rowsByNativeID(t, st, TypeLookoutEquipmentModelVersion)
	if len(versions) != 4 {
		t.Errorf("model versions = %d; want 4", len(versions))
	}
	for _, m := range leTestModels {
		for _, n := range []int64{1, 2} {
			id := leTestVersionID(m, n)
			r, ok := versions[id]
			if !ok {
				t.Errorf("model version %s missing", id)
				continue
			}
			wantStatus := "SUCCESS"
			if m == "model-bare" && n == 2 {
				wantStatus = ""
			}
			if want := m + "/" + strconv.FormatInt(n, 10); sv(r.Name) != want || sv(r.Region) != testRegion || sv(r.Status) != wantStatus || r.CreatedAt != nil {
				t.Errorf("%s Name, Region, Status, CreatedAt = %q, %q, %q, %v; want %q, %q, %q, nil", id, sv(r.Name), sv(r.Region), sv(r.Status), r.CreatedAt, want, testRegion, wantStatus)
			}
			if wantStatus == "" && r.Status != nil {
				t.Errorf("%s Status = %q; want nil", id, *r.Status)
			}
			if attrs := leAttrs(t, r); attrs["ModelVersionArn"] != leTestVersionARN(m, n) || attrs["ModelArn"] != leTestARN("model", m) {
				t.Errorf("%s attrs = %s; want its element", id, r.AttributesJSON)
			}
			if n == 1 {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeLookoutEquipmentModelVersion})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", id, *r.TagsJSON)
			}
		}
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, leAllTagARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, leAllTagARNs())
	}
	if got := slices.Sorted(slices.Values(calls.versionsOf)); !slices.Equal(got, leTestModels) {
		t.Errorf("ListModelVersions models = %v; want %v", got, leTestModels)
	}
	if got := slices.Sorted(slices.Values(calls.described)); !slices.Equal(got, []string{"sched-bare", "sched-tagged"}) {
		t.Errorf("DescribeInferenceScheduler names = %v; want both schedulers", got)
	}
	for _, op := range []string{"ListInferenceSchedulers", "ListDatasets", "ListLabelGroups", "ListModels", "ListModelVersions"} {
		if got, want := calls.maxResults[op], calls.count(op); len(got) != want || want == 0 {
			t.Errorf("%s sent MaxResults on %d of %d calls", op, len(got), want)
		}
		for _, n := range calls.maxResults[op] {
			if n != 500 {
				t.Errorf("%s MaxResults = %d; want 500", op, n)
			}
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanLookoutEquipment_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := leScan(context.Background(), st, leHandlers(newLECalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := leScan(context.Background(), st, leHandlers(newLECalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != leTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, leTestTotal)
	}
}

// Empty listings: nothing stored, no Describe, version listing or tag read,
// no error, no warning.
func TestScanLookoutEquipment_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newLECalls()
	handlers := leHandlers(calls)
	handlers["ListInferenceSchedulers"] = func(in any) (any, error) {
		calls.record("ListInferenceSchedulers", in)
		return &lookoutequipment.ListInferenceSchedulersOutput{}, nil
	}
	handlers["ListDatasets"] = func(in any) (any, error) {
		calls.record("ListDatasets", in)
		return &lookoutequipment.ListDatasetsOutput{}, nil
	}
	handlers["ListLabelGroups"] = func(in any) (any, error) {
		calls.record("ListLabelGroups", in)
		return &lookoutequipment.ListLabelGroupsOutput{}, nil
	}
	handlers["ListModels"] = func(in any) (any, error) {
		calls.record("ListModels", in)
		return &lookoutequipment.ListModelsOutput{}, nil
	}

	total, inserted, err := leScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range leKinds() {
		if n := calls.count(k.op); n != 1 {
			t.Errorf("%s called %d times; want 1", k.op, n)
		}
	}
	for _, op := range []string{"DescribeInferenceScheduler", "ListModelVersions", "ListTagsForResource"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently (a model with its
// versions, which are then not listed). A denial keeps the row untagged and
// warns once under the tag op with the scan's scope; a throttle keeps it and
// is transient; any other error keeps it and is an error.
func TestScanLookoutEquipment_TagReadFailures(t *testing.T) {
	version := leTestVersionARN("model-tagged", 1)
	versionID := leTestVersionID("model-tagged", 1)
	model := leKindOf(t, "ListModels").arn("bare")
	for _, c := range []struct {
		name          string
		tagARN, rowID string
		typ           string
		err           error
		outcome       serviceOutcome // -1: no error
		code          string
		wantTotal     int
		wantKept      bool
		warn          bool
	}{
		{"model version/not found", version, versionID, TypeLookoutEquipmentModelVersion, leTestGone, -1, "", leTestTotal - 1, false, false},
		{"model version/denied", version, versionID, TypeLookoutEquipmentModelVersion, leTestDenied, -1, "", leTestTotal, true, true},
		{"model version/transient", version, versionID, TypeLookoutEquipmentModelVersion, leTestThrottle, outcomeTransient, "ThrottlingException", leTestTotal, true, false},
		{"model version/other error", version, versionID, TypeLookoutEquipmentModelVersion, leTestBad, outcomeError, "ValidationException", leTestTotal, true, false},
		{"model/not found", model, model, TypeLookoutEquipmentModel, leTestGone, -1, "", leTestTotal - 1 - 2, false, false},
		{"dataset/denied", leKindOf(t, "ListDatasets").arn("tagged"), leKindOf(t, "ListDatasets").arn("tagged"), TypeLookoutEquipmentDataset, leTestDenied, -1, "", leTestTotal, true, true},
		{"inference scheduler/other error", leKindOf(t, "ListInferenceSchedulers").arn("bare"), leKindOf(t, "ListInferenceSchedulers").arn("bare"), TypeLookoutEquipmentInferenceScheduler, leTestBad, outcomeError, "ValidationException", leTestTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newLECalls()
			calls.errs[c.tagARN] = c.err

			total, _, err := leScan(context.Background(), st, leHandlers(calls))
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
			r, ok := rowsByNativeID(t, st, c.typ)[c.rowID]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{leTagsOp}
			}
			if got := leWarnOps(w); !slices.Equal(got, want) {
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
func TestScanLookoutEquipment_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newLECalls()
	calls.errs[leKindOf(t, "ListLabelGroups").arn("bare")] = leTestDenied
	calls.errs[leKindOf(t, "ListModels").arn("tagged")] = leTestDenied
	calls.errs[leTestVersionARN("model-bare", 2)] = leTestDenied

	total, _, err := leScan(context.Background(), st, leHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != leTestTotal {
		t.Errorf("total = %d; want %d", total, leTestTotal)
	}
	if got := leWarnOps(warns()); !slices.Equal(got, []string{leTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, leTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanLookoutEquipment_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, leTagsOp)
	calls := newLECalls()
	calls.errs[leKindOf(t, "ListDatasets").arn("bare")] = leTestThrottle
	calls.errs[leTestVersionARN("model-tagged", 2)] = leTestBad

	_, _, err := leScan(context.Background(), st, leHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", leTagsOp, *warns)
	}
}

// A failed Describe. ResourceNotFoundException is modeled, so a scheduler
// deleted since it was listed is dropped silently and has no tag read. Any
// other failure stores the list summary as the row, its tags still read: a
// denial warns once under the Describe op, a throttle is transient, any
// other error is an error. A scheduler listed without a name is not
// described: it is stored as its summary and the scan reports
// errLESchedulerNoName.
func TestScanLookoutEquipment_DescribeFailures(t *testing.T) {
	const describeOp = "lookoutequipment:DescribeInferenceScheduler"
	arn := leKindOf(t, "ListInferenceSchedulers").arn("tagged")
	for _, c := range []struct {
		name      string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"not found", leTestGone, -1, "", leTestTotal - 1, false, false},
		{"denied", leTestDenied, -1, "", leTestTotal, true, true},
		{"transient", leTestThrottle, outcomeTransient, "ThrottlingException", leTestTotal, true, false},
		{"other error", leTestBad, outcomeError, "ValidationException", leTestTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newLECalls()
			calls.describeErrs["sched-tagged"] = c.err

			total, _, err := leScan(context.Background(), st, leHandlers(calls))
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
			r, ok := rowsByNativeID(t, st, TypeLookoutEquipmentInferenceScheduler)[arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if sent := slices.Contains(calls.sortedTagARNs(), arn); sent != c.wantKept {
				t.Errorf("tag read sent = %v; want %v", sent, c.wantKept)
			}
			if ok {
				attrs := leAttrs(t, r)
				if _, described := attrs["RoleArn"]; described || attrs["InferenceSchedulerArn"] != arn || attrs["Status"] != "RUNNING" {
					t.Errorf("attrs = %s; want the list summary", r.AttributesJSON)
				}
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeLookoutEquipmentInferenceScheduler})
			}
			var want []string
			if c.warn {
				want = []string{describeOp}
			}
			if got := leWarnOps(warns()); !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
	t.Run("no name", func(t *testing.T) {
		st := newTestStore(t)
		calls := newLECalls()
		handlers := leHandlers(calls)
		handlers["ListInferenceSchedulers"] = func(in any) (any, error) {
			calls.record("ListInferenceSchedulers", in)
			return &lookoutequipment.ListInferenceSchedulersOutput{InferenceSchedulerSummaries: []letypes.InferenceSchedulerSummary{{InferenceSchedulerArn: &arn}}}, nil
		}

		total, _, err := leScan(context.Background(), st, handlers)
		if !errors.Is(err, errLESchedulerNoName) {
			t.Fatalf("err = %v; want errLESchedulerNoName", err)
		}
		if want := leTestTotal - 1; total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
		if n := calls.count("DescribeInferenceScheduler"); n != 0 {
			t.Errorf("DescribeInferenceScheduler called %d times; want 0", n)
		}
		r, ok := rowsByNativeID(t, st, TypeLookoutEquipmentInferenceScheduler)[arn]
		if !ok {
			t.Fatal("scheduler without a name not stored")
		}
		if leAttrs(t, r)["InferenceSchedulerArn"] != arn {
			t.Errorf("attrs = %s; want the list summary", r.AttributesJSON)
		}
	})
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phase's rows
// stay stored.
func TestScanLookoutEquipment_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeLookoutEquipmentDataset)
	calls := newLECalls()
	calls.errs[leKindOf(t, "ListDatasets").arn("tagged")] = leTestDenied

	total, _, err := leScan(context.Background(), st, leHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListLabelGroups", "ListModels", "ListModelVersions"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0 after the failed write", op, n)
		}
	}
	if total != 2 {
		t.Errorf("total = %d; want the 2 inference schedulers", total)
	}
	if n := len(rowsByNativeID(t, st, TypeLookoutEquipmentInferenceScheduler)); n != 2 {
		t.Errorf("inference schedulers = %d; want 2", n)
	}
}

// A listing's failed later page keeps the page read before it, and its error
// does not stop the later phases. A model not stored has no versions read.
func TestScanLookoutEquipment_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		op                   string
		wantTotal, wantLater int
	}{
		{"ListInferenceSchedulers", leTestTotal - 1, 4},
		{"ListModels", leTestTotal - 1 - 2, 2},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newLECalls()
			handlers := leHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.record(c.op, in)
					return nil, leTestBad
				}
				return list(in)
			}

			total, _, err := leScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			k := leKindOf(t, c.op)
			rows := rowsByNativeID(t, st, k.typ)
			if _, ok := rows[k.arn("tagged")]; !ok {
				t.Errorf("%s, read before the failure, missing", k.arn("tagged"))
			}
			if _, ok := rows[k.arn("bare")]; ok {
				t.Errorf("%s stored; its page failed", k.arn("bare"))
			}
			if got := len(rowsByNativeID(t, st, TypeLookoutEquipmentModelVersion)); got != c.wantLater {
				t.Errorf("model versions (the last phase) = %d; want %d", got, c.wantLater)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanLookoutEquipment_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	calls := newLECalls()
	handlers := leHandlers(calls)
	twoPages := handlers["ListLabelGroups"]
	handlers["ListLabelGroups"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*lookoutequipment.ListLabelGroupsOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, _, err := leScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != leTestTotal {
		t.Errorf("total = %d; want %d", total, leTestTotal)
	}
	if n := len(rowsByNativeID(t, st, TypeLookoutEquipmentLabelGroup)); n != 2 {
		t.Errorf("label groups = %d; want 2", n)
	}
}

// One model's failed version listing costs the other model none of its
// rows: a model deleted since it was listed is skipped silently, or warned
// about when part of its listing was read; a denial warns once under the op;
// any other error is returned.
func TestScanLookoutEquipment_PerModelVersionFailures(t *testing.T) {
	const versionsOp = "lookoutequipment:ListModelVersions"
	for _, c := range []struct {
		name       string
		laterPage  bool
		err        error
		outcome    serviceOutcome // -1: no error
		wantTagged int
		warn       bool
	}{
		{"not found", false, leTestGone, -1, 0, false},
		{"not found mid-listing", true, leTestGone, -1, 1, true},
		{"denied", false, leTestDenied, -1, 0, true},
		{"other error", false, leTestBad, outcomeError, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newLECalls()
			handlers := leHandlers(calls)
			handlers["ListModelVersions"] = func(in any) (any, error) {
				req := in.(*lookoutequipment.ListModelVersionsInput)
				if sv(req.ModelName) == "model-tagged" && (req.NextToken != nil) == c.laterPage {
					calls.record("ListModelVersions", in)
					return nil, c.err
				}
				return calls.versions(in)
			}

			total, _, err := leScan(context.Background(), st, handlers)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, "ValidationException")
			}
			if want := leTestTotal - 2 + c.wantTagged; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			rows := rowsByNativeID(t, st, TypeLookoutEquipmentModelVersion)
			for _, id := range []string{leTestVersionID("model-bare", 1), leTestVersionID("model-bare", 2)} {
				if _, ok := rows[id]; !ok {
					t.Errorf("%s of the other model missing", id)
				}
			}
			if _, ok := rows[leTestVersionID("model-tagged", 1)]; ok != (c.wantTagged == 1) {
				t.Errorf("model-tagged version 1 stored = %v; want %v", ok, c.wantTagged == 1)
			}
			var want []string
			if c.warn {
				want = []string{versionsOp}
			}
			if got := leWarnOps(warns()); !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

// Listed elements without the ids a row needs. A dataset without an ARN and
// a model version without a ModelArn or ModelVersion are not stored, not sent
// to the tag op, and warned about once under their listing. A model without a
// name is stored without its versions read, warned about under ListModels. A
// model version without a ModelVersionArn is stored untagged and the scan
// reports errLEVersionNoARN.
func TestScanLookoutEquipment_ElementsWithoutIDs(t *testing.T) {
	t.Run("dataset without ARN", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newLECalls()
		handlers := leHandlers(calls)
		twoPages := handlers["ListDatasets"]
		handlers["ListDatasets"] = func(in any) (any, error) {
			out, err := twoPages(in)
			page := out.(*lookoutequipment.ListDatasetsOutput)
			page.DatasetSummaries = append(page.DatasetSummaries, letypes.DatasetSummary{DatasetName: sdkaws.String("ds-noarn")})
			return out, err
		}
		total, _, err := leScan(context.Background(), st, handlers)
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		if total != leTestTotal {
			t.Errorf("total = %d; want %d", total, leTestTotal)
		}
		if got := calls.sortedTagARNs(); !slices.Equal(got, leAllTagARNs()) {
			t.Errorf("ListTagsForResource ARNs = %v; want only the fixture's", got)
		}
		if got := leWarnOps(warns()); !slices.Equal(got, []string{"lookoutequipment:ListDatasets"}) {
			t.Errorf("warnings = %v; want one under lookoutequipment:ListDatasets", got)
		}
	})
	for _, c := range []struct {
		name  string
		model string
		strip func(*letypes.ModelVersionSummary)
	}{
		{"model version without ModelArn", "model-orphan", func(v *letypes.ModelVersionSummary) { v.ModelArn = nil }},
		{"model version without ModelVersion", "model-tagged", func(v *letypes.ModelVersionSummary) { v.ModelVersion = nil }},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newLECalls()
			handlers := leHandlers(calls)
			handlers["ListModelVersions"] = func(in any) (any, error) {
				out, err := calls.versions(in)
				page := out.(*lookoutequipment.ListModelVersionsOutput)
				v := leTestVersion(c.model, 9)
				c.strip(&v)
				page.ModelVersionSummaries = append(page.ModelVersionSummaries, v)
				return out, err
			}
			total, _, err := leScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != leTestTotal {
				t.Errorf("total = %d; want %d", total, leTestTotal)
			}
			if got := len(rowsByNativeID(t, st, TypeLookoutEquipmentModelVersion)); got != 4 {
				t.Errorf("model versions = %d; want the fixture's 4", got)
			}
			if got := calls.sortedTagARNs(); !slices.Equal(got, leAllTagARNs()) {
				t.Errorf("ListTagsForResource ARNs = %v; want only the fixture's", got)
			}
			if got := leWarnOps(warns()); !slices.Equal(got, []string{"lookoutequipment:ListModelVersions"}) {
				t.Errorf("warnings = %v; want one under lookoutequipment:ListModelVersions", got)
			}
		})
	}
	t.Run("model without name", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newLECalls()
		handlers := leHandlers(calls)
		arn := leTestARN("model", "unnamed")
		handlers["ListModels"] = func(in any) (any, error) {
			calls.record("ListModels", in)
			return &lookoutequipment.ListModelsOutput{ModelSummaries: []letypes.ModelSummary{{ModelArn: &arn}}}, nil
		}
		total, _, err := leScan(context.Background(), st, handlers)
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		if want := 2*3 + 1; total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
		if _, ok := rowsByNativeID(t, st, TypeLookoutEquipmentModel)[arn]; !ok {
			t.Error("model without a name not stored")
		}
		if n := calls.count("ListModelVersions"); n != 0 {
			t.Errorf("ListModelVersions called %d times; want 0", n)
		}
		if got := leWarnOps(warns()); !slices.Equal(got, []string{"lookoutequipment:ListModels"}) {
			t.Errorf("warnings = %v; want one under lookoutequipment:ListModels", got)
		}
	})
	t.Run("model version without ModelVersionArn", func(t *testing.T) {
		st := newTestStore(t)
		calls := newLECalls()
		handlers := leHandlers(calls)
		handlers["ListModelVersions"] = func(in any) (any, error) {
			out, err := calls.versions(in)
			for i := range out.(*lookoutequipment.ListModelVersionsOutput).ModelVersionSummaries {
				out.(*lookoutequipment.ListModelVersionsOutput).ModelVersionSummaries[i].ModelVersionArn = nil
			}
			return out, err
		}
		total, _, err := leScan(context.Background(), st, handlers)
		if !errors.Is(err, errLEVersionNoARN) {
			t.Fatalf("err = %v; want errLEVersionNoARN", err)
		}
		if total != leTestTotal {
			t.Errorf("total = %d; want %d", total, leTestTotal)
		}
		r, ok := rowsByNativeID(t, st, TypeLookoutEquipmentModelVersion)[leTestVersionID("model-tagged", 1)]
		if !ok || r.TagsJSON != nil {
			t.Errorf("version stored = %v, TagsJSON = %v; want stored untagged", ok, r.TagsJSON)
		}
		for _, arn := range calls.sortedTagARNs() {
			if strings.Contains(arn, "/model-version/") || strings.Contains(arn, "/version/") {
				t.Errorf("tags read for a version with %s", arn)
			}
		}
	})
}

// A GovCloud or China ARN reaches the tag op unchanged, a model version's
// ModelVersionArn included, and the version's NativeID is built from its
// model's ARN in that partition.
func TestScanLookoutEquipment_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newLECalls()
			handlers := leHandlers(calls)
			prefix := "arn:" + c.partition + ":lookoutequipment:" + c.region + ":" + testAccountID + ":"
			datasetARN := prefix + "dataset/ds-p/id-ds-p"
			modelARN := prefix + "model/m-p/id-m-p"
			versionARN := modelARN + "/model-version/1"
			calls.tags[datasetARN] = map[string]string{"k": "dataset"}
			calls.tags[versionARN] = map[string]string{"k": "version"}
			handlers["ListDatasets"] = func(in any) (any, error) {
				calls.record("ListDatasets", in)
				return &lookoutequipment.ListDatasetsOutput{DatasetSummaries: []letypes.DatasetSummary{{DatasetArn: &datasetARN, DatasetName: sdkaws.String("ds-p")}}}, nil
			}
			handlers["ListModels"] = func(in any) (any, error) {
				calls.record("ListModels", in)
				return &lookoutequipment.ListModelsOutput{ModelSummaries: []letypes.ModelSummary{{ModelArn: &modelARN, ModelName: sdkaws.String("m-p")}}}, nil
			}
			handlers["ListModelVersions"] = func(in any) (any, error) {
				calls.record("ListModelVersions", in)
				return &lookoutequipment.ListModelVersionsOutput{ModelVersionSummaries: []letypes.ModelVersionSummary{
					{ModelArn: &modelARN, ModelName: sdkaws.String("m-p"), ModelVersion: sdkaws.Int64(1), ModelVersionArn: &versionARN},
				}}, nil
			}
			acct := newTestAccount(testAccountID)
			acct.Partition = c.partition
			acct.cfg = opStubConfig(handlers, leTestDenied)
			if _, _, err := scanLookoutEquipment(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			for _, want := range []string{datasetARN, versionARN} {
				if !slices.Contains(calls.sortedTagARNs(), want) {
					t.Errorf("ListTagsForResource ARNs %v lack %s", calls.sortedTagARNs(), want)
				}
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeLookoutEquipmentDataset)[datasetARN].TagsJSON, map[string]string{"k": "dataset"})
			assertTagsMap(t, rowsByNativeID(t, st, TypeLookoutEquipmentModelVersion)[modelARN+"/version/1"].TagsJSON, map[string]string{"k": "version"})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phase is not run.
func TestScanLookoutEquipment_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newLECalls()
	handlers := leHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListModels"]
	handlers["ListModels"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := leScan(ctx, st, handlers)
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
	if n := calls.count("ListModelVersions"); n != 0 {
		t.Errorf("ListModelVersions called %d times after ctx ended", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and no later phase runs.
func TestScanLookoutEquipment_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "lookoutequipment.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := leHandlers(newLECalls())
		handlers["ListModelVersions"] = func(any) (any, error) { return nil, nx }
		total, _, err := leScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := leTestTotal - 4; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newLECalls()
		calls.errs[leKindOf(t, "ListInferenceSchedulers").arn("tagged")] = nx
		_, _, err := leScan(context.Background(), st, leHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newLECalls()
		handlers := map[string]func(in any) (any, error){}
		for _, op := range []string{"ListInferenceSchedulers", "ListDatasets"} {
			handlers[op] = func(in any) (any, error) {
				calls.record(op, in)
				return nil, nx
			}
		}
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(handlers, nx)
		_, _, err := scanLookoutEquipment(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("ListDatasets"); n != 0 {
			t.Errorf("ListDatasets called %d times; want 0 once the region proved unavailable", n)
		}
	})
}
