package aws

import (
	"context"
	"fmt"
	"slices"
	"strconv"
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
		"ListTrainingPlans": empty(&sagemaker.ListTrainingPlansOutput{TrainingPlanSummaries: []smtypes.TrainingPlanSummary{{
			TrainingPlanArn: sdkaws.String(planARN), TrainingPlanName: sdkaws.String("p1"),
			ReservedCapacitySummaries: []smtypes.ReservedCapacitySummary{smUltraCapacity(rc)},
		}}}),
		"ListUltraServersByReservedCapacity": empty(&sagemaker.ListUltraServersByReservedCapacityOutput{UltraServers: []smtypes.UltraServer{smUltraServer("us-1")}}),
	})
	client := sagemaker.NewFromConfig(cloud9CfgWithStub(stub, testRegion))
	if _, _, err := scanSageMakerGovernance(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertSMIDs(t, st, TypeSageMakerUltraServer, rc+"/ultraserver/us-1")
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, planARN), rc+"/ultraserver/us-1")
}
