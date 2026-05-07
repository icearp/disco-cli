package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
)

type stubSageMakerRegistry struct {
	packages      []smtypes.ModelPackageSummary
	packageOut    map[string]*sagemaker.DescribeModelPackageOutput
	groups        []smtypes.ModelPackageGroupSummary
	groupOut      map[string]*sagemaker.DescribeModelPackageGroupOutput
	cards         []smtypes.ModelCardSummary
	cardOut       map[string]*sagemaker.DescribeModelCardOutput
	featureGroups []smtypes.FeatureGroupSummary
	featureOut    map[string]*sagemaker.DescribeFeatureGroupOutput
	servers       []smtypes.TrackingServerSummary
	serverOut     map[string]*sagemaker.DescribeMlflowTrackingServerOutput
}

func (s *stubSageMakerRegistry) ListModelPackages(_ context.Context, _ *sagemaker.ListModelPackagesInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListModelPackagesOutput, error) {
	return &sagemaker.ListModelPackagesOutput{ModelPackageSummaryList: s.packages}, nil
}

func (s *stubSageMakerRegistry) DescribeModelPackage(_ context.Context, in *sagemaker.DescribeModelPackageInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeModelPackageOutput, error) {
	return s.packageOut[*in.ModelPackageName], nil
}

func (s *stubSageMakerRegistry) ListModelPackageGroups(_ context.Context, _ *sagemaker.ListModelPackageGroupsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListModelPackageGroupsOutput, error) {
	return &sagemaker.ListModelPackageGroupsOutput{ModelPackageGroupSummaryList: s.groups}, nil
}

func (s *stubSageMakerRegistry) DescribeModelPackageGroup(_ context.Context, in *sagemaker.DescribeModelPackageGroupInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeModelPackageGroupOutput, error) {
	return s.groupOut[*in.ModelPackageGroupName], nil
}

func (s *stubSageMakerRegistry) ListModelCards(_ context.Context, _ *sagemaker.ListModelCardsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListModelCardsOutput, error) {
	return &sagemaker.ListModelCardsOutput{ModelCardSummaries: s.cards}, nil
}

func (s *stubSageMakerRegistry) DescribeModelCard(_ context.Context, in *sagemaker.DescribeModelCardInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeModelCardOutput, error) {
	return s.cardOut[*in.ModelCardName], nil
}

func (s *stubSageMakerRegistry) ListFeatureGroups(_ context.Context, _ *sagemaker.ListFeatureGroupsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListFeatureGroupsOutput, error) {
	return &sagemaker.ListFeatureGroupsOutput{FeatureGroupSummaries: s.featureGroups}, nil
}

func (s *stubSageMakerRegistry) DescribeFeatureGroup(_ context.Context, in *sagemaker.DescribeFeatureGroupInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeFeatureGroupOutput, error) {
	return s.featureOut[*in.FeatureGroupName], nil
}

func (s *stubSageMakerRegistry) ListMlflowTrackingServers(_ context.Context, _ *sagemaker.ListMlflowTrackingServersInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListMlflowTrackingServersOutput, error) {
	return &sagemaker.ListMlflowTrackingServersOutput{TrackingServerSummaries: s.servers}, nil
}

func (s *stubSageMakerRegistry) DescribeMlflowTrackingServer(_ context.Context, in *sagemaker.DescribeMlflowTrackingServerInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeMlflowTrackingServerOutput, error) {
	return s.serverOut[*in.TrackingServerName], nil
}

func TestScanSageMakerRegistry(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	now := time.Unix(1700000000, 0).UTC()

	pkgName := "pkg-1"
	pkgARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:model-package/%s/1", testRegion, acct.ID, pkgName)
	grpName := "grp-1"
	grpARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:model-package-group/%s", testRegion, acct.ID, grpName)
	cardName := "card-1"
	cardARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:model-card/%s", testRegion, acct.ID, cardName)
	fgName := "fg-1"
	fgARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:feature-group/%s", testRegion, acct.ID, fgName)
	srvName := "server-1"
	srvARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:mlflow-tracking-server/%s", testRegion, acct.ID, srvName)

	stub := &stubSageMakerRegistry{
		packages: []smtypes.ModelPackageSummary{{ModelPackageArn: &pkgARN, ModelPackageName: &pkgName, ModelPackageStatus: smtypes.ModelPackageStatusCompleted, CreationTime: &now}},
		packageOut: map[string]*sagemaker.DescribeModelPackageOutput{
			pkgARN: {ModelPackageArn: &pkgARN, ModelPackageName: &pkgName, ModelPackageStatus: smtypes.ModelPackageStatusCompleted, CreationTime: &now},
		},
		groups: []smtypes.ModelPackageGroupSummary{{ModelPackageGroupArn: &grpARN, ModelPackageGroupName: &grpName, CreationTime: &now}},
		groupOut: map[string]*sagemaker.DescribeModelPackageGroupOutput{
			grpName: {ModelPackageGroupArn: &grpARN, ModelPackageGroupName: &grpName, ModelPackageGroupStatus: smtypes.ModelPackageGroupStatusCompleted, CreationTime: &now},
		},
		cards: []smtypes.ModelCardSummary{{ModelCardArn: &cardARN, ModelCardName: &cardName, CreationTime: &now}},
		cardOut: map[string]*sagemaker.DescribeModelCardOutput{
			cardName: {ModelCardArn: &cardARN, ModelCardName: &cardName, ModelCardStatus: smtypes.ModelCardStatusDraft, CreationTime: &now},
		},
		featureGroups: []smtypes.FeatureGroupSummary{{FeatureGroupArn: &fgARN, FeatureGroupName: &fgName, CreationTime: &now}},
		featureOut: map[string]*sagemaker.DescribeFeatureGroupOutput{
			fgName: {FeatureGroupArn: &fgARN, FeatureGroupName: &fgName, FeatureGroupStatus: smtypes.FeatureGroupStatusCreated, CreationTime: &now},
		},
		servers: []smtypes.TrackingServerSummary{{TrackingServerArn: &srvARN, TrackingServerName: &srvName, CreationTime: &now}},
		serverOut: map[string]*sagemaker.DescribeMlflowTrackingServerOutput{
			srvName: {TrackingServerArn: &srvARN, TrackingServerName: &srvName, TrackingServerStatus: smtypes.TrackingServerStatusCreated, CreationTime: &now},
		},
	}

	total, inserted, err := scanSageMakerRegistry(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 5 || inserted != 5 {
		t.Fatalf("total=%d inserted=%d want 5/5", total, inserted)
	}
	for _, want := range []struct{ typ, id string }{
		{TypeSageMakerModelPackage, pkgARN},
		{TypeSageMakerModelPackageGroup, grpARN},
		{TypeSageMakerModelCard, cardARN},
		{TypeSageMakerFeatureGroup, fgARN},
		{TypeSageMakerMlflowTrackingServer, srvARN},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.typ, want.id)); err != nil {
			t.Errorf("%s missing: %v", want.typ, err)
		}
	}
}

func TestScanSageMakerRegistryEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubSageMakerRegistry{}
	total, inserted, err := scanSageMakerRegistry(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}
