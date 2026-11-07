package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/supplychain"
	scntypes "github.com/aws/aws-sdk-go-v2/service/supplychain/types"
	"github.com/icearp/disco-cli/store"
)

const (
	scnTestInstA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	scnTestInstB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	// scnTestInstDel is listed with State Deleted.
	scnTestInstDel = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"

	// scnTestTotal is every row of the fixture: three instances; for each of
	// the two live instances two flows, two custom namespaces and two
	// datasets in each of its four namespaces (asc, default and the two
	// custom ones).
	scnTestTotal = 3 + 2*2 + 2*2 + 2*4*2
	// scnTestInstAChildren is every row listed under instance A.
	scnTestInstAChildren = 2 + 2 + 4*2
)

var (
	scnTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: scn:List")
	scnTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	scnTestBad      = apiErr("ValidationException", "bad")
	scnTestGone     = apiErr("ResourceNotFoundException", "gone")
	scnTestCreated  = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
)

// scnTestFixture answers every Supply Chain op a scan calls, in partition and
// region: instances A and a deleted one on page one, B on page two; each live
// instance has two pages of flows (flow-tagged, flow-bare) and of custom
// namespaces (ns_tagged, ns_bare), and every namespace of it, asc and default
// included, two pages of datasets (ds_tagged, ds_bare); every child listing
// of the deleted instance answers ResourceNotFoundException. Every "tagged" row,
// instance A included, is tagged with its type. Every call is recorded; the
// tag reads answer errs by ARN first.
type scnTestFixture struct {
	partition, region string

	mu         sync.Mutex
	maxResults map[string][]int32
	parents    map[string][]string
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

func newSCNFixture(partition, region string) *scnTestFixture {
	f := &scnTestFixture{
		partition: partition, region: region,
		maxResults: map[string][]int32{}, parents: map[string][]string{},
		tags: map[string]map[string]string{}, errs: map[string]error{},
	}
	f.tags[f.instARN(scnTestInstA)] = map[string]string{"type": TypeSupplyChainInstance}
	for _, inst := range []string{scnTestInstA, scnTestInstB} {
		f.tags[f.flowARN(inst, "flow-tagged")] = map[string]string{"type": TypeSupplyChainDataIntegrationFlow}
		f.tags[f.nsARN(inst, "ns_tagged")] = map[string]string{"type": TypeSupplyChainNamespace}
		for _, ns := range []string{"asc", "default", "ns_tagged", "ns_bare"} {
			f.tags[f.dsARN(inst, ns, "ds_tagged")] = map[string]string{"type": TypeSupplyChainDataset}
		}
	}
	return f
}

func (f *scnTestFixture) instARN(inst string) string {
	return "arn:" + f.partition + ":scn:" + f.region + ":" + testAccountID + ":instance/" + inst
}

func (f *scnTestFixture) flowARN(inst, name string) string {
	return f.instARN(inst) + "/data-integration-flows/" + name
}

func (f *scnTestFixture) nsARN(inst, ns string) string {
	return f.instARN(inst) + "/namespaces/" + ns
}

func (f *scnTestFixture) dsARN(inst, ns, name string) string {
	return f.nsARN(inst, ns) + "/datasets/" + name
}

// rows is the ARN of every row of type typ the fixture lists, sorted.
func (f *scnTestFixture) rows(typ string) []string {
	var arns []string
	live := []string{scnTestInstA, scnTestInstB}
	switch typ {
	case TypeSupplyChainInstance:
		arns = []string{f.instARN(scnTestInstA), f.instARN(scnTestInstB), f.instARN(scnTestInstDel)}
	case TypeSupplyChainDataIntegrationFlow:
		for _, inst := range live {
			arns = append(arns, f.flowARN(inst, "flow-tagged"), f.flowARN(inst, "flow-bare"))
		}
	case TypeSupplyChainNamespace:
		for _, inst := range live {
			arns = append(arns, f.nsARN(inst, "ns_tagged"), f.nsARN(inst, "ns_bare"))
		}
	case TypeSupplyChainDataset:
		for _, inst := range live {
			for _, ns := range []string{"asc", "default", "ns_tagged", "ns_bare"} {
				arns = append(arns, f.dsARN(inst, ns, "ds_tagged"), f.dsARN(inst, ns, "ds_bare"))
			}
		}
	}
	slices.Sort(arns)
	return arns
}

// record notes op's MaxResults and, on a first page, the parent it lists.
func (f *scnTestFixture) record(op string, maxResults *int32, token *string, parent string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.maxResults[op] = append(f.maxResults[op], sdkaws.ToInt32(maxResults))
	if token == nil {
		f.parents[op] = append(f.parents[op], parent)
	}
}

func (f *scnTestFixture) calls(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.maxResults[op])
}

func (f *scnTestFixture) listedParents(op string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(slices.Values(f.parents[op]))
}

func (f *scnTestFixture) sortedTagARNs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(slices.Values(f.tagARNs))
}

// scnTwoPages is first with the token "p2" for the first page, then second.
func scnTwoPages[T any](token *string, first, second T) (T, *string) {
	if token == nil {
		return first, sdkaws.String("p2")
	}
	return second, nil
}

func (f *scnTestFixture) listTags(in any) (any, error) {
	arn := sv(in.(*supplychain.ListTagsForResourceInput).ResourceArn)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tagARNs = append(f.tagARNs, arn)
	if err := f.errs[arn]; err != nil {
		return nil, err
	}
	if arn == f.instARN(scnTestInstDel) {
		// What the op may answer for a deleted instance.
		return nil, scnTestGone
	}
	return &supplychain.ListTagsForResourceOutput{Tags: f.tags[arn]}, nil
}

func (f *scnTestFixture) handlers() map[string]func(in any) (any, error) {
	instance := func(id string, state scntypes.InstanceState) scntypes.Instance {
		return scntypes.Instance{
			InstanceId: sdkaws.String(id), AwsAccountId: sdkaws.String(testAccountID), State: state,
			InstanceName: sdkaws.String("name-" + id[:8]), CreatedTime: &scnTestCreated, VersionNumber: sdkaws.Float64(2),
		}
	}
	return map[string]func(in any) (any, error){
		"ListTagsForResource": f.listTags,
		"ListInstances": func(in any) (any, error) {
			p := in.(*supplychain.ListInstancesInput)
			f.record("ListInstances", p.MaxResults, p.NextToken, "")
			var out supplychain.ListInstancesOutput
			out.Instances, out.NextToken = scnTwoPages(p.NextToken,
				[]scntypes.Instance{instance(scnTestInstA, scntypes.InstanceStateActive), instance(scnTestInstDel, scntypes.InstanceStateDeleted)},
				[]scntypes.Instance{instance(scnTestInstB, scntypes.InstanceStateActive)})
			return &out, nil
		},
		"ListDataIntegrationFlows": func(in any) (any, error) {
			p := in.(*supplychain.ListDataIntegrationFlowsInput)
			inst := sv(p.InstanceId)
			f.record("ListDataIntegrationFlows", p.MaxResults, p.NextToken, inst)
			if inst == scnTestInstDel {
				return nil, scnTestGone
			}
			flow := func(name string) []scntypes.DataIntegrationFlow {
				return []scntypes.DataIntegrationFlow{{
					InstanceId: p.InstanceId, Name: sdkaws.String(name), CreatedTime: &scnTestCreated, LastModifiedTime: &scnTestCreated,
					Target: &scntypes.DataIntegrationFlowTarget{TargetType: scntypes.DataIntegrationFlowTargetTypeDataset},
				}}
			}
			var out supplychain.ListDataIntegrationFlowsOutput
			out.Flows, out.NextToken = scnTwoPages(p.NextToken, flow("flow-tagged"), flow("flow-bare"))
			return &out, nil
		},
		"ListDataLakeNamespaces": func(in any) (any, error) {
			p := in.(*supplychain.ListDataLakeNamespacesInput)
			inst := sv(p.InstanceId)
			f.record("ListDataLakeNamespaces", p.MaxResults, p.NextToken, inst)
			if inst == scnTestInstDel {
				return nil, scnTestGone
			}
			ns := func(name string) []scntypes.DataLakeNamespace {
				return []scntypes.DataLakeNamespace{{
					InstanceId: p.InstanceId, Name: sdkaws.String(name), Arn: sdkaws.String(f.nsARN(inst, name)),
					Description: sdkaws.String("custom " + name), CreatedTime: &scnTestCreated, LastModifiedTime: &scnTestCreated,
				}}
			}
			var out supplychain.ListDataLakeNamespacesOutput
			out.Namespaces, out.NextToken = scnTwoPages(p.NextToken, ns("ns_tagged"), ns("ns_bare"))
			return &out, nil
		},
		"ListDataLakeDatasets": func(in any) (any, error) {
			p := in.(*supplychain.ListDataLakeDatasetsInput)
			inst, ns := sv(p.InstanceId), sv(p.Namespace)
			f.record("ListDataLakeDatasets", p.MaxResults, p.NextToken, inst+"/"+ns)
			if inst == scnTestInstDel {
				return nil, scnTestGone
			}
			ds := func(name string) []scntypes.DataLakeDataset {
				return []scntypes.DataLakeDataset{{
					InstanceId: p.InstanceId, Namespace: p.Namespace, Name: sdkaws.String(name), Arn: sdkaws.String(f.dsARN(inst, ns, name)),
					Schema: &scntypes.DataLakeDatasetSchema{Name: sdkaws.String("schema_" + name)}, CreatedTime: &scnTestCreated, LastModifiedTime: &scnTestCreated,
				}}
			}
			var out supplychain.ListDataLakeDatasetsOutput
			out.Datasets, out.NextToken = scnTwoPages(p.NextToken, ds("ds_tagged"), ds("ds_bare"))
			return &out, nil
		},
	}
}

// scnScan runs the scan's real entry point against handlers in the test
// account and region; an op without a handler is denied.
func scnScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, scnTestDenied)
	return scanSupplyChain(ctx, acct, testRegion, st, testScanID)
}

func scnWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

var scnTestTypes = []string{TypeSupplyChainInstance, TypeSupplyChainDataIntegrationFlow, TypeSupplyChainNamespace, TypeSupplyChainDataset}

// Every listing stores both its pages for every parent, each row under its
// ARN (the instance's and flow's built in the Service Reference format), named
// by its name, its attributes the listed element, with the tags its tag read
// returns (nil for none). The deleted instance is stored with its State and
// has no tag read; its children are listed, and their not-found skipped.
// Datasets are listed for each instance's asc and default namespaces and each
// live instance's custom ones. Every listing is sent MaxResults 20. No error,
// no warning.
func TestScanSupplyChain_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newSCNFixture("aws", testRegion)

	total, inserted, err := scnScan(context.Background(), st, f.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != scnTestTotal || inserted != scnTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, scnTestTotal, scnTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	var wantTagARNs []string
	for _, typ := range scnTestTypes {
		rows := rowsByNativeID(t, st, typ)
		if got := slices.Sorted(maps.Keys(rows)); !slices.Equal(got, f.rows(typ)) {
			t.Errorf("%s rows = %v\nwant %v", typ, got, f.rows(typ))
		}
		for _, arn := range f.rows(typ) {
			r := rows[arn]
			if r.CreatedAt != nil || sv(r.Region) != testRegion {
				t.Errorf("%s CreatedAt, Region = %v, %q; want nil, %q", arn, r.CreatedAt, sv(r.Region), testRegion)
			}
			if want := f.tags[arn]; want != nil {
				assertTagsMap(t, r.TagsJSON, want)
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
			if arn != f.instARN(scnTestInstDel) {
				wantTagARNs = append(wantTagARNs, arn)
			}
		}
	}
	slices.Sort(wantTagARNs)
	if got := f.sortedTagARNs(); !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, wantTagARNs)
	}

	insts := rowsByNativeID(t, st, TypeSupplyChainInstance)
	for id, state := range map[string]string{scnTestInstA: "Active", scnTestInstB: "Active", scnTestInstDel: "Deleted"} {
		r := insts[f.instARN(id)]
		var attrs scntypes.Instance
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || sv(attrs.InstanceId) != id || sv(attrs.AwsAccountId) != testAccountID || sdkaws.ToFloat64(attrs.VersionNumber) != 2 {
			t.Errorf("instance %s attrs = %s (%v); want the listed element", id, r.AttributesJSON, err)
		}
		if sv(r.Name) != "name-"+id[:8] || sv(r.Status) != state {
			t.Errorf("instance %s Name, Status = %q, %q; want %q, %q", id, sv(r.Name), sv(r.Status), "name-"+id[:8], state)
		}
	}
	flow := rowsByNativeID(t, st, TypeSupplyChainDataIntegrationFlow)[f.flowARN(scnTestInstB, "flow-bare")]
	var flowAttrs scntypes.DataIntegrationFlow
	if err := json.Unmarshal([]byte(flow.AttributesJSON), &flowAttrs); err != nil || sv(flowAttrs.InstanceId) != scnTestInstB || sv(flowAttrs.Name) != "flow-bare" ||
		flowAttrs.Target == nil || flowAttrs.Target.TargetType != scntypes.DataIntegrationFlowTargetTypeDataset {
		t.Errorf("flow attrs = %s (%v); want the listed element", flow.AttributesJSON, err)
	}
	if sv(flow.Name) != "flow-bare" || flow.Status != nil {
		t.Errorf("flow Name, Status = %q, %v; want flow-bare, nil", sv(flow.Name), flow.Status)
	}
	ns := rowsByNativeID(t, st, TypeSupplyChainNamespace)[f.nsARN(scnTestInstA, "ns_bare")]
	var nsAttrs struct{ Arn, Name, Description string }
	if err := json.Unmarshal([]byte(ns.AttributesJSON), &nsAttrs); err != nil || nsAttrs.Arn != f.nsARN(scnTestInstA, "ns_bare") || nsAttrs.Description != "custom ns_bare" {
		t.Errorf("namespace attrs = %s (%v); want the listed element", ns.AttributesJSON, err)
	}
	if sv(ns.Name) != "ns_bare" || ns.Status != nil {
		t.Errorf("namespace Name, Status = %q, %v; want ns_bare, nil", sv(ns.Name), ns.Status)
	}
	ds := rowsByNativeID(t, st, TypeSupplyChainDataset)[f.dsARN(scnTestInstA, "asc", "ds_tagged")]
	var dsAttrs struct {
		Namespace string
		Schema    struct{ Name string }
	}
	if err := json.Unmarshal([]byte(ds.AttributesJSON), &dsAttrs); err != nil || dsAttrs.Namespace != "asc" || dsAttrs.Schema.Name != "schema_ds_tagged" {
		t.Errorf("dataset attrs = %s (%v); want the listed element", ds.AttributesJSON, err)
	}
	if sv(ds.Name) != "ds_tagged" || ds.Status != nil {
		t.Errorf("dataset Name, Status = %q, %v; want ds_tagged, nil", sv(ds.Name), ds.Status)
	}

	every := []string{scnTestInstA, scnTestInstB, scnTestInstDel}
	perNamespace := []string{scnTestInstDel + "/asc", scnTestInstDel + "/default"}
	for _, inst := range []string{scnTestInstA, scnTestInstB} {
		for _, n := range []string{"asc", "default", "ns_bare", "ns_tagged"} {
			perNamespace = append(perNamespace, inst+"/"+n)
		}
	}
	slices.Sort(perNamespace)
	for op, want := range map[string][]string{
		"ListInstances": {""}, "ListDataIntegrationFlows": every, "ListDataLakeNamespaces": every, "ListDataLakeDatasets": perNamespace,
	} {
		if got := f.listedParents(op); !slices.Equal(got, want) {
			t.Errorf("%s parents = %v; want %v", op, got, want)
		}
		for _, n := range f.maxResults[op] {
			if n != 20 {
				t.Errorf("%s MaxResults = %d; want 20", op, n)
			}
		}
	}
}

// A rescan reports every stored row in its total, not only the newly inserted
// ones. The dispatcher reports total for the service, and a first scan into an
// empty store has total == inserted, so only a rescan catches the two swapped.
func TestScanSupplyChain_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, _, err := scnScan(context.Background(), st, newSCNFixture("aws", testRegion).handlers()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, _, err := scnScan(context.Background(), st, newSCNFixture("aws", testRegion).handlers())
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != scnTestTotal {
		t.Errorf("rescan total = %d; want %d", total, scnTestTotal)
	}
}

// No instance: nothing stored, no child listing or tag read, no error, no
// warning.
func TestScanSupplyChain_NoInstances(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newSCNFixture("aws", testRegion)
	h := f.handlers()
	h["ListInstances"] = func(in any) (any, error) {
		p := in.(*supplychain.ListInstancesInput)
		f.record("ListInstances", p.MaxResults, p.NextToken, "")
		return &supplychain.ListInstancesOutput{}, nil
	}

	total, inserted, err := scnScan(context.Background(), st, h)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListDataIntegrationFlows", "ListDataLakeNamespaces", "ListDataLakeDatasets"} {
		if n := f.calls(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
	if got := f.sortedTagARNs(); len(got) != 0 {
		t.Errorf("tag reads = %v; want none", got)
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently (an instance or namespace
// with its children, which are then not listed). A denial keeps the row
// untagged and warns once under the tag op with the scan's scope; a throttle
// keeps it and is transient; any other error keeps it and is an error.
func TestScanSupplyChain_TagReadFailures(t *testing.T) {
	t.Parallel()
	f0 := newSCNFixture("aws", testRegion)
	inst := f0.instARN(scnTestInstA)
	flow := f0.flowARN(scnTestInstB, "flow-tagged")
	ns := f0.nsARN(scnTestInstA, "ns_tagged")
	ds := f0.dsARN(scnTestInstB, "default", "ds_bare")
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
		{"instance/not found", inst, TypeSupplyChainInstance, scnTestGone, -1, "", scnTestTotal - 1 - scnTestInstAChildren, false, false},
		{"instance/denied", inst, TypeSupplyChainInstance, scnTestDenied, -1, "", scnTestTotal, true, true},
		{"instance/transient", inst, TypeSupplyChainInstance, scnTestThrottle, outcomeTransient, "ThrottlingException", scnTestTotal, true, false},
		{"flow/not found", flow, TypeSupplyChainDataIntegrationFlow, scnTestGone, -1, "", scnTestTotal - 1, false, false},
		{"flow/other error", flow, TypeSupplyChainDataIntegrationFlow, scnTestBad, outcomeError, "ValidationException", scnTestTotal, true, false},
		{"namespace/not found", ns, TypeSupplyChainNamespace, scnTestGone, -1, "", scnTestTotal - 1 - 2, false, false},
		{"namespace/denied", ns, TypeSupplyChainNamespace, scnTestDenied, -1, "", scnTestTotal, true, true},
		{"dataset/not found", ds, TypeSupplyChainDataset, scnTestGone, -1, "", scnTestTotal - 1, false, false},
		{"dataset/transient", ds, TypeSupplyChainDataset, scnTestThrottle, outcomeTransient, "ThrottlingException", scnTestTotal, true, false},
		{"dataset/other error", ds, TypeSupplyChainDataset, scnTestBad, outcomeError, "ValidationException", scnTestTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newSCNFixture("aws", testRegion)
			f.errs[c.arn] = c.err

			total, _, err := scnScan(context.Background(), st, f.handlers())
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
				want = []string{scnTagsOp}
			}
			if got := scnWarnOps(w); !slices.Equal(got, want) {
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

// Denied tag reads in every phase send one warning for the whole scan; the
// rows are kept.
func TestScanSupplyChain_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newSCNFixture("aws", testRegion)
	for _, typ := range scnTestTypes {
		for _, arn := range f.rows(typ) {
			f.errs[arn] = scnTestDenied
		}
	}

	total, _, err := scnScan(context.Background(), st, f.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != scnTestTotal {
		t.Errorf("total = %d; want %d", total, scnTestTotal)
	}
	if got := scnWarnOps(warns()); !slices.Equal(got, []string{scnTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, scnTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanSupplyChain_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, scnTagsOp)
	f := newSCNFixture("aws", testRegion)
	f.errs[f.flowARN(scnTestInstA, "flow-bare")] = scnTestThrottle
	f.errs[f.dsARN(scnTestInstB, "asc", "ds_tagged")] = scnTestBad

	_, _, err := scnScan(context.Background(), st, f.handlers())
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", scnTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored.
func TestScanSupplyChain_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	t.Run("namespaces", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		refuseStoreWrites(t, st, TypeSupplyChainNamespace)
		f := newSCNFixture("aws", testRegion)
		f.errs[f.nsARN(scnTestInstA, "ns_bare")] = scnTestDenied

		total, _, err := scnScan(context.Background(), st, f.handlers())
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := f.calls("ListDataLakeDatasets"); n != 0 {
			t.Errorf("ListDataLakeDatasets called %d times; want 0 after the failed write", n)
		}
		// The instances and the flows.
		if want := 3 + 4; total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
	})
	t.Run("instances", func(t *testing.T) {
		st := newTestStore(t)
		refuseStoreWrites(t, st, TypeSupplyChainInstance)
		f := newSCNFixture("aws", testRegion)

		_, _, err := scnScan(context.Background(), st, f.handlers())
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		for _, op := range []string{"ListDataIntegrationFlows", "ListDataLakeNamespaces", "ListDataLakeDatasets"} {
			if n := f.calls(op); n != 0 {
				t.Errorf("%s called %d times after the failed instance write", op, n)
			}
		}
	})
}

// A listing's failed later page keeps the pages read before it, and its
// error does not stop the later phases. An instance not stored has no
// children read.
func TestScanSupplyChain_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	f0 := newSCNFixture("aws", testRegion)
	for _, c := range []struct {
		op, parent           string
		typ, kept, lost      string
		wantTotal, wantLater int
	}{
		// Instance A's second page of flows; every dataset is still listed.
		{
			"ListDataIntegrationFlows", scnTestInstA, TypeSupplyChainDataIntegrationFlow,
			f0.flowARN(scnTestInstA, "flow-tagged"), f0.flowARN(scnTestInstA, "flow-bare"),
			scnTestTotal - 1, 16,
		},
		// The instances' second page: B and its children are lost.
		{
			"ListInstances", "", TypeSupplyChainInstance,
			f0.instARN(scnTestInstA), f0.instARN(scnTestInstB),
			scnTestTotal - 1 - scnTestInstAChildren, 8,
		},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			f := newSCNFixture("aws", testRegion)
			h := f.handlers()
			list := h[c.op]
			h[c.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil && sv(bacField[*string](in, "InstanceId")) == c.parent {
					return nil, scnTestBad
				}
				return list(in)
			}

			total, _, err := scnScan(context.Background(), st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			rows := rowsByNativeID(t, st, c.typ)
			if _, ok := rows[c.kept]; !ok {
				t.Errorf("%s, read before the failure, missing", c.kept)
			}
			if _, ok := rows[c.lost]; ok {
				t.Errorf("%s stored; its page failed", c.lost)
			}
			if got := len(rowsByNativeID(t, st, TypeSupplyChainDataset)); got != c.wantLater {
				t.Errorf("datasets (the last phase) = %d; want %d", got, c.wantLater)
			}
		})
	}
}

// A repeated nextToken ends the listing as an error, keeping its pages.
func TestScanSupplyChain_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newSCNFixture("aws", testRegion)
	h := f.handlers()
	list := h["ListDataLakeNamespaces"]
	h["ListDataLakeNamespaces"] = func(in any) (any, error) {
		out, err := list(in)
		if err != nil {
			return nil, err
		}
		out.(*supplychain.ListDataLakeNamespacesOutput).NextToken = sdkaws.String("same")
		return out, nil
	}

	total, _, err := scnScan(context.Background(), st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != scnTestTotal {
		t.Errorf("total = %d; want %d", total, scnTestTotal)
	}
	if n := len(rowsByNativeID(t, st, TypeSupplyChainNamespace)); n != 4 {
		t.Errorf("namespaces = %d; want 4", n)
	}
}

// One parent's failed child listing costs the other parents none of their
// rows. Every listing models ResourceNotFoundException, so an instance or
// namespace deleted since it was listed is skipped silently. A denial warns
// once under the op, and any other error is returned.
func TestScanSupplyChain_PerParentListingFailures(t *testing.T) {
	t.Parallel()
	f0 := newSCNFixture("aws", testRegion)
	for _, c := range []struct {
		op, parent string
		typ        string
		// lostUnder prefixes the ARN of every row of typ the failure loses.
		lostUnder string
	}{
		{"ListDataIntegrationFlows", scnTestInstB, TypeSupplyChainDataIntegrationFlow, f0.instARN(scnTestInstB) + "/"},
		{"ListDataLakeNamespaces", scnTestInstA, TypeSupplyChainNamespace, f0.instARN(scnTestInstA) + "/"},
		{"ListDataLakeDatasets", scnTestInstA + "/asc", TypeSupplyChainDataset, f0.nsARN(scnTestInstA, "asc") + "/"},
	} {
		for _, e := range []struct {
			name      string
			err       error
			code      string
			wantWarns int
		}{
			{"not found", scnTestGone, "", 0},
			{"denied", scnTestDenied, "", 1},
			{"other error", scnTestBad, "ValidationException", 0},
		} {
			t.Run(c.op+"/"+e.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "supplychain:"+c.op)
				f := newSCNFixture("aws", testRegion)
				h := f.handlers()
				list := h[c.op]
				h[c.op] = func(in any) (any, error) {
					parent := sv(bacField[*string](in, "InstanceId"))
					if ns := bacField[*string](in, "Namespace"); ns != nil {
						parent += "/" + *ns
					}
					if parent == c.parent {
						return nil, e.err
					}
					return list(in)
				}

				total, _, err := scnScan(context.Background(), st, h)
				if e.code == "" {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, outcomeError)
					assertAPICode(t, err, e.code)
				}
				// Each failed parent lists two rows.
				lost := 2
				if c.op == "ListDataLakeNamespaces" {
					// The lost namespaces' datasets are not listed either.
					lost += 2 * 2
				}
				if total != scnTestTotal-lost {
					t.Errorf("total = %d; want %d", total, scnTestTotal-lost)
				}
				rows := rowsByNativeID(t, st, c.typ)
				for _, arn := range f.rows(c.typ) {
					_, stored := rows[arn]
					if want := !strings.HasPrefix(arn, c.lostUnder); stored != want {
						t.Errorf("%s stored = %v; want %v", arn, stored, want)
					}
				}
				if *warns != e.wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, e.wantWarns)
				}
			})
		}
	}
}

// An element without its identifier is not stored, is not sent to the tag
// op, and is warned about once under its listing, keyed by its name, its
// parent, or the fallback text. A namespace without a Name is stored but its
// datasets are not listed, with one warning naming it.
func TestScanSupplyChain_ElementWithoutIdentifier(t *testing.T) {
	t.Parallel()
	f0 := newSCNFixture("aws", testRegion)
	nameless := f0.nsARN(scnTestInstA, "x")
	for _, c := range []struct {
		name, op, key string
		answer        func(in any) any
		typ           string
		wantRows      int
		wantTotal     int
	}{
		{
			"instance without id", "ListInstances", "orphan",
			func(any) any {
				return &supplychain.ListInstancesOutput{Instances: []scntypes.Instance{{InstanceName: sdkaws.String("orphan"), State: scntypes.InstanceStateActive}}}
			},
			TypeSupplyChainInstance, 0, 0,
		},
		{
			"instance without id or name", "ListInstances", noIdentifier,
			func(any) any {
				return &supplychain.ListInstancesOutput{Instances: []scntypes.Instance{{State: scntypes.InstanceStateActive}}}
			},
			TypeSupplyChainInstance, 0, 0,
		},
		{
			"flow without name", "ListDataIntegrationFlows", scnTestInstA,
			func(in any) any {
				if sv(bacField[*string](in, "InstanceId")) != scnTestInstA {
					return &supplychain.ListDataIntegrationFlowsOutput{}
				}
				return &supplychain.ListDataIntegrationFlowsOutput{Flows: []scntypes.DataIntegrationFlow{{InstanceId: sdkaws.String(scnTestInstA)}}}
			},
			TypeSupplyChainDataIntegrationFlow, 0, scnTestTotal - 4,
		},
		{
			"namespace without ARN", "ListDataLakeNamespaces", "ns_x",
			func(in any) any {
				if sv(bacField[*string](in, "InstanceId")) != scnTestInstA {
					return &supplychain.ListDataLakeNamespacesOutput{}
				}
				return &supplychain.ListDataLakeNamespacesOutput{Namespaces: []scntypes.DataLakeNamespace{{InstanceId: sdkaws.String(scnTestInstA), Name: sdkaws.String("ns_x")}}}
			},
			// No custom namespace stored: their eight datasets are not listed.
			TypeSupplyChainNamespace, 0, scnTestTotal - 4 - 8,
		},
		{
			"namespace without name", "ListDataLakeNamespaces", nameless,
			func(in any) any {
				if sv(bacField[*string](in, "InstanceId")) != scnTestInstA {
					return &supplychain.ListDataLakeNamespacesOutput{}
				}
				return &supplychain.ListDataLakeNamespacesOutput{Namespaces: []scntypes.DataLakeNamespace{{InstanceId: sdkaws.String(scnTestInstA), Arn: sdkaws.String(nameless)}}}
			},
			// One namespace stored in place of four, and no custom
			// namespace's datasets listed.
			TypeSupplyChainNamespace, 1, scnTestTotal - 3 - 8,
		},
		{
			"dataset without ARN", "ListDataLakeDatasets", "ds_x",
			func(in any) any {
				if sv(bacField[*string](in, "Namespace")) != "default" {
					return &supplychain.ListDataLakeDatasetsOutput{}
				}
				return &supplychain.ListDataLakeDatasetsOutput{Datasets: []scntypes.DataLakeDataset{{Name: sdkaws.String("ds_x")}}}
			},
			TypeSupplyChainDataset, 0, scnTestTotal - 16,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newSCNFixture("aws", testRegion)
			h := f.handlers()
			h[c.op] = func(in any) (any, error) { return c.answer(in), nil }

			total, _, err := scnScan(context.Background(), st, h)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != c.wantRows {
				t.Errorf("%s rows = %d; want %d", c.typ, n, c.wantRows)
			}
			for _, parent := range f.listedParents("ListDataLakeDatasets") {
				if strings.HasPrefix(parent, "/") || strings.HasSuffix(parent, "/") {
					t.Errorf("ListDataLakeDatasets sent %q; want an InstanceId and a Namespace", parent)
				}
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "supplychain:"+c.op || !strings.Contains(w[0].Message, c.key) {
				t.Fatalf("warnings = %v; want one under supplychain:%s naming %s", w, c.op, c.key)
			}
			if slices.Contains(f.sortedTagARNs(), "") {
				t.Error("an empty ARN was sent to ListTagsForResource")
			}
		})
	}
}

// A namespace listed without its InstanceId is stored, and its datasets are
// listed under the instance it was listed under, with no warning.
func TestScanSupplyChain_NamespaceWithoutInstanceIDKeepsDatasets(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newSCNFixture("aws", testRegion)
	h := f.handlers()
	list := h["ListDataLakeNamespaces"]
	h["ListDataLakeNamespaces"] = func(in any) (any, error) {
		out, err := list(in)
		if err != nil {
			return nil, err
		}
		for i := range out.(*supplychain.ListDataLakeNamespacesOutput).Namespaces {
			out.(*supplychain.ListDataLakeNamespacesOutput).Namespaces[i].InstanceId = nil
		}
		return out, nil
	}

	total, _, err := scnScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != scnTestTotal {
		t.Errorf("total = %d; want %d", total, scnTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	rows := rowsByNativeID(t, st, TypeSupplyChainDataset)
	if got := slices.Sorted(maps.Keys(rows)); !slices.Equal(got, f.rows(TypeSupplyChainDataset)) {
		t.Errorf("dataset rows = %v\nwant %v", got, f.rows(TypeSupplyChainDataset))
	}
}

// In GovCloud and China the built instance and flow ARNs carry the account's
// partition, and every ARN reaches the tag op unchanged.
func TestScanSupplyChain_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			f := newSCNFixture(pf.partition, pf.region)
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(f.handlers(), scnTestDenied)

			if _, _, err := scanSupplyChain(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			inst := map[string]string{
				"aws-cn":     "arn:aws-cn:scn:cn-north-1:123456789012:instance/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
				"aws-us-gov": "arn:aws-us-gov:scn:us-gov-west-1:123456789012:instance/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			}[pf.partition]
			for _, arn := range []string{
				inst, inst + "/data-integration-flows/flow-tagged",
				f.nsARN(scnTestInstB, "ns_tagged"), f.dsARN(scnTestInstB, "asc", "ds_tagged"),
			} {
				if !slices.Contains(f.sortedTagARNs(), arn) {
					t.Fatalf("ListTagsForResource ARNs = %v; want %s among them", f.sortedTagARNs(), arn)
				}
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeSupplyChainInstance)[f.instARN(scnTestInstA)].TagsJSON, map[string]string{"type": TypeSupplyChainInstance})
			assertTagsMap(t, rowsByNativeID(t, st, TypeSupplyChainDataIntegrationFlow)[f.flowARN(scnTestInstA, "flow-tagged")].TagsJSON, map[string]string{"type": TypeSupplyChainDataIntegrationFlow})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing and its tag reads, and the later phases are
// not run.
func TestScanSupplyChain_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newSCNFixture("aws", testRegion)
	h := f.handlers()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := h["ListDataIntegrationFlows"]
	h["ListDataIntegrationFlows"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := scnScan(ctx, st, h)
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
	if n := f.calls("ListDataLakeNamespaces"); n != 0 {
		t.Errorf("ListDataLakeNamespaces called %d times after ctx ended", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanSupplyChain_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "scn.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		h := newSCNFixture("aws", testRegion).handlers()
		h["ListDataLakeDatasets"] = func(any) (any, error) { return nil, nx }
		total, _, err := scnScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		if want := scnTestTotal - 16; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		f := newSCNFixture("aws", testRegion)
		f.errs[f.nsARN(scnTestInstB, "ns_bare")] = nx
		_, _, err := scnScan(context.Background(), st, f.handlers())
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanSupplyChain(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
