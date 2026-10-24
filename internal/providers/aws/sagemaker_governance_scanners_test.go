package aws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"strconv"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// Shared helpers for the per-parent child phases built on scanSageMakerChildren
// (governance, misc, edge and monitoring families).

// smPage serves pages[token] where the token is the page index, so a stub
// paginates the way the SDK paginator expects.
func smPage[T any](pages [][]T, token *string) ([]T, *string, error) {
	i := 0
	if token != nil {
		var err error
		if i, err = strconv.Atoi(*token); err != nil {
			return nil, nil, fmt.Errorf("stub page token %q: %w", *token, err)
		}
	}
	if i >= len(pages) {
		return nil, nil, nil
	}
	var next *string
	if i+1 < len(pages) {
		next = sdkaws.String(strconv.Itoa(i + 1))
	}
	return pages[i], next, nil
}

// smStoredRows returns rtype rows keyed by NativeID, failing on any row outside
// testRegion.
func smStoredRows(t *testing.T, st *store.Store, rtype string) map[string]store.Resource {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{Types: []string{rtype}, Limit: util.AllResources})
	if err != nil {
		t.Fatalf("list %s: %v", rtype, err)
	}
	out := make(map[string]store.Resource, len(rows))
	for _, r := range rows {
		if sv(r.Region) != testRegion {
			t.Errorf("%s %s region = %q; want %q", rtype, r.NativeID, sv(r.Region), testRegion)
		}
		out[r.NativeID] = r
	}
	return out
}

func assertSMIDs(t *testing.T, st *store.Store, rtype string, want ...string) {
	t.Helper()
	var got []string
	for id := range smStoredRows(t, st, rtype) {
		got = append(got, id)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("stored %s NativeIDs = %v; want %v", rtype, got, want)
	}
}

// assertSMStatus checks a row's Status; want "" means Status must be unset.
func assertSMStatus(t *testing.T, st *store.Store, rtype, nativeID, want string) {
	t.Helper()
	r, ok := smStoredRows(t, st, rtype)[nativeID]
	if !ok {
		t.Fatalf("%s %s not stored", rtype, nativeID)
	}
	if want == "" {
		if r.Status != nil {
			t.Errorf("%s %s status = %q; want unset", rtype, nativeID, *r.Status)
		}
		return
	}
	if sv(r.Status) != want {
		t.Errorf("%s %s status = %q; want %q", rtype, nativeID, sv(r.Status), want)
	}
}

func assertSMContains(t *testing.T, st *store.Store, parentID, childNativeID string) {
	t.Helper()
	rels, err := st.RelationshipsFrom(parentID, store.RelContains)
	if err != nil {
		t.Fatalf("relationships from %s: %v", parentID, err)
	}
	assertRelationship(t, rels, parentID, store.ResourceID("aws", testAccountID, childNativeID), store.RelContains)
}

func countSMWarnings(st *store.Store) *int {
	n := 0
	st.OnWarn = func(store.ScanWarning) { n++ }
	return &n
}

// --- ultraservers ----------------------------------------------------------

type stubSMUltraServers struct {
	sagemakerGovernanceAPI
	pages map[string][][]smtypes.UltraServer
	errs  map[string]error
}

func (s *stubSMUltraServers) ListUltraServersByReservedCapacity(_ context.Context, in *sagemaker.ListUltraServersByReservedCapacityInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListUltraServersByReservedCapacityOutput, error) {
	if err := s.errs[*in.ReservedCapacityArn]; err != nil {
		return nil, err
	}
	items, next, err := smPage(s.pages[*in.ReservedCapacityArn], in.NextToken)
	if err != nil {
		return nil, err
	}
	return &sagemaker.ListUltraServersByReservedCapacityOutput{UltraServers: items, NextToken: next}, nil
}

func smUltraCapacity(arn string) smtypes.ReservedCapacitySummary {
	return smtypes.ReservedCapacitySummary{ReservedCapacityArn: sdkaws.String(arn), ReservedCapacityType: smtypes.ReservedCapacityTypeUltraserver}
}

func smPlanAttrs(capacities ...smtypes.ReservedCapacitySummary) string {
	return mustJSON(smtypes.TrainingPlanSummary{TrainingPlanArn: sdkaws.String("plan"), ReservedCapacitySummaries: capacities})
}

func smUltraServer(id string) smtypes.UltraServer {
	return smtypes.UltraServer{UltraServerId: sdkaws.String(id), HealthStatus: smtypes.UltraServerHealthStatusOk}
}

func smRCARN(name string) string {
	return "arn:aws:sagemaker:us-east-1:123456789012:reserved-capacity/" + name
}

func smPlanARN(name string) string {
	return "arn:aws:sagemaker:us-east-1:123456789012:training-plan/" + name
}

func TestScanSageMakerUltraServers_PaginatesEachReservedCapacity(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "sagemaker:ListUltraServersByReservedCapacity")
	rc1, rc2, rcGone, rc3, rcInstance := smRCARN("rc1"), smRCARN("rc2"), smRCARN("expired"), smRCARN("rc3"), smRCARN("instances")
	p1ID := upsertTestResourceNamed(t, st, TypeSageMakerTrainingPlan, smPlanARN("p1"), testRegion,
		smPlanAttrs(smUltraCapacity(rc1), smUltraCapacity(rcGone), smUltraCapacity(rc2),
			smtypes.ReservedCapacitySummary{ReservedCapacityArn: sdkaws.String(rcInstance), ReservedCapacityType: smtypes.ReservedCapacityTypeInstance}), "p1")
	p2ID := upsertTestResourceNamed(t, st, TypeSageMakerTrainingPlan, smPlanARN("p2"), testRegion, smPlanAttrs(smUltraCapacity(rc3)), "p2")
	stub := &stubSMUltraServers{
		pages: map[string][][]smtypes.UltraServer{
			rc1:        {{smUltraServer("us-1")}, {smUltraServer("us-2"), {HealthStatus: smtypes.UltraServerHealthStatusOk}}},
			rc2:        {{smUltraServer("us-3")}},
			rc3:        {{{UltraServerId: sdkaws.String("us-4")}}},
			rcInstance: {{smUltraServer("must-not-be-listed")}},
		},
		errs: map[string]error{rcGone: apiErr("ResourceNotFound", "reserved capacity not found")},
	}
	total, _, err := scanSageMakerUltraServers(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d; want 4", total)
	}
	assertSMIDs(t, st, TypeSageMakerUltraServer,
		rc1+"/ultraserver/us-1", rc1+"/ultraserver/us-2", rc2+"/ultraserver/us-3", rc3+"/ultraserver/us-4")
	assertSMStatus(t, st, TypeSageMakerUltraServer, rc1+"/ultraserver/us-1", string(smtypes.UltraServerHealthStatusOk))
	assertSMStatus(t, st, TypeSageMakerUltraServer, rc3+"/ultraserver/us-4", "")
	assertSMContains(t, st, p1ID, rc2+"/ultraserver/us-3")
	assertSMContains(t, st, p2ID, rc3+"/ultraserver/us-4")
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1 (the UltraServer without an id)", *warns)
	}
}

func TestScanSageMakerUltraServers_NoUltraServers(t *testing.T) {
	st := newTestStore(t)
	upsertTestResourceNamed(t, st, TypeSageMakerTrainingPlan, smPlanARN("p1"), testRegion, smPlanAttrs(smUltraCapacity(smRCARN("rc"))), "p1")
	total, _, err := scanSageMakerUltraServers(context.Background(), &stubSMUltraServers{}, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Fatalf("scan = (%d, %v); want (0, nil)", total, err)
	}
}

// A deny on one reserved capacity must not hide its siblings in the same plan,
// and two denies in one phase warn once.
func TestScanSageMakerUltraServers_DeniedCapacityKeepsSiblings(t *testing.T) {
	st := newTestStore(t)
	warnings := countSMWarnings(st)
	denied1, denied2, open := smRCARN("denied1"), smRCARN("denied2"), smRCARN("open")
	upsertTestResourceNamed(t, st, TypeSageMakerTrainingPlan, smPlanARN("p1"), testRegion,
		smPlanAttrs(smUltraCapacity(denied1), smUltraCapacity(open), smUltraCapacity(denied2)), "p1")
	stub := &stubSMUltraServers{
		pages: map[string][][]smtypes.UltraServer{open: {{smUltraServer("us-1")}}},
		errs: map[string]error{
			denied1: apiErr("AccessDeniedException", "denied"),
			denied2: apiErr("AccessDeniedException", "denied"),
		},
	}
	if _, _, err := scanSageMakerUltraServers(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertSMIDs(t, st, TypeSageMakerUltraServer, open+"/ultraserver/us-1")
	if *warnings != 1 {
		t.Errorf("warnings = %d; want 1", *warnings)
	}
}

// TestScanSageMakerGovernance_TrainingPlanThenUltraServers runs the whole family
// against the real SDK client: the UltraServer phase must see the training plan
// the earlier phase stored in the same scan.
func TestScanSageMakerGovernance_TrainingPlanThenUltraServers(t *testing.T) {
	st := newTestStore(t)
	rc := smRCARN("rc1")
	planARN := smPlanARN("p1")
	empty := func(out any) []stubCall { return []stubCall{{Output: out}} }
	stub := stubResponses(t, map[string][]stubCall{
		"ListActions":                 empty(&sagemaker.ListActionsOutput{}),
		"ListContexts":                empty(&sagemaker.ListContextsOutput{}),
		"ListExperiments":             empty(&sagemaker.ListExperimentsOutput{}),
		"ListTrials":                  empty(&sagemaker.ListTrialsOutput{}),
		"ListHubs":                    empty(&sagemaker.ListHubsOutput{}),
		"ListHumanTaskUis":            empty(&sagemaker.ListHumanTaskUisOutput{}),
		"ListFlowDefinitions":         empty(&sagemaker.ListFlowDefinitionsOutput{}),
		"ListLineageGroups":           empty(&sagemaker.ListLineageGroupsOutput{}),
		"ListAlgorithms":              empty(&sagemaker.ListAlgorithmsOutput{}),
		"ListWorkforces":              empty(&sagemaker.ListWorkforcesOutput{}),
		"ListComputeQuotas":           empty(&sagemaker.ListComputeQuotasOutput{}),
		"ListClusterSchedulerConfigs": empty(&sagemaker.ListClusterSchedulerConfigsOutput{}),
		"ListEdgeDeploymentPlans":     empty(&sagemaker.ListEdgeDeploymentPlansOutput{}),
		"ListAIWorkloadConfigs":       empty(&sagemaker.ListAIWorkloadConfigsOutput{}),
		"ListMlflowApps":              empty(&sagemaker.ListMlflowAppsOutput{}),
		"ListTags":                    empty(&sagemaker.ListTagsOutput{}),
		"ListTrainingPlans": empty(&sagemaker.ListTrainingPlansOutput{TrainingPlanSummaries: []smtypes.TrainingPlanSummary{{
			TrainingPlanArn: sdkaws.String(planARN), TrainingPlanName: sdkaws.String("p1"),
			ReservedCapacitySummaries: []smtypes.ReservedCapacitySummary{smUltraCapacity(rc)},
		}}}),
		"ListUltraServersByReservedCapacity": empty(&sagemaker.ListUltraServersByReservedCapacityOutput{UltraServers: []smtypes.UltraServer{smUltraServer("us-1")}}),
	})
	client := newSageMakerClient(cfgWithStub(stub, testRegion), testRegion)
	if _, _, err := scanSageMakerGovernance(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertSMIDs(t, st, TypeSageMakerUltraServer, rc+"/ultraserver/us-1")
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, planARN), rc+"/ultraserver/us-1")
}

// Every governance type whose tags are read has them read as the mode says;
// lineage groups, whose tags are not read (scanSageMakerLineageGroups), are
// never asked for.
func TestScanSageMakerGovernance_StoresTags(t *testing.T) {
	for _, mode := range smTagModes {
		t.Run(mode.String(), func(t *testing.T) { testScanSageMakerGovernanceTags(t, mode) })
	}
}

func testScanSageMakerGovernanceTags(t *testing.T, mode smTagMode) {
	st := newTestStore(t)
	arn := func(kind string) string { return smTestARN(kind, "x") }
	name := sdkaws.String("x")
	hubContentARN := smTestARN("hub-content", "h1/Model/x")
	taggedARNs := []string{
		arn("action"), arn("context"), arn("experiment"), arn("experiment-trial"), arn("hub"), hubContentARN,
		arn("human-task-ui"), arn("flow-definition"), arn("algorithm"), arn("compute-quota"),
		arn("cluster-scheduler-config"), arn("edge-deployment-plan"), arn("ai-workload-config"), arn("mlflow-app"),
		arn("training-plan"), arn("workforce"),
	}
	untaggedARNs := []string{arn("lineage-group")}
	tags := newSMTags(mode, taggedARNs...)
	out := func(v any) func(any) (any, error) { return func(any) (any, error) { return v, nil } }
	client := newSageMakerClient(opStubConfig(map[string]func(in any) (any, error){
		"ListActions": out(&sagemaker.ListActionsOutput{ActionSummaries: []smtypes.ActionSummary{{ActionArn: sdkaws.String(arn("action")), ActionName: name}}}),
		"ListContexts": out(&sagemaker.ListContextsOutput{ContextSummaries: []smtypes.ContextSummary{{
			ContextArn: sdkaws.String(arn("context")), ContextName: name,
		}}}),
		"ListExperiments": out(&sagemaker.ListExperimentsOutput{ExperimentSummaries: []smtypes.ExperimentSummary{{
			ExperimentArn: sdkaws.String(arn("experiment")), ExperimentName: name,
		}}}),
		"ListTrials":    out(&sagemaker.ListTrialsOutput{TrialSummaries: []smtypes.TrialSummary{{TrialName: name}}}),
		"DescribeTrial": out(&sagemaker.DescribeTrialOutput{TrialArn: sdkaws.String(arn("experiment-trial")), TrialName: name}),
		"ListHubs":      out(&sagemaker.ListHubsOutput{HubSummaries: []smtypes.HubInfo{{HubArn: sdkaws.String(arn("hub")), HubName: sdkaws.String("h1")}}}),
		"ListHubContents": func(in any) (any, error) {
			if in.(*sagemaker.ListHubContentsInput).HubContentType != smtypes.HubContentTypeModel {
				return &sagemaker.ListHubContentsOutput{}, nil
			}
			return &sagemaker.ListHubContentsOutput{HubContentSummaries: []smtypes.HubContentInfo{{HubContentArn: sdkaws.String(hubContentARN), HubContentName: name}}}, nil
		},
		"ListHumanTaskUis": out(&sagemaker.ListHumanTaskUisOutput{HumanTaskUiSummaries: []smtypes.HumanTaskUiSummary{{
			HumanTaskUiArn: sdkaws.String(arn("human-task-ui")), HumanTaskUiName: name,
		}}}),
		"ListFlowDefinitions": out(&sagemaker.ListFlowDefinitionsOutput{FlowDefinitionSummaries: []smtypes.FlowDefinitionSummary{{
			FlowDefinitionArn: sdkaws.String(arn("flow-definition")), FlowDefinitionName: name,
		}}}),
		"ListLineageGroups": out(&sagemaker.ListLineageGroupsOutput{LineageGroupSummaries: []smtypes.LineageGroupSummary{{
			LineageGroupArn: sdkaws.String(arn("lineage-group")), LineageGroupName: name,
		}}}),
		"ListAlgorithms": out(&sagemaker.ListAlgorithmsOutput{AlgorithmSummaryList: []smtypes.AlgorithmSummary{{
			AlgorithmArn: sdkaws.String(arn("algorithm")), AlgorithmName: name,
		}}}),
		"ListWorkforces": out(&sagemaker.ListWorkforcesOutput{Workforces: []smtypes.Workforce{{WorkforceArn: sdkaws.String(arn("workforce")), WorkforceName: name}}}),
		"ListComputeQuotas": out(&sagemaker.ListComputeQuotasOutput{ComputeQuotaSummaries: []smtypes.ComputeQuotaSummary{{
			ComputeQuotaArn: sdkaws.String(arn("compute-quota")), Name: name,
		}}}),
		"ListClusterSchedulerConfigs": out(&sagemaker.ListClusterSchedulerConfigsOutput{ClusterSchedulerConfigSummaries: []smtypes.ClusterSchedulerConfigSummary{{
			ClusterSchedulerConfigArn: sdkaws.String(arn("cluster-scheduler-config")), Name: name,
		}}}),
		"ListEdgeDeploymentPlans": out(&sagemaker.ListEdgeDeploymentPlansOutput{EdgeDeploymentPlanSummaries: []smtypes.EdgeDeploymentPlanSummary{{
			EdgeDeploymentPlanArn: sdkaws.String(arn("edge-deployment-plan")), EdgeDeploymentPlanName: name,
		}}}),
		"ListAIWorkloadConfigs": out(&sagemaker.ListAIWorkloadConfigsOutput{AIWorkloadConfigs: []smtypes.AIWorkloadConfigSummary{{
			AIWorkloadConfigArn: sdkaws.String(arn("ai-workload-config")), AIWorkloadConfigName: name,
		}}}),
		"ListMlflowApps": out(&sagemaker.ListMlflowAppsOutput{Summaries: []smtypes.MlflowAppSummary{{Arn: sdkaws.String(arn("mlflow-app")), Name: name}}}),
		"ListTrainingPlans": out(&sagemaker.ListTrainingPlansOutput{TrainingPlanSummaries: []smtypes.TrainingPlanSummary{{
			TrainingPlanArn: sdkaws.String(arn("training-plan")), TrainingPlanName: name,
		}}}),
		"ListTags": func(in any) (any, error) { return tags.ListTags(context.Background(), in.(*sagemaker.ListTagsInput)) },
	}, apiErr("AccessDeniedException", "op not stubbed")), testRegion)

	total, _, err := scanSageMakerGovernance(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	checkSMScanErr(t, err, mode)
	if want := len(taggedARNs) + len(untaggedARNs); total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	assertSMTags(t, st, tags, mode, taggedARNs...)
	for _, a := range untaggedARNs {
		r, err := st.GetResource(store.ResourceID("aws", testAccountID, a))
		if err != nil {
			t.Fatalf("%s not stored: %v", a, err)
		}
		if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %s; want nil", a, *r.TagsJSON)
		}
	}
}

// --- shared list and child-list bodies ---------------------------------------

// A failed child listing keeps every other listing's rows and its own rows read
// before it failed: a vanished parent is skipped silently, a denied one is
// warned about after the write, a throttled one is an error demoted under op
// beside a hard one, and the hard one is returned.
func TestScanSageMakerChildren_FailedListingKeepsOthers(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	acct := newTestAccount(testAccountID)
	const op = "sagemaker:ListAliases"
	ids := map[string]string{}
	for _, name := range []string{"bad", "denied", "gone", "slow"} {
		ids[name] = upsertTestResourceNamed(t, st, TypeSageMakerImage, smImageARN(name), testRegion, "{}", name)
	}
	errs := map[string]error{
		"bad":    apiErr("ValidationException", "bad"),
		"denied": apiErr("AccessDeniedException", "denied"),
		"gone":   apiErr("ResourceNotFound", "gone"),
		"slow":   apiErr("ThrottlingException", "Rate exceeded"),
	}
	alias := func(image string) string { return smImageARN(image) + "/alias/a" }
	_, _, err := runSageMakerFamilies(context.Background(), st, acct, testRegion, func(ctx context.Context) (int, int, error) {
		return scanSageMakerChildren(ctx, acct, testRegion, st, testScanID, TypeSageMakerImage, op, []string{"ResourceNotFound"}, sagemakerChildSkips{},
			func(_ context.Context, image store.Resource) []sagemakerChildList[*store.Resource] {
				name := sv(image.Name)
				l := sagemakerChildList[*store.Resource]{key: name, err: errs[name]}
				if name == "bad" || name == "slow" {
					l.items = []*store.Resource{{
						Provider: "aws", AccountID: testAccountID, Type: TypeSageMakerAlias, NativeID: alias(name),
						Region: sdkaws.String(testRegion), AttributesJSON: "{}", DiscoveredBy: testScanID,
					}}
				}
				return []sagemakerChildList[*store.Resource]{l}
			})
	})
	assertAPICode(t, err, "ValidationException")
	if isAPIErrorCode(err, "ResourceNotFound", "AccessDeniedException", "ThrottlingException") {
		t.Errorf("err = %v; want only the hard listing error", err)
	}
	assertSMIDs(t, st, TypeSageMakerAlias, alias("bad"), alias("slow"))
	assertSMContains(t, st, ids["bad"], alias("bad"))
	assertSMContains(t, st, ids["slow"], alias("slow"))
	if got, want := smWarnServices(*warns), map[string]int{op: 2}; !maps.Equal(got, want) {
		t.Errorf("warnings by op = %v; want %v", got, want)
	}
}

// A summary listing keeps the pages before a failed one, and warns about a
// summary without an ARN instead of dropping it silently.
func TestScanSagemakerSummaryList_FailedPageKeepsEarlierPages(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "sagemaker:ListActions")
	actionARN := smTestARN("action", "a")
	tags := newSMTags(smUntagged)
	client := newSageMakerClient(opStubConfig(map[string]func(in any) (any, error){
		"ListActions": func(in any) (any, error) {
			if in.(*sagemaker.ListActionsInput).NextToken != nil {
				return nil, apiErr("ValidationException", "bad")
			}
			return &sagemaker.ListActionsOutput{
				ActionSummaries: []smtypes.ActionSummary{{ActionArn: sdkaws.String(actionARN)}, {ActionName: sdkaws.String("no-arn")}},
				NextToken:       sdkaws.String("1"),
			}, nil
		},
		"ListTags": func(in any) (any, error) { return tags.ListTags(context.Background(), in.(*sagemaker.ListTagsInput)) },
	}, apiErr("AccessDeniedException", "op not stubbed")), testRegion)
	total, _, err := scanSageMakerActions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	assertAPICode(t, err, "ValidationException")
	if total != 1 {
		t.Errorf("total = %d; want 1", total)
	}
	assertSMIDs(t, st, TypeSageMakerAction, actionARN)
	if *warns != 1 {
		t.Errorf("sagemaker:ListActions warnings = %d; want 1 (the summary without an ARN)", *warns)
	}
}

// --- hubs ----------------------------------------------------------------------

// ListHubs repeating its NextToken stops the listing with an error, and the
// hubs of the pages read are stored.
func TestScanSageMakerHubs_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	hubA, hubB := smTestARN("hub", "a"), smTestARN("hub", "b")
	tags := newSMTags(smUntagged)
	client := newSageMakerClient(opStubConfig(map[string]func(in any) (any, error){
		"ListHubs": func(in any) (any, error) {
			hub := hubA
			if in.(*sagemaker.ListHubsInput).NextToken != nil {
				hub = hubB
			}
			return &sagemaker.ListHubsOutput{HubSummaries: []smtypes.HubInfo{{HubArn: sdkaws.String(hub)}}, NextToken: sdkaws.String("t")}, nil
		},
		"ListTags": func(in any) (any, error) { return tags.ListTags(context.Background(), in.(*sagemaker.ListTagsInput)) },
	}, apiErr("AccessDeniedException", "op not stubbed")), testRegion)
	_, _, err := scanSageMakerHubs(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Errorf("err = %v; want errRepeatedNextToken", err)
	}
	assertSMIDs(t, st, TypeSageMakerHub, hubA, hubB)
}

// Hub contents are listed only for the hubs this scan stored in this region.
// A listing that repeats its NextToken stops with an error and keeps its
// pages, a denied one is warned about, and a vanished hub is skipped
// silently.
func TestScanSageMakerHubContents_ListsThisScansHubs(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	upsertTestResourceNamed(t, st, TypeSageMakerHub, smTestARN("hub", "h1"), testRegion, "{}", "h1")
	upsertTestResourceNamed(t, st, TypeSageMakerHub, "arn:aws:sagemaker:us-west-2:123456789012:hub/elsewhere", "us-west-2", "{}", "elsewhere")
	insertTestScan(t, st, "an-earlier-scan", "completed")
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: testAccountID, Type: TypeSageMakerHub, NativeID: smTestARN("hub", "old"),
		Name: sdkaws.String("old"), Region: sdkaws.String(testRegion), AttributesJSON: "{}", DiscoveredBy: "an-earlier-scan",
	}); err != nil {
		t.Fatal(err)
	}
	c1, c2 := smTestARN("hub-content", "h1/Model/c1"), smTestARN("hub-content", "h1/Model/c2")
	var (
		mu    sync.Mutex
		asked []string
	)
	tags := newSMTags(smUntagged)
	client := newSageMakerClient(opStubConfig(map[string]func(in any) (any, error){
		"ListHubContents": func(in any) (any, error) {
			req := in.(*sagemaker.ListHubContentsInput)
			if got := sdkaws.ToInt32(req.MaxResults); got != sagemakerListPageLimit {
				return nil, fmt.Errorf("stub ListHubContents MaxResults = %d", got)
			}
			mu.Lock()
			asked = append(asked, sdkaws.ToString(req.HubName))
			mu.Unlock()
			switch req.HubContentType {
			case smtypes.HubContentTypeNotebook:
				return nil, apiErr("AccessDeniedException", "denied")
			case smtypes.HubContentTypeModelReference:
				return nil, apiErr("ResourceNotFound", "gone")
			}
			content := c1
			if req.NextToken != nil {
				content = c2
			}
			return &sagemaker.ListHubContentsOutput{
				HubContentSummaries: []smtypes.HubContentInfo{{HubContentArn: sdkaws.String(content)}},
				NextToken:           sdkaws.String("t"),
			}, nil
		},
		"ListTags": func(in any) (any, error) { return tags.ListTags(context.Background(), in.(*sagemaker.ListTagsInput)) },
	}, apiErr("AccessDeniedException", "op not stubbed")), testRegion)
	_, _, err := scanSageMakerHubContents(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Errorf("err = %v; want errRepeatedNextToken", err)
	}
	if isAPIErrorCode(err, "AccessDeniedException", "ResourceNotFound") {
		t.Errorf("err = %v; want neither the denial nor the vanished hub", err)
	}
	if got := slices.Compact(slices.Sorted(slices.Values(asked))); !slices.Equal(got, []string{"h1"}) {
		t.Errorf("hubs listed = %v; want [h1]", got)
	}
	assertSMIDs(t, st, TypeSageMakerHubContent, c1, c2)
	if got, want := smWarnServices(*warns), map[string]int{"sagemaker:ListHubContents": 1}; !maps.Equal(got, want) {
		t.Errorf("warnings by op = %v; want %v", got, want)
	}
}

// smAliasListing lists for scanSageMakerChildren one alias row of each image,
// keyed by image name, plus an alias skipped as empty, except that an image
// in errs fails with its error and an image without a name is nameless.
func smAliasListing(errs map[string]error) func(context.Context, store.Resource) []sagemakerChildList[*store.Resource] {
	return func(_ context.Context, image store.Resource) []sagemakerChildList[*store.Resource] {
		name := sv(image.Name)
		if name == "" {
			return []sagemakerChildList[*store.Resource]{{key: image.NativeID, nameless: true}}
		}
		return []sagemakerChildList[*store.Resource]{{
			key: name, err: errs[name], skipped: []string{name + " #2"},
			items: []*store.Resource{{
				Provider: "aws", AccountID: testAccountID, Type: TypeSageMakerAlias, NativeID: image.NativeID + "/alias/a",
				Region: sdkaws.String(testRegion), AttributesJSON: "{}", DiscoveredBy: testScanID,
			}},
		}}
	}
}

// A child listing's warnings (a denied listing, items skipped as empty, a
// nameless parent) are sent only after the rows are written.
func TestScanSageMakerChildren_WarnsOnlyAfterWrite(t *testing.T) {
	for _, writeFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("writeFails=%v", writeFails), func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			const op = "sagemaker:ListAliases"
			for _, name := range []string{"ok", "denied", ""} {
				upsertTestResourceNamed(t, st, TypeSageMakerImage, smImageARN("img-"+name), testRegion, "{}", name)
			}
			if writeFails {
				refuseStoreWrites(t, st, TypeSageMakerAlias)
			}
			_, _, err := scanSageMakerChildren(context.Background(), newTestAccount(testAccountID), testRegion, st, testScanID,
				TypeSageMakerImage, op, nil, sagemakerChildSkips{emptyWhy: "aliases are empty", namelessWhat: "stored image(s)"},
				smAliasListing(map[string]error{"denied": apiErr("AccessDeniedException", "denied")}))
			want := map[string]int{op: 3}
			if writeFails {
				if err == nil {
					t.Fatal("err = nil; want the refused write")
				}
				want = map[string]int{}
			} else if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := smWarnServices(*warns); !maps.Equal(got, want) {
				t.Errorf("warnings by op = %v; want %v", got, want)
			}
		})
	}
}

// A child listing's NXDOMAIN, after the parents' phase reached the endpoint,
// is a plain error, and the other listings' rows are kept.
func TestScanSageMakerChildren_LateNXDOMAINIsAnError(t *testing.T) {
	st := newTestStore(t)
	for _, name := range []string{"ok", "lost"} {
		upsertTestResourceNamed(t, st, TypeSageMakerImage, smImageARN(name), testRegion, "{}", name)
	}
	nxdomain := &net.DNSError{Err: "no such host", Name: "api.sagemaker.us-east-1.amazonaws.com", IsNotFound: true}
	_, _, err := scanSageMakerChildren(context.Background(), newTestAccount(testAccountID), testRegion, st, testScanID,
		TypeSageMakerImage, "sagemaker:ListAliases", nil, sagemakerChildSkips{}, smAliasListing(map[string]error{"lost": nxdomain}))
	if err == nil || isDNSNotFound(err) {
		t.Errorf("err = %v; want a plain error, not an NXDOMAIN", err)
	}
	assertSMIDs(t, st, TypeSageMakerAlias, smImageARN("lost")+"/alias/a", smImageARN("ok")+"/alias/a")
}

// Child listings cut short by ctx are one error member, however many were in
// flight.
func TestScanSageMakerChildren_StopIsOneMember(t *testing.T) {
	st := newTestStore(t)
	names := []string{"a", "b", "c"}
	for _, name := range names {
		upsertTestResourceNamed(t, st, TypeSageMakerImage, smImageARN(name), testRegion, "{}", name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var started sync.WaitGroup
	started.Add(len(names))
	_, _, err := scanSageMakerChildren(ctx, newTestAccount(testAccountID), testRegion, st, testScanID,
		TypeSageMakerImage, "sagemaker:ListAliases", nil, sagemakerChildSkips{},
		func(ctx context.Context, image store.Resource) []sagemakerChildList[*store.Resource] {
			started.Done()
			started.Wait()
			cancel()
			return []sagemakerChildList[*store.Resource]{{key: sv(image.Name), err: ctx.Err()}}
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	if n := len(smLeaves(err)); n != 1 {
		t.Errorf("err has %d members; want 1: %v", n, err)
	}
}

// A hub-content listing still throttled after the SDK's retries is returned,
// and the other listings' rows are kept.
func TestScanSageMakerHubContents_ThrottledListingReturned(t *testing.T) {
	st := newTestStore(t)
	upsertTestResourceNamed(t, st, TypeSageMakerHub, smTestARN("hub", "h1"), testRegion, "{}", "h1")
	model := smTestARN("hub-content", "h1/Model/m")
	client := newSageMakerClient(opStubConfig(map[string]func(in any) (any, error){
		"ListHubContents": func(in any) (any, error) {
			if in.(*sagemaker.ListHubContentsInput).HubContentType != smtypes.HubContentTypeModel {
				return nil, apiErr("ThrottlingException", "Rate exceeded")
			}
			return &sagemaker.ListHubContentsOutput{HubContentSummaries: []smtypes.HubContentInfo{{HubContentArn: sdkaws.String(model)}}}, nil
		},
		"ListTags": func(any) (any, error) { return &sagemaker.ListTagsOutput{}, nil },
	}, apiErr("AccessDeniedException", "op not stubbed")), testRegion)
	_, _, err := scanSageMakerHubContents(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err == nil || !everyMember(err, func(m error) bool { return isAPIErrorCode(m, "ThrottlingException") }) {
		t.Errorf("err = %v; want only the throttled listings", err)
	}
	assertSMIDs(t, st, TypeSageMakerHubContent, model)
}

// Every content type of a hub is listed; a content without an ARN and a hub
// without a name are each warned about instead of dropped silently.
func TestScanSageMakerHubContents_EveryTypeAndSkipsWarned(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	upsertTestResourceNamed(t, st, TypeSageMakerHub, smTestARN("hub", "h1"), testRegion, "{}", "h1")
	upsertTestResourceNamed(t, st, TypeSageMakerHub, smTestARN("hub", "nameless"), testRegion, "{}", "")
	arn := func(ct smtypes.HubContentType) string { return smTestARN("hub-content", "h1/"+string(ct)+"/c") }
	client := newSageMakerClient(opStubConfig(map[string]func(in any) (any, error){
		"ListHubContents": func(in any) (any, error) {
			ct := in.(*sagemaker.ListHubContentsInput).HubContentType
			contents := []smtypes.HubContentInfo{{HubContentArn: sdkaws.String(arn(ct))}}
			if ct == smtypes.HubContentTypeModel {
				contents = append(contents, smtypes.HubContentInfo{HubContentName: sdkaws.String("no-arn")})
			}
			return &sagemaker.ListHubContentsOutput{HubContentSummaries: contents}, nil
		},
		"ListTags": func(any) (any, error) { return &sagemaker.ListTagsOutput{}, nil },
	}, apiErr("AccessDeniedException", "op not stubbed")), testRegion)
	if _, _, err := scanSageMakerHubContents(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertSMIDs(t, st, TypeSageMakerHubContent,
		arn(smtypes.HubContentTypeModel), arn(smtypes.HubContentTypeModelReference), arn(smtypes.HubContentTypeNotebook))
	if got, want := smWarnServices(*warns), map[string]int{"sagemaker:ListHubContents": 2}; !maps.Equal(got, want) {
		t.Errorf("warnings by op = %v; want %v", got, want)
	}
}

// A failed write of the hub contents is returned.
func TestScanSageMakerHubContents_FailedWriteReturned(t *testing.T) {
	st := newTestStore(t)
	upsertTestResourceNamed(t, st, TypeSageMakerHub, smTestARN("hub", "h1"), testRegion, "{}", "h1")
	refuseStoreWrites(t, st, TypeSageMakerHubContent)
	client := newSageMakerClient(opStubConfig(map[string]func(in any) (any, error){
		"ListHubContents": func(in any) (any, error) {
			ct := in.(*sagemaker.ListHubContentsInput).HubContentType
			return &sagemaker.ListHubContentsOutput{HubContentSummaries: []smtypes.HubContentInfo{
				{HubContentArn: sdkaws.String(smTestARN("hub-content", "h1/"+string(ct)+"/c"))},
			}}, nil
		},
		"ListTags": func(any) (any, error) { return &sagemaker.ListTagsOutput{}, nil },
	}, apiErr("AccessDeniedException", "op not stubbed")), testRegion)
	if _, _, err := scanSageMakerHubContents(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID); err == nil {
		t.Error("err = nil; want the refused write")
	}
}

// A failed query for the parents is returned, not read as no parents.
func TestScanSageMakerChildren_ParentQueryErrorReturned(t *testing.T) {
	st, closeStore := openClosingStore(t)
	closeStore()
	_, _, err := scanSageMakerChildren(context.Background(), newTestAccount(testAccountID), testRegion, st, testScanID,
		TypeSageMakerImage, "sagemaker:ListAliases", nil, sagemakerChildSkips{}, smAliasListing(nil))
	if err == nil {
		t.Error("err = nil; want the failed parent query")
	}
}
