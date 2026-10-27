package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iotsitewise"
	iswtypes "github.com/aws/aws-sdk-go-v2/service/iotsitewise/types"
	"github.com/icearp/disco-cli/store"
)

var (
	iswDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: iotsitewise:List")
	iswBadInput = apiErr("InvalidRequestException", "bad")
	iswThrottle = apiErr("ThrottlingException", "slow down")
	iswGone     = apiErr("ResourceNotFoundException", "gone")
	iswGap      = apiErr("InvalidRequestException", "Feature not supported yet")
)

// iswNames is the two resources each listing of the fixture serves, "tagged"
// on its first page and "bare" on its second; ListTagsForResource answers
// with tags for every resource whose ARN ends in "tagged".
var iswNames = []string{"tagged", "bare"}

func iswARN(kind, id string) string { return iotSWARN("aws", testRegion, testAccountID, kind, id) }

func iswIsTagged(arn string) bool { return strings.HasSuffix(arn, "tagged") }

// iswRow is one fixture row: its type and NativeID.
type iswRow struct{ typ, arn string }

// iswRows is every row the fixture stores. A child's id names its parent, so
// that ids stay unique across parents.
func iswRows() []iswRow {
	var rows []iswRow
	for _, n := range iswNames {
		rows = append(rows,
			iswRow{TypeIoTSWAssetModel, iswARN("asset-model", n)},
			iswRow{TypeIoTSWComputationModel, iswARN("computation-model", n)},
			iswRow{TypeIoTSWGateway, iswARN("gateway", n)},
			iswRow{TypeIoTSWDataset, iswARN("dataset", n)},
			iswRow{TypeIoTSWWorkspace, iswARN("workspace", n)},
			iswRow{TypeIoTSWApplication, iswARN("workspace", "tagged/application/"+n)},
			iswRow{TypeIoTSWPortal, iswARN("portal", n)})
		for _, c := range iswNames {
			proj := n + "-" + c
			rows = append(rows,
				iswRow{TypeIoTSWAsset, iswARN("asset", proj)},
				iswRow{TypeIoTSWPipeline, iswARN("workspace", n+"/pipeline/"+c)},
				iswRow{TypeIoTSWTask, iswARN("workspace", n+"/task/"+c)},
				iswRow{TypeIoTSWProject, iswARN("project", proj)},
				iswRow{TypeIoTSWAccessPolicy, iswARN("access-policy", "PORTAL-"+proj)})
			for _, d := range iswNames {
				rows = append(rows,
					iswRow{TypeIoTSWDashboard, iswARN("dashboard", proj+"-"+d)},
					iswRow{TypeIoTSWAccessPolicy, iswARN("access-policy", "PROJECT-"+proj+"-"+d)})
			}
		}
	}
	return rows
}

// iswCalls records the stubbed SiteWise calls (bacCalls), the parents each
// child listing is sent, and answers the Describes from describeErrs, keyed
// by the id described.
type iswCalls struct {
	*bacCalls
	assetsOf, projectsOf, dashboardsOf, pipelinesOf, accessOf parentCalls
	describeErrs                                              map[string]error
}

func newISWCalls() *iswCalls {
	c := &iswCalls{bacCalls: newBACCalls(), describeErrs: map[string]error{}}
	for _, r := range iswRows() {
		if iswIsTagged(r.arn) {
			c.tags[r.arn] = map[string]string{"type": r.typ}
		}
	}
	return c
}

func (c *iswCalls) listTags(in any) (any, error) {
	arn := sv(in.(*iotsitewise.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &iotsitewise.ListTagsForResourceOutput{Tags: tags}, nil
}

// describe answers a Describe of id with out, or with id's describeErrs.
func (c *iswCalls) describe(op string, id func(in any) string, out func(id string) any) func(any) (any, error) {
	return func(in any) (any, error) {
		c.called(op, in)
		c.mu.Lock()
		err := c.describeErrs[id(in)]
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return out(id(in)), nil
	}
}

// twoPages answers op with "tagged" on the first page and "bare" on the
// second.
func (c *iswCalls) twoPages(op string, out func(in any, name string, next *string) any) func(any) (any, error) {
	return c.answer(op, func(in any) any {
		if bacField[*string](in, "NextToken") == nil {
			return out(in, "tagged", sdkaws.String("p2"))
		}
		return out(in, "bare", nil)
	})
}

// iswHandlers answers every op the scan calls with the fixture (iswRows).
func iswHandlers(c *iswCalls) map[string]func(any) (any, error) {
	s := sdkaws.String
	return map[string]func(any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListAssetModels": c.twoPages("ListAssetModels", func(_ any, n string, next *string) any {
			return &iotsitewise.ListAssetModelsOutput{AssetModelSummaries: []iswtypes.AssetModelSummary{{Id: s(n), Arn: s(iswARN("asset-model", n)), Name: s("model " + n)}}, NextToken: next}
		}),
		"DescribeAssetModel": c.describe("DescribeAssetModel", func(in any) string { return sv(in.(*iotsitewise.DescribeAssetModelInput).AssetModelId) },
			func(id string) any {
				return &iotsitewise.DescribeAssetModelOutput{AssetModelId: s(id), AssetModelArn: s(iswARN("asset-model", id)), AssetModelDescription: s("described " + id)}
			}),
		"ListAssets": c.twoPages("ListAssets", func(in any, n string, next *string) any {
			p := in.(*iotsitewise.ListAssetsInput)
			c.assetsOf.add(sv(p.AssetModelId) + "/" + string(p.Filter))
			id := sv(p.AssetModelId) + "-" + n
			return &iotsitewise.ListAssetsOutput{AssetSummaries: []iswtypes.AssetSummary{{Id: s(id), Arn: s(iswARN("asset", id))}}, NextToken: next}
		}),
		"ListComputationModels": c.twoPages("ListComputationModels", func(_ any, n string, next *string) any {
			return &iotsitewise.ListComputationModelsOutput{ComputationModelSummaries: []iswtypes.ComputationModelSummary{{Id: s(n), Arn: s(iswARN("computation-model", n))}}, NextToken: next}
		}),
		"DescribeComputationModel": c.describe("DescribeComputationModel", func(in any) string {
			return sv(in.(*iotsitewise.DescribeComputationModelInput).ComputationModelId)
		}, func(id string) any {
			return &iotsitewise.DescribeComputationModelOutput{ComputationModelId: s(id), ComputationModelDescription: s("described " + id)}
		}),
		"ListGateways": c.twoPages("ListGateways", func(_ any, n string, next *string) any {
			return &iotsitewise.ListGatewaysOutput{GatewaySummaries: []iswtypes.GatewaySummary{{GatewayId: s(n), GatewayName: s("gateway " + n)}}, NextToken: next}
		}),
		"ListDatasets": c.twoPages("ListDatasets", func(_ any, n string, next *string) any {
			return &iotsitewise.ListDatasetsOutput{DatasetSummaries: []iswtypes.DatasetSummary{{Id: s(n), Arn: s(iswARN("dataset", n))}}, NextToken: next}
		}),
		"DescribeDataset": c.describe("DescribeDataset", func(in any) string { return sv(in.(*iotsitewise.DescribeDatasetInput).DatasetId) },
			func(id string) any {
				return &iotsitewise.DescribeDatasetOutput{DatasetId: s(id), DatasetDescription: s("described " + id)}
			}),
		"ListWorkspaces": c.twoPages("ListWorkspaces", func(_ any, n string, next *string) any {
			return &iotsitewise.ListWorkspacesOutput{WorkspaceSummaries: []iswtypes.WorkspaceSummary{{
				Name: s(n), Arn: s(iswARN("workspace", n)), Status: &iswtypes.WorkspaceStatus{State: iswtypes.WorkspaceStateActive},
			}}, NextToken: next}
		}),
		"ListApplications": c.twoPages("ListApplications", func(_ any, n string, next *string) any {
			return &iotsitewise.ListApplicationsOutput{Applications: []iswtypes.ApplicationSummary{{
				Id: s(n), Name: s("app " + n), WorkspaceName: s("tagged"), Arn: s(iswARN("workspace", "tagged/application/"+n)), Status: iswtypes.ApplicationStatusActive,
			}}, NextToken: next}
		}),
		"ListPipelines": c.twoPages("ListPipelines", func(in any, n string, next *string) any {
			ws := sv(in.(*iotsitewise.ListPipelinesInput).WorkspaceName)
			c.pipelinesOf.add(ws)
			return &iotsitewise.ListPipelinesOutput{PipelineSummaries: []iswtypes.PipelineSummary{{
				PipelineName: s(n), PipelineArn: s(iswARN("workspace", ws+"/pipeline/"+n)), Status: &iswtypes.ResourceStatus{State: iswtypes.ResourceStateUpdating},
			}}, NextToken: next}
		}),
		"ListTasks": c.twoPages("ListTasks", func(in any, n string, next *string) any {
			ws := sv(in.(*iotsitewise.ListTasksInput).WorkspaceName)
			return &iotsitewise.ListTasksOutput{TaskSummaries: []iswtypes.TaskSummary{{
				TaskName: s(n), TaskArn: s(iswARN("workspace", ws+"/task/"+n)), Status: &iswtypes.ResourceStatus{State: iswtypes.ResourceStateActive},
			}}, NextToken: next}
		}),
		"ListPortals": c.twoPages("ListPortals", func(_ any, n string, next *string) any {
			return &iotsitewise.ListPortalsOutput{PortalSummaries: []iswtypes.PortalSummary{{Id: s(n), Name: s("portal " + n)}}, NextToken: next}
		}),
		"ListProjects": c.twoPages("ListProjects", func(in any, n string, next *string) any {
			portal := sv(in.(*iotsitewise.ListProjectsInput).PortalId)
			c.projectsOf.add(portal)
			return &iotsitewise.ListProjectsOutput{ProjectSummaries: []iswtypes.ProjectSummary{{Id: s(portal + "-" + n)}}, NextToken: next}
		}),
		"ListDashboards": c.twoPages("ListDashboards", func(in any, n string, next *string) any {
			proj := sv(in.(*iotsitewise.ListDashboardsInput).ProjectId)
			c.dashboardsOf.add(proj)
			return &iotsitewise.ListDashboardsOutput{DashboardSummaries: []iswtypes.DashboardSummary{{Id: s(proj + "-" + n)}}, NextToken: next}
		}),
		"ListAccessPolicies": c.twoPages("ListAccessPolicies", func(in any, n string, next *string) any {
			p := in.(*iotsitewise.ListAccessPoliciesInput)
			c.accessOf.add(string(p.ResourceType) + "-" + sv(p.ResourceId))
			return &iotsitewise.ListAccessPoliciesOutput{AccessPolicySummaries: []iswtypes.AccessPolicySummary{{Id: s(string(p.ResourceType) + "-" + sv(p.ResourceId) + "-" + n)}}, NextToken: next}
		}),
	}
}

// iswScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func iswScan(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, iswDenied)
	return scanIoTSiteWise(ctx, acct, testRegion, st, testScanID)
}

func iswRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// Every type stores the tags ListTagsForResource returns for its ARN, and
// nil for a resource without tags; exactly the stored rows are sent to it.
// Both pages of every listing are stored, every listing asks for the model's
// page cap, the described types store their Describe body, every child is
// contained by its parent, and the scan reports no error or warning.
func TestScanIoTSiteWise_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newISWCalls()

	total, inserted, err := iswScan(context.Background(), t, st, iswHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 50 || inserted != 50 {
		t.Errorf("total, inserted = %d, %d; want 50, 50", total, inserted)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantARNs []string
	for _, w := range iswRows() {
		wantARNs = append(wantARNs, w.arn)
		r, ok := iswRowOf(t, st, w.typ, w.arn)
		switch {
		case !ok:
			t.Errorf("%s %s missing", w.typ, w.arn)
		case iswIsTagged(w.arn):
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": w.typ})
		case r.TagsJSON != nil:
			t.Errorf("%s %s TagsJSON = %q; want nil", w.typ, w.arn, *r.TagsJSON)
		}
	}
	slices.Sort(wantARNs)
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", calls.tagARNs, wantARNs)
	}
	for _, op := range []string{
		"ListAssetModels", "ListAssets", "ListComputationModels", "ListGateways", "ListDatasets", "ListWorkspaces",
		"ListApplications", "ListPipelines", "ListTasks", "ListPortals", "ListProjects", "ListDashboards", "ListAccessPolicies",
	} {
		got := calls.maxResults[op]
		if len(got) == 0 {
			t.Errorf("%s never sent MaxResults", op)
		}
		for _, n := range got {
			if n != 250 {
				t.Errorf("%s MaxResults = %d; want 250", op, n)
			}
		}
	}
	for _, d := range []iswRow{
		{TypeIoTSWAssetModel, iswARN("asset-model", "tagged")},
		{TypeIoTSWComputationModel, iswARN("computation-model", "bare")},
		{TypeIoTSWDataset, iswARN("dataset", "tagged")},
	} {
		if r, _ := iswRowOf(t, st, d.typ, d.arn); !strings.Contains(r.AttributesJSON, "Description\":\"described ") {
			t.Errorf("%s attributes = %s; want its Describe body", d.typ, r.AttributesJSON)
		}
	}
	if got, want := calls.assetsOf.distinct(), []string{"bare/ALL", "tagged/ALL"}; !slices.Equal(got, want) {
		t.Errorf("ListAssets model/filter = %v; want %v", got, want)
	}
	if got, want := calls.accessOf.distinct(), []string{
		"PORTAL-bare", "PORTAL-tagged", "PROJECT-bare-bare", "PROJECT-bare-tagged", "PROJECT-tagged-bare", "PROJECT-tagged-tagged",
	}; !slices.Equal(got, want) {
		t.Errorf("ListAccessPolicies parents = %v; want %v", got, want)
	}
	assertContainsNative(t, st, iswARN("portal", "bare"), iswARN("project", "bare-tagged"))
	assertContainsNative(t, st, iswARN("project", "tagged-bare"), iswARN("dashboard", "tagged-bare-tagged"))
	for _, c := range []string{"pipeline/bare", "task/tagged", "application/bare"} {
		assertContainsNative(t, st, iswARN("workspace", "tagged"), iswARN("workspace", "tagged/"+c))
	}
	ws, _ := iswRowOf(t, st, TypeIoTSWWorkspace, iswARN("workspace", "tagged"))
	app, _ := iswRowOf(t, st, TypeIoTSWApplication, iswARN("workspace", "tagged/application/tagged"))
	pl, _ := iswRowOf(t, st, TypeIoTSWPipeline, iswARN("workspace", "bare/pipeline/tagged"))
	tk, _ := iswRowOf(t, st, TypeIoTSWTask, iswARN("workspace", "tagged/task/bare"))
	if sv(pl.Status) != "UPDATING" || sv(tk.Status) != "ACTIVE" {
		t.Errorf("pipeline, task Status = %q, %q; want UPDATING, ACTIVE", sv(pl.Status), sv(tk.Status))
	}
	if sv(ws.Status) != "ACTIVE" || sv(app.Status) != "ACTIVE" || sv(app.Name) != "app tagged" {
		t.Errorf("workspace Status = %q, application Status, Name = %q, %q; want both ACTIVE and Name app tagged", sv(ws.Status), sv(app.Status), sv(app.Name))
	}
}

// A rescan stores the same rows without inserting any.
func TestScanIoTSiteWise_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newISWCalls()
	if _, _, err := iswScan(context.Background(), t, st, iswHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := iswScan(context.Background(), t, st, iswHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != 50 || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want 50, 0", total, inserted)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under iotsitewise:ListTagsForResource, a throttle after
// retries is a transient error, any other code a hard one. A portal the tag
// read no longer finds is dropped, and its projects are never listed; a
// gateway, from an account-wide listing, likewise.
func TestScanIoTSiteWise_TagReadFailures(t *testing.T) {
	for _, target := range []iswRow{
		{TypeIoTSWPortal, iswARN("portal", "tagged")},
		{TypeIoTSWGateway, iswARN("gateway", "tagged")},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", iswDenied, true, 0, 1},
				{"unauthorized", apiErr("UnauthorizedException", "You are not authorized."), true, 0, 1},
				{"not found", iswGone, false, 0, 0},
				{"other error", iswBadInput, true, outcomeError, 0},
				{"transient", iswThrottle, true, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, iotSWListTagsOp)
					calls := newISWCalls()
					calls.errs[target.arn] = c.err

					_, _, err := iswScan(context.Background(), t, st, iswHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.outcome == outcomeError {
						assertAPICode(t, err, "InvalidRequestException")
					}
					r, ok := iswRowOf(t, st, target.typ, target.arn)
					if ok != c.kept {
						t.Fatalf("%s row stored = %v; want %v", target.typ, ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if target.typ == TypeIoTSWPortal {
						if listed := calls.projectsOf.distinct(); slices.Contains(listed, "tagged") != c.kept || !slices.Contains(listed, "bare") {
							t.Errorf("projects listed of portals %v; want the portal only when it is kept, its sibling always", listed)
						}
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", iotSWListTagsOp, *warnings, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanIoTSiteWise_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, iotSWListTagsOp)
	calls := newISWCalls()
	calls.errs[iswARN("dashboard", "tagged-tagged-tagged")] = iswThrottle
	calls.errs[iswARN("dashboard", "bare-bare-bare")] = iswBadInput

	_, _, err := iswScan(context.Background(), t, st, iswHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", iotSWListTagsOp, *warns)
	}
}

// A failed Describe keeps the row as its list element, tags still read,
// unless the resource is gone: a denial warns once under the Describe op, a
// throttle is a transient error, any other code a hard one. An asset model
// DescribeAssetModel no longer finds is dropped and its assets are never
// listed.
func TestScanIoTSiteWise_DescribeFailures(t *testing.T) {
	for _, target := range []struct {
		typ, arn, op, id string
	}{
		{TypeIoTSWAssetModel, iswARN("asset-model", "tagged"), "iotsitewise:DescribeAssetModel", "tagged"},
		{TypeIoTSWDataset, iswARN("dataset", "tagged"), "iotsitewise:DescribeDataset", "tagged"},
		{TypeIoTSWComputationModel, iswARN("computation-model", "tagged"), "iotsitewise:DescribeComputationModel", "tagged"},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", iswDenied, true, 0, 1},
				{"not found", iswGone, false, 0, 0},
				{"other error", iswBadInput, true, outcomeError, 0},
				{"transient", iswThrottle, true, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, target.op)
					calls := newISWCalls()
					calls.describeErrs[target.id] = c.err

					_, _, err := iswScan(context.Background(), t, st, iswHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					r, ok := iswRowOf(t, st, target.typ, target.arn)
					if ok != c.kept {
						t.Fatalf("row stored = %v; want %v", ok, c.kept)
					}
					if ok {
						if strings.Contains(r.AttributesJSON, "described") || !strings.Contains(r.AttributesJSON, `"Id":"tagged"`) {
							t.Errorf("attributes = %s; want the list element", r.AttributesJSON)
						}
						assertTagsMap(t, r.TagsJSON, map[string]string{"type": target.typ})
					}
					if target.typ == TypeIoTSWAssetModel {
						if listed := calls.assetsOf.distinct(); slices.Contains(listed, "tagged/ALL") != c.kept {
							t.Errorf("assets listed of models %v; want the model only when it is kept", listed)
						}
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", target.op, *warnings, c.wantWarns)
					}
				})
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanIoTSiteWise_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeIoTSWAsset)
	calls := newISWCalls()
	calls.errs[iswARN("asset", "tagged-tagged")] = iswDenied
	handlers := iswHandlers(calls)
	handlers["ListAssets"] = failOnCall(2, iswDenied, handlers["ListAssets"])

	_, _, err := iswScan(context.Background(), t, st, handlers)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := calls.count("ListComputationModels"); n != 0 {
		t.Errorf("ListComputationModels called %d times after the failed write", n)
	}
}

// A listing's failed page keeps the pages read before it, and no listing's
// failure stops the phases after it. A model ListAssets no longer finds is
// skipped silently.
func TestScanIoTSiteWise_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newISWCalls()
	handlers := iswHandlers(calls)
	handlers["ListWorkspaces"] = failOnCall(2, apiErr("ConflictingOperationException", "page 2"), handlers["ListWorkspaces"])
	handlers["ListGateways"] = func(any) (any, error) { return nil, iswBadInput }
	listAssets := handlers["ListAssets"]
	handlers["ListAssets"] = func(in any) (any, error) {
		if sv(in.(*iotsitewise.ListAssetsInput).AssetModelId) == "tagged" {
			return nil, iswGone
		}
		return listAssets(in)
	}

	_, _, err := iswScan(context.Background(), t, st, handlers)
	for _, code := range []string{"ConflictingOperationException", "InvalidRequestException"} {
		if !joinCarriesAPIErrorCode(err, code) {
			t.Errorf("err = %v; want it to carry %s", err, code)
		}
	}
	if joinCarriesAPIErrorCode(err, "ResourceNotFoundException") {
		t.Errorf("err = %v; want the model ListAssets no longer finds skipped", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if _, ok := iswRowOf(t, st, TypeIoTSWWorkspace, iswARN("workspace", "tagged")); !ok {
		t.Error("workspace from the page before the failure missing")
	}
	if _, ok := iswRowOf(t, st, TypeIoTSWWorkspace, iswARN("workspace", "bare")); ok {
		t.Error("workspace from the failed page stored")
	}
	if got := calls.pipelinesOf.distinct(); !slices.Equal(got, []string{"tagged"}) {
		t.Errorf("pipelines listed of workspaces %v; want [tagged]", got)
	}
	for _, later := range []iswRow{
		{TypeIoTSWAsset, iswARN("asset", "bare-bare")},
		{TypeIoTSWDataset, iswARN("dataset", "bare")},
		{TypeIoTSWDashboard, iswARN("dashboard", "bare-bare-bare")},
	} {
		if _, ok := iswRowOf(t, st, later.typ, later.arn); !ok {
			t.Errorf("%s %s missing after the earlier failures", later.typ, later.arn)
		}
	}
}

// A first page refused as a feature not deployed in the region ends the
// listing silently; on a later page the same answer is an error, and the
// pages before it are kept. Per workspace too.
func TestScanIoTSiteWise_FeatureGap(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		failCall int
		wantErr  bool
		typ, arn string
		stored   bool
	}{
		{"computation models first page", "ListComputationModels", 1, false, TypeIoTSWComputationModel, iswARN("computation-model", "tagged"), false},
		{"computation models later page", "ListComputationModels", 2, true, TypeIoTSWComputationModel, iswARN("computation-model", "tagged"), true},
		{"workspace pipelines first page", "ListPipelines", 1, false, TypeIoTSWPipeline, iswARN("workspace", "tagged/pipeline/tagged"), false},
		{"workspace pipelines later page", "ListPipelines", 2, true, TypeIoTSWPipeline, iswARN("workspace", "tagged/pipeline/tagged"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			handlers := iswHandlers(newISWCalls())
			if tc.op == "ListPipelines" {
				// One workspace, so the call count is its pages.
				handlers["ListWorkspaces"] = func(any) (any, error) {
					return &iotsitewise.ListWorkspacesOutput{WorkspaceSummaries: []iswtypes.WorkspaceSummary{{Name: sdkaws.String("tagged"), Arn: sdkaws.String(iswARN("workspace", "tagged"))}}}, nil
				}
			}
			handlers[tc.op] = failOnCall(tc.failCall, iswGap, handlers[tc.op])

			_, _, err := iswScan(context.Background(), t, st, handlers)
			if tc.wantErr {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "InvalidRequestException")
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if _, ok := iswRowOf(t, st, tc.typ, tc.arn); ok != tc.stored {
				t.Errorf("%s stored = %v; want %v", tc.arn, ok, tc.stored)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
		})
	}
}

// An item that cannot be stored is warned about in one warning per op that
// names it; its siblings are stored.
func TestScanIoTSiteWise_UnstorableItemsWarn(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, "iotsitewise:ListGateways")
	handlers := iswHandlers(newISWCalls())
	handlers["ListGateways"] = func(any) (any, error) {
		return &iotsitewise.ListGatewaysOutput{GatewaySummaries: []iswtypes.GatewaySummary{{GatewayId: sdkaws.String("tagged")}, {GatewayName: sdkaws.String("unnamed")}}}, nil
	}

	if _, _, err := iswScan(context.Background(), t, st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := len(rowsByNativeID(t, st, TypeIoTSWGateway)); got != 1 {
		t.Errorf("gateways stored = %d; want 1", got)
	}
	if len(*warnings) != 1 || !strings.HasSuffix((*warnings)[0].Message, "first unnamed") {
		t.Errorf("warnings = %v; want one naming unnamed first", *warnings)
	}
}

// An application whose workspace was not stored this scan is stored
// unlinked.
func TestScanIoTSiteWise_ApplicationOfUnlistedWorkspaceUnlinked(t *testing.T) {
	st := newTestStore(t)
	handlers := iswHandlers(newISWCalls())
	handlers["ListApplications"] = func(any) (any, error) {
		return &iotsitewise.ListApplicationsOutput{Applications: []iswtypes.ApplicationSummary{{
			Id: sdkaws.String("orphan"), WorkspaceName: sdkaws.String("gone"), Arn: sdkaws.String(iswARN("workspace", "gone/application/orphan")),
		}}}, nil
	}

	if _, _, err := iswScan(context.Background(), t, st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	app, ok := iswRowOf(t, st, TypeIoTSWApplication, iswARN("workspace", "gone/application/orphan"))
	if !ok {
		t.Fatal("application missing")
	}
	rels, err := st.RelationshipsTo(app.ID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsTo: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("contains edges into the application = %v; want none", rels)
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanIoTSiteWise_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := iswHandlers(newISWCalls())
	handlers["ListGateways"] = func(any) (any, error) {
		return &iotsitewise.ListGatewaysOutput{GatewaySummaries: []iswtypes.GatewaySummary{{GatewayId: sdkaws.String("tagged")}}, NextToken: sdkaws.String("same")}, nil
	}

	_, _, err := iswScan(context.Background(), t, st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if _, ok := iswRowOf(t, st, TypeIoTSWGateway, iswARN("gateway", "tagged")); !ok {
		t.Error("gateway from the pages read missing")
	}
}

// Once ctx ends no further phase starts, and the stop is reported once,
// naming the phases not run, although the phase in flight saw it in its
// listing and its tag reads.
func TestScanIoTSiteWise_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newISWCalls()
	handlers := iswHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gateways := handlers["ListGateways"]
	handlers["ListGateways"] = func(in any) (any, error) {
		if in.(*iotsitewise.ListGatewaysInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return gateways(in)
	}

	_, _, err := iswScan(ctx, t, st, handlers)
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
	if n := calls.count("ListDatasets"); n != 0 {
		t.Errorf("ListDatasets called %d times after ctx ended", n)
	}
	// Gateways are phase 4 of 14, so 10 never started.
	if !strings.Contains(err.Error(), "iotsitewise: 10 phases not run") {
		t.Errorf("err = %v; want it to say 10 phases not run", err)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
// The monitor host failing to resolve after the api host stored rows is an
// error too, on its first page or a later one.
func TestScanIoTSiteWise_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "api.iotsitewise.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("later api phase after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := iswHandlers(newISWCalls())
		handlers["ListDatasets"] = func(any) (any, error) { return nil, nx }
		total, _, err := iswScan(context.Background(), t, st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		var unresolved *iotSWMonitorUnresolvedError
		if errors.As(err, &unresolved) {
			t.Errorf("err = %v; want an api-host NXDOMAIN not named as the monitor host", err)
		}
		if total == 0 {
			t.Error("total = 0; want the stored rows counted")
		}
	})
	t.Run("monitor host after api rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		calls := newISWCalls()
		handlers := iswHandlers(calls)
		handlers["ListPortals"] = func(any) (any, error) { return nil, nx }
		total, _, err := iswScan(context.Background(), t, st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		var unresolved *iotSWMonitorUnresolvedError
		if !errors.As(err, &unresolved) {
			t.Errorf("err = %v; want an iotSWMonitorUnresolvedError", err)
		}
		if total != 24 {
			t.Errorf("total = %d; want the 24 api-host rows counted", total)
		}
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
		if n := calls.count("ListProjects") + calls.count("ListAccessPolicies"); n != 0 {
			t.Errorf("portal children listed %d times; want none", n)
		}
	})
	t.Run("monitor host on a later page", func(t *testing.T) {
		st := newTestStore(t)
		handlers := iswHandlers(newISWCalls())
		handlers["ListPortals"] = failOnCall(2, nx, handlers["ListPortals"])
		_, _, err := iswScan(context.Background(), t, st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if _, ok := iswRowOf(t, st, TypeIoTSWPortal, iswARN("portal", "tagged")); !ok {
			t.Error("portal from the page before the NXDOMAIN missing")
		}
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanIoTSiteWise(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
}

// A workspace ListPipelines or ListTasks no longer finds is skipped
// silently; its sibling's children are stored.
func TestScanIoTSiteWise_DeletedWorkspaceSkipped(t *testing.T) {
	for _, tc := range []struct{ op, typ, kind string }{
		{"ListPipelines", TypeIoTSWPipeline, "pipeline"},
		{"ListTasks", TypeIoTSWTask, "task"},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			handlers := iswHandlers(newISWCalls())
			list := handlers[tc.op]
			handlers[tc.op] = func(in any) (any, error) {
				if bacField[*string](in, "WorkspaceName") != nil && *bacField[*string](in, "WorkspaceName") == "tagged" {
					return nil, iswGone
				}
				return list(in)
			}
			if _, _, err := iswScan(context.Background(), t, st, handlers); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			if _, ok := iswRowOf(t, st, tc.typ, iswARN("workspace", "bare/"+tc.kind+"/tagged")); !ok {
				t.Errorf("%s of the remaining workspace missing", tc.kind)
			}
		})
	}
}

// An asset model listed without an Id cannot be described: it is stored as
// its list element with its tags, and one warning names it by its ARN.
func TestScanIoTSiteWise_ElementWithoutIDStoredUndescribed(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, "iotsitewise:DescribeAssetModel")
	calls := newISWCalls()
	handlers := iswHandlers(calls)
	arn := iswARN("asset-model", "tagged")
	handlers["ListAssetModels"] = func(any) (any, error) {
		return &iotsitewise.ListAssetModelsOutput{AssetModelSummaries: []iswtypes.AssetModelSummary{{Arn: sdkaws.String(arn), Name: sdkaws.String("no id")}}}, nil
	}

	if _, _, err := iswScan(context.Background(), t, st, handlers); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	r, ok := iswRowOf(t, st, TypeIoTSWAssetModel, arn)
	if !ok {
		t.Fatal("asset model missing")
	}
	if strings.Contains(r.AttributesJSON, "described") {
		t.Errorf("attributes = %s; want the list element", r.AttributesJSON)
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeIoTSWAssetModel})
	if n := calls.count("DescribeAssetModel"); n != 0 {
		t.Errorf("DescribeAssetModel called %d times; want 0", n)
	}
	if len(*warnings) != 1 || !strings.HasSuffix((*warnings)[0].Message, "first "+arn) {
		t.Errorf("warnings = %v; want one naming %s first", *warnings, arn)
	}
}

// An asset model listed without an Id whose tag read finds it deleted is
// dropped, and no warning claims it was stored.
func TestScanIoTSiteWise_ElementWithoutIDDroppedByTagRead(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newISWCalls()
	handlers := iswHandlers(calls)
	arn := iswARN("asset-model", "tagged")
	calls.errs[arn] = iswGone
	handlers["ListAssetModels"] = func(any) (any, error) {
		return &iotsitewise.ListAssetModelsOutput{AssetModelSummaries: []iswtypes.AssetModelSummary{{Arn: sdkaws.String(arn)}}}, nil
	}

	if _, _, err := iswScan(context.Background(), t, st, handlers); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if _, ok := iswRowOf(t, st, TypeIoTSWAssetModel, arn); ok {
		t.Error("asset model the tag read no longer finds stored")
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
}
