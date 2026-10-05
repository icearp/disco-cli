package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	"github.com/icearp/disco-cli/store"
)

type stubSageMakerMisc struct {
	clusters    []smtypes.ClusterSummary
	clusterOut  map[string]*sagemaker.DescribeClusterOutput
	workteams   []smtypes.Workteam
	workteamOut map[string]*sagemaker.DescribeWorkteamOutput
	nodes       map[string][]smtypes.ClusterNodeSummary
	subscribed  []smtypes.SubscribedWorkteam
}

func (s *stubSageMakerMisc) ListClusters(_ context.Context, _ *sagemaker.ListClustersInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListClustersOutput, error) {
	return &sagemaker.ListClustersOutput{ClusterSummaries: s.clusters}, nil
}

func (s *stubSageMakerMisc) DescribeCluster(_ context.Context, in *sagemaker.DescribeClusterInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeClusterOutput, error) {
	return s.clusterOut[*in.ClusterName], nil
}

func (s *stubSageMakerMisc) ListWorkteams(_ context.Context, _ *sagemaker.ListWorkteamsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListWorkteamsOutput, error) {
	return &sagemaker.ListWorkteamsOutput{Workteams: s.workteams}, nil
}

func (s *stubSageMakerMisc) DescribeWorkteam(_ context.Context, in *sagemaker.DescribeWorkteamInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeWorkteamOutput, error) {
	return s.workteamOut[*in.WorkteamName], nil
}

func (s *stubSageMakerMisc) ListClusterNodes(_ context.Context, in *sagemaker.ListClusterNodesInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListClusterNodesOutput, error) {
	return &sagemaker.ListClusterNodesOutput{ClusterNodeSummaries: s.nodes[*in.ClusterName]}, nil
}

func (s *stubSageMakerMisc) ListSubscribedWorkteams(_ context.Context, _ *sagemaker.ListSubscribedWorkteamsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListSubscribedWorkteamsOutput, error) {
	return &sagemaker.ListSubscribedWorkteamsOutput{SubscribedWorkteams: s.subscribed}, nil
}

func TestScanSageMakerMisc(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	now := time.Unix(1700000000, 0).UTC()

	cName := "cluster-1"
	cARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:cluster/%s", testRegion, acct.ID, cName)
	wName := "team-1"
	wARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:workteam/private-crowd/%s", testRegion, acct.ID, wName)
	vendorARN := fmt.Sprintf("arn:aws:sagemaker:%s:394669845002:workteam/vendor-crowd/default", testRegion)
	nodeID := "i-0123456789abcdef0"

	stub := &stubSageMakerMisc{
		clusters: []smtypes.ClusterSummary{{ClusterArn: &cARN, ClusterName: &cName, CreationTime: &now}},
		clusterOut: map[string]*sagemaker.DescribeClusterOutput{
			cName: {ClusterArn: &cARN, ClusterName: &cName, ClusterStatus: smtypes.ClusterStatusInservice, CreationTime: &now},
		},
		workteams: []smtypes.Workteam{{WorkteamArn: &wARN, WorkteamName: &wName, CreateDate: &now}},
		workteamOut: map[string]*sagemaker.DescribeWorkteamOutput{
			wName: {Workteam: &smtypes.Workteam{WorkteamArn: &wARN, WorkteamName: &wName, CreateDate: &now}},
		},
		nodes:      map[string][]smtypes.ClusterNodeSummary{cARN: {smNode(nodeID)}},
		subscribed: []smtypes.SubscribedWorkteam{{WorkteamArn: &vendorARN}},
	}

	total, inserted, err := scanSageMakerMisc(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 4 || inserted != 4 {
		t.Fatalf("total=%d inserted=%d want 4/4", total, inserted)
	}
	for _, want := range []struct{ typ, id string }{
		{TypeSageMakerCluster, cARN},
		{TypeSageMakerWorkteam, wARN},
		{TypeSageMakerClusterNode, cARN + "/node/" + nodeID},
		{TypeSageMakerSubscribedWorkteam, vendorARN},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.id)); err != nil {
			t.Errorf("%s missing: %v", want.typ, err)
		}
	}
}

func TestScanSageMakerMiscEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubSageMakerMisc{}
	total, inserted, err := scanSageMakerMisc(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}

// --- cluster nodes ---------------------------------------------------------

type stubSMClusterNodes struct {
	sagemakerMiscAPI
	pages map[string][][]smtypes.ClusterNodeSummary
	errs  map[string]error
}

func (s *stubSMClusterNodes) ListClusterNodes(_ context.Context, in *sagemaker.ListClusterNodesInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListClusterNodesOutput, error) {
	if err := s.errs[*in.ClusterName]; err != nil {
		return nil, err
	}
	items, next, err := smPage(s.pages[*in.ClusterName], in.NextToken)
	if err != nil {
		return nil, err
	}
	return &sagemaker.ListClusterNodesOutput{ClusterNodeSummaries: items, NextToken: next}, nil
}

func smNode(id string) smtypes.ClusterNodeSummary {
	return smtypes.ClusterNodeSummary{
		InstanceId:        sdkaws.String(id),
		InstanceGroupName: sdkaws.String("workers"),
		InstanceStatus:    &smtypes.ClusterInstanceStatusDetails{Status: smtypes.ClusterInstanceStatusRunning},
	}
}

func smClusterARN(name string) string {
	return "arn:aws:sagemaker:us-east-1:123456789012:cluster/" + name
}

func TestScanSageMakerClusterNodes_PaginatesEachScannedCluster(t *testing.T) {
	st := newTestStore(t)
	const staleScanID = "11111111111111111111111111111111"
	insertTestScan(t, st, staleScanID, "completed")
	c1, c2, gone, stale, west := smClusterARN("c1"), smClusterARN("c2"), smClusterARN("gone"), smClusterARN("stale"), "arn:aws:sagemaker:us-west-2:123456789012:cluster/west"
	c1ID := upsertTestResourceNamed(t, st, TypeSageMakerCluster, c1, testRegion, "{}", "c1")
	c2ID := upsertTestResourceNamed(t, st, TypeSageMakerCluster, c2, testRegion, "{}", "c2")
	upsertTestResourceNamed(t, st, TypeSageMakerCluster, gone, testRegion, "{}", "gone")
	upsertTestResourceNamed(t, st, TypeSageMakerCluster, west, "us-west-2", "{}", "west")
	staleRegion := testRegion
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: testAccountID, Type: TypeSageMakerCluster, NativeID: stale,
		Region: &staleRegion, AttributesJSON: "{}", DiscoveredBy: staleScanID,
	}); err != nil {
		t.Fatalf("seed stale cluster: %v", err)
	}

	stub := &stubSMClusterNodes{
		pages: map[string][][]smtypes.ClusterNodeSummary{
			c1:    {{smNode("i-1")}, {{InstanceId: sdkaws.String("i-2"), InstanceStatus: &smtypes.ClusterInstanceStatusDetails{}}, {InstanceGroupName: sdkaws.String("provisioning")}}},
			c2:    {{smNode("i-3")}},
			stale: {{smNode("i-stale")}},
			west:  {{smNode("i-west")}},
		},
		errs: map[string]error{gone: apiErr("ResourceNotFound", "cluster not found")},
	}
	total, _, err := scanSageMakerClusterNodes(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	assertSMIDs(t, st, TypeSageMakerClusterNode, c1+"/node/i-1", c1+"/node/i-2", c2+"/node/i-3")
	assertSMStatus(t, st, TypeSageMakerClusterNode, c1+"/node/i-1", string(smtypes.ClusterInstanceStatusRunning))
	assertSMStatus(t, st, TypeSageMakerClusterNode, c1+"/node/i-2", "")
	assertSMContains(t, st, c1ID, c1+"/node/i-2")
	assertSMContains(t, st, c2ID, c2+"/node/i-3")
}

func TestScanSageMakerClusterNodes_NoNodes(t *testing.T) {
	st := newTestStore(t)
	upsertTestResourceNamed(t, st, TypeSageMakerCluster, smClusterARN("c1"), testRegion, "{}", "c1")
	total, _, err := scanSageMakerClusterNodes(context.Background(), &stubSMClusterNodes{}, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Fatalf("scan = (%d, %v); want (0, nil)", total, err)
	}
}

// A resource-scoped deny on one cluster must not hide its siblings' nodes, and
// two denied clusters warn once.
func TestScanSageMakerClusterNodes_DeniedClustersKeepSiblings(t *testing.T) {
	st := newTestStore(t)
	warnings := countSMWarnings(st)
	denied1, denied2, open := smClusterARN("denied1"), smClusterARN("denied2"), smClusterARN("open")
	for _, c := range []string{denied1, denied2, open} {
		upsertTestResourceNamed(t, st, TypeSageMakerCluster, c, testRegion, "{}", c)
	}
	stub := &stubSMClusterNodes{
		pages: map[string][][]smtypes.ClusterNodeSummary{open: {{smNode("i-1")}}},
		errs: map[string]error{
			denied1: apiErr("AccessDeniedException", "denied"),
			denied2: apiErr("AccessDeniedException", "denied"),
		},
	}
	if _, _, err := scanSageMakerClusterNodes(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertSMIDs(t, st, TypeSageMakerClusterNode, open+"/node/i-1")
	if *warnings != 1 {
		t.Errorf("warnings = %d; want 1", *warnings)
	}
}

func TestScanSageMakerClusterNodes_OtherErrorFails(t *testing.T) {
	st := newTestStore(t)
	c1 := smClusterARN("c1")
	upsertTestResourceNamed(t, st, TypeSageMakerCluster, c1, testRegion, "{}", "c1")
	stub := &stubSMClusterNodes{errs: map[string]error{c1: apiErr("ValidationException", "bad")}}
	if _, _, err := scanSageMakerClusterNodes(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); err == nil {
		t.Fatal("ValidationException was swallowed; want error")
	}
}

// --- subscribed workteams --------------------------------------------------

type stubSMSubscribedWorkteams struct {
	sagemakerMiscAPI
	pages [][]smtypes.SubscribedWorkteam
	err   error
}

func (s *stubSMSubscribedWorkteams) ListSubscribedWorkteams(_ context.Context, in *sagemaker.ListSubscribedWorkteamsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListSubscribedWorkteamsOutput, error) {
	if s.err != nil {
		return nil, s.err
	}
	items, next, err := smPage(s.pages, in.NextToken)
	if err != nil {
		return nil, err
	}
	return &sagemaker.ListSubscribedWorkteamsOutput{SubscribedWorkteams: items, NextToken: next}, nil
}

func TestScanSageMakerSubscribedWorkteams_Paginates(t *testing.T) {
	st := newTestStore(t)
	w1 := "arn:aws:sagemaker:us-east-1:394669845002:workteam/vendor-crowd/w1"
	w2 := "arn:aws:sagemaker:us-east-1:394669845002:workteam/vendor-crowd/w2"
	stub := &stubSMSubscribedWorkteams{pages: [][]smtypes.SubscribedWorkteam{
		{{WorkteamArn: sdkaws.String(w1), MarketplaceTitle: sdkaws.String("Vendor one")}},
		{{WorkteamArn: sdkaws.String(w2)}, {MarketplaceTitle: sdkaws.String("no arn")}},
	}}
	total, _, err := scanSageMakerSubscribedWorkteams(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2", total)
	}
	assertSMIDs(t, st, TypeSageMakerSubscribedWorkteam, w1, w2)
}

func TestScanSageMakerSubscribedWorkteams_NoWorkteams(t *testing.T) {
	st := newTestStore(t)
	total, _, err := scanSageMakerSubscribedWorkteams(context.Background(), &stubSMSubscribedWorkteams{}, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Fatalf("scan = (%d, %v); want (0, nil)", total, err)
	}
}

func TestScanSageMakerSubscribedWorkteams_AccessDeniedWarns(t *testing.T) {
	st := newTestStore(t)
	warnings := countSMWarnings(st)
	stub := &stubSMSubscribedWorkteams{err: apiErr("AccessDeniedException", "denied")}
	total, _, err := scanSageMakerSubscribedWorkteams(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Fatalf("scan = (%d, %v); want (0, nil)", total, err)
	}
	if *warnings != 1 {
		t.Errorf("warnings = %d; want 1", *warnings)
	}
}
