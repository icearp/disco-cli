package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
)

type stubSageMakerMisc struct {
	clusters    []smtypes.ClusterSummary
	clusterOut  map[string]*sagemaker.DescribeClusterOutput
	workteams   []smtypes.Workteam
	workteamOut map[string]*sagemaker.DescribeWorkteamOutput
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

func TestScanSageMakerMisc(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	now := time.Unix(1700000000, 0).UTC()

	cName := "cluster-1"
	cARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:cluster/%s", testRegion, acct.ID, cName)
	wName := "team-1"
	wARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:workteam/private-crowd/%s", testRegion, acct.ID, wName)

	stub := &stubSageMakerMisc{
		clusters: []smtypes.ClusterSummary{{ClusterArn: &cARN, ClusterName: &cName, CreationTime: &now}},
		clusterOut: map[string]*sagemaker.DescribeClusterOutput{
			cName: {ClusterArn: &cARN, ClusterName: &cName, ClusterStatus: smtypes.ClusterStatusInservice, CreationTime: &now},
		},
		workteams: []smtypes.Workteam{{WorkteamArn: &wARN, WorkteamName: &wName, CreateDate: &now}},
		workteamOut: map[string]*sagemaker.DescribeWorkteamOutput{
			wName: {Workteam: &smtypes.Workteam{WorkteamArn: &wARN, WorkteamName: &wName, CreateDate: &now}},
		},
	}

	total, inserted, err := scanSageMakerMisc(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("total=%d inserted=%d want 2/2", total, inserted)
	}
	for _, want := range []struct{ typ, id string }{
		{TypeSageMakerCluster, cARN},
		{TypeSageMakerWorkteam, wARN},
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
