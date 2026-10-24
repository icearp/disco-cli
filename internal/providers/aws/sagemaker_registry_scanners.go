package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSageMakerModelPackage, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerModelPackageGroup, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerModelCard, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerFeatureGroup, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerMlflowTrackingServer, Service: "sagemaker"})
}

// sagemakerRegistryAPI is the model-registry family's narrow surface. Each
// phase List+fan-out Describe so attrs carry the full Describe body:
// InferenceSpecification (model package), Status/Description (group),
// ModelCardContent (card), OnlineStoreConfig + OfflineStoreConfig (feature
// group), ArtifactStoreUri + RoleArn (MLflow tracking server).
type sagemakerRegistryAPI interface {
	sagemakerTagsAPI
	ListModelPackages(context.Context, *sagemaker.ListModelPackagesInput, ...func(*sagemaker.Options)) (*sagemaker.ListModelPackagesOutput, error)
	DescribeModelPackage(context.Context, *sagemaker.DescribeModelPackageInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeModelPackageOutput, error)
	ListModelPackageGroups(context.Context, *sagemaker.ListModelPackageGroupsInput, ...func(*sagemaker.Options)) (*sagemaker.ListModelPackageGroupsOutput, error)
	DescribeModelPackageGroup(context.Context, *sagemaker.DescribeModelPackageGroupInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeModelPackageGroupOutput, error)
	ListModelCards(context.Context, *sagemaker.ListModelCardsInput, ...func(*sagemaker.Options)) (*sagemaker.ListModelCardsOutput, error)
	DescribeModelCard(context.Context, *sagemaker.DescribeModelCardInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeModelCardOutput, error)
	ListFeatureGroups(context.Context, *sagemaker.ListFeatureGroupsInput, ...func(*sagemaker.Options)) (*sagemaker.ListFeatureGroupsOutput, error)
	DescribeFeatureGroup(context.Context, *sagemaker.DescribeFeatureGroupInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeFeatureGroupOutput, error)
	ListMlflowTrackingServers(context.Context, *sagemaker.ListMlflowTrackingServersInput, ...func(*sagemaker.Options)) (*sagemaker.ListMlflowTrackingServersOutput, error)
	DescribeMlflowTrackingServer(context.Context, *sagemaker.DescribeMlflowTrackingServerInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeMlflowTrackingServerOutput, error)
}

// scanSageMakerRegistry runs all model-registry-family phases for one region.
func scanSageMakerRegistry(ctx context.Context, client sagemakerRegistryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return runSageMakerPhases(
		sagemakerPhase{"sagemaker:ListModelPackages", func() (int, int, error) { return scanSageMakerModelPackages(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListModelPackageGroups", func() (int, int, error) {
			return scanSageMakerModelPackageGroups(ctx, client, acct, region, st, scanID)
		}},
		sagemakerPhase{"sagemaker:ListModelCards", func() (int, int, error) { return scanSageMakerModelCards(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListFeatureGroups", func() (int, int, error) { return scanSageMakerFeatureGroups(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListMlflowTrackingServers", func() (int, int, error) {
			return scanSageMakerMlflowTrackingServers(ctx, client, acct, region, st, scanID)
		}},
	)
}

// scanSageMakerModelPackages fans out by ARN — versioned entries may leave
// ModelPackageName empty, but every entry (versioned or not) carries
// ModelPackageArn; DescribeModelPackage accepts either name or ARN as input.
func scanSageMakerModelPackages(ctx context.Context, client sagemakerRegistryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.ModelPackageSummary]{
		listOp: "sagemaker:ListModelPackages", describeOp: "sagemaker:DescribeModelPackage",
		list: func(ctx context.Context, token *string) ([]smtypes.ModelPackageSummary, *string, error) {
			out, err := client.ListModelPackages(ctx, &sagemaker.ListModelPackagesInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ModelPackageSummaryList, out.NextToken, nil
		},
		name: func(s smtypes.ModelPackageSummary) string { return sv(s.ModelPackageArn) },
		listed: func(s smtypes.ModelPackageSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerModelPackage, s.ModelPackageArn, s.ModelPackageName, s.CreationTime, string(s.ModelPackageStatus), s)
		},
		describe: func(ctx context.Context, s smtypes.ModelPackageSummary) (*store.Resource, error) {
			arn := sv(s.ModelPackageArn)
			out, err := client.DescribeModelPackage(ctx, &sagemaker.DescribeModelPackageInput{ModelPackageName: &arn})
			if err != nil {
				return nil, err
			}
			outARN := sv(out.ModelPackageArn)
			if outARN == "" {
				return nil, nil
			}
			name := sv(out.ModelPackageName)
			status := string(out.ModelPackageStatus)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerModelPackage,
				NativeID:       outARN,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListModelPackages"))
}

func scanSageMakerModelPackageGroups(ctx context.Context, client sagemakerRegistryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.ModelPackageGroupSummary]{
		listOp: "sagemaker:ListModelPackageGroups", describeOp: "sagemaker:DescribeModelPackageGroup",
		list: func(ctx context.Context, token *string) ([]smtypes.ModelPackageGroupSummary, *string, error) {
			out, err := client.ListModelPackageGroups(ctx, &sagemaker.ListModelPackageGroupsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ModelPackageGroupSummaryList, out.NextToken, nil
		},
		name: func(s smtypes.ModelPackageGroupSummary) string { return sv(s.ModelPackageGroupName) },
		listed: func(s smtypes.ModelPackageGroupSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerModelPackageGroup, s.ModelPackageGroupArn, s.ModelPackageGroupName, s.CreationTime, string(s.ModelPackageGroupStatus), s)
		},
		describe: func(ctx context.Context, s smtypes.ModelPackageGroupSummary) (*store.Resource, error) {
			name := sv(s.ModelPackageGroupName)
			out, err := client.DescribeModelPackageGroup(ctx, &sagemaker.DescribeModelPackageGroupInput{ModelPackageGroupName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.ModelPackageGroupArn)
			if arn == "" {
				return nil, nil
			}
			gname := sv(out.ModelPackageGroupName)
			status := string(out.ModelPackageGroupStatus)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerModelPackageGroup,
				NativeID:       arn,
				Name:           &gname,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListModelPackageGroups"))
}

func scanSageMakerModelCards(ctx context.Context, client sagemakerRegistryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.ModelCardSummary]{
		listOp: "sagemaker:ListModelCards", describeOp: "sagemaker:DescribeModelCard", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.ModelCardSummary, *string, error) {
			out, err := client.ListModelCards(ctx, &sagemaker.ListModelCardsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ModelCardSummaries, out.NextToken, nil
		},
		name: func(s smtypes.ModelCardSummary) string { return sv(s.ModelCardName) },
		listed: func(s smtypes.ModelCardSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerModelCard, s.ModelCardArn, s.ModelCardName, s.CreationTime, string(s.ModelCardStatus), s)
		},
		describe: func(ctx context.Context, s smtypes.ModelCardSummary) (*store.Resource, error) {
			name := sv(s.ModelCardName)
			out, err := client.DescribeModelCard(ctx, &sagemaker.DescribeModelCardInput{ModelCardName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.ModelCardArn)
			if arn == "" {
				return nil, nil
			}
			cname := sv(out.ModelCardName)
			status := string(out.ModelCardStatus)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerModelCard,
				NativeID:       arn,
				Name:           &cname,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListModelCards"))
}

func scanSageMakerFeatureGroups(ctx context.Context, client sagemakerRegistryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.FeatureGroupSummary]{
		listOp: "sagemaker:ListFeatureGroups", describeOp: "sagemaker:DescribeFeatureGroup", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.FeatureGroupSummary, *string, error) {
			out, err := client.ListFeatureGroups(ctx, &sagemaker.ListFeatureGroupsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.FeatureGroupSummaries, out.NextToken, nil
		},
		name: func(s smtypes.FeatureGroupSummary) string { return sv(s.FeatureGroupName) },
		listed: func(s smtypes.FeatureGroupSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerFeatureGroup, s.FeatureGroupArn, s.FeatureGroupName, s.CreationTime, string(s.FeatureGroupStatus), s)
		},
		describe: func(ctx context.Context, s smtypes.FeatureGroupSummary) (*store.Resource, error) {
			name := sv(s.FeatureGroupName)
			out, err := client.DescribeFeatureGroup(ctx, &sagemaker.DescribeFeatureGroupInput{FeatureGroupName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.FeatureGroupArn)
			if arn == "" {
				return nil, nil
			}
			fname := sv(out.FeatureGroupName)
			status := string(out.FeatureGroupStatus)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerFeatureGroup,
				NativeID:       arn,
				Name:           &fname,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListFeatureGroups"))
}

func scanSageMakerMlflowTrackingServers(ctx context.Context, client sagemakerRegistryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.TrackingServerSummary]{
		listOp: "sagemaker:ListMlflowTrackingServers", describeOp: "sagemaker:DescribeMlflowTrackingServer", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.TrackingServerSummary, *string, error) {
			out, err := client.ListMlflowTrackingServers(ctx, &sagemaker.ListMlflowTrackingServersInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.TrackingServerSummaries, out.NextToken, nil
		},
		name: func(s smtypes.TrackingServerSummary) string { return sv(s.TrackingServerName) },
		listed: func(s smtypes.TrackingServerSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerMlflowTrackingServer, s.TrackingServerArn, s.TrackingServerName, s.CreationTime, string(s.TrackingServerStatus), s)
		},
		describe: func(ctx context.Context, s smtypes.TrackingServerSummary) (*store.Resource, error) {
			name := sv(s.TrackingServerName)
			out, err := client.DescribeMlflowTrackingServer(ctx, &sagemaker.DescribeMlflowTrackingServerInput{TrackingServerName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.TrackingServerArn)
			if arn == "" {
				return nil, nil
			}
			sname := sv(out.TrackingServerName)
			status := string(out.TrackingServerStatus)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerMlflowTrackingServer,
				NativeID:       arn,
				Name:           &sname,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListMlflowTrackingServers"))
}
