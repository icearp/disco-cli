package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "sagemaker", DiscoType: TypeSageMakerModelPackage},
		coverage.TypeDecl{Service: "sagemaker", DiscoType: TypeSageMakerModelPackageGroup},
		coverage.TypeDecl{Service: "sagemaker", DiscoType: TypeSageMakerModelCard},
		coverage.TypeDecl{Service: "sagemaker", DiscoType: TypeSageMakerFeatureGroup},
		coverage.TypeDecl{Service: "sagemaker", DiscoType: TypeSageMakerMlflowTrackingServer},
	)
}

// sagemakerRegistryAPI is the model-registry family's narrow surface. Each
// phase List+fan-out Describe so attrs carry the full Describe body:
// InferenceSpecification (model package), Status/Description (group),
// ModelCardContent (card), OnlineStoreConfig + OfflineStoreConfig (feature
// group), ArtifactStoreUri + RoleArn (MLflow tracking server).
type sagemakerRegistryAPI interface {
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
func scanSageMakerRegistry(ctx context.Context, client sagemakerRegistryAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func(context.Context, sagemakerRegistryAPI, *account, string, *store.Store, string) (int, int, error){
		scanSageMakerModelPackages,
		scanSageMakerModelPackageGroups,
		scanSageMakerModelCards,
		scanSageMakerFeatureGroups,
		scanSageMakerMlflowTrackingServers,
	} {
		t, i, ferr := phase(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// scanSageMakerModelPackages fans out by ARN — versioned entries may leave
// ModelPackageName empty, but every entry (versioned or not) carries
// ModelPackageArn; DescribeModelPackage accepts either name or ARN as input.
func scanSageMakerModelPackages(ctx context.Context, client sagemakerRegistryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListModelPackagesPaginator(client, &sagemaker.ListModelPackagesInput{})
	var arns []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListModelPackages", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListModelPackages: %w", perr)
		}
		for _, p := range out.ModelPackageSummaryList {
			if p.ModelPackageArn != nil {
				arns = append(arns, *p.ModelPackageArn)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, arns, fanoutMed, func(gctx context.Context, arn string) (*store.Resource, error) {
		out, derr := client.DescribeModelPackage(gctx, &sagemaker.DescribeModelPackageInput{ModelPackageName: &arn})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeModelPackage %s: %w", arn, derr)
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
	}, st, "sagemaker model packages")
}

func scanSageMakerModelPackageGroups(ctx context.Context, client sagemakerRegistryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListModelPackageGroupsPaginator(client, &sagemaker.ListModelPackageGroupsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListModelPackageGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListModelPackageGroups: %w", perr)
		}
		for _, g := range out.ModelPackageGroupSummaryList {
			if g.ModelPackageGroupName != nil {
				names = append(names, *g.ModelPackageGroupName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeModelPackageGroup(gctx, &sagemaker.DescribeModelPackageGroupInput{ModelPackageGroupName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeModelPackageGroup %s: %w", name, derr)
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
	}, st, "sagemaker model package groups")
}

func scanSageMakerModelCards(ctx context.Context, client sagemakerRegistryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListModelCardsPaginator(client, &sagemaker.ListModelCardsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListModelCards", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListModelCards: %w", perr)
		}
		for _, c := range out.ModelCardSummaries {
			if c.ModelCardName != nil {
				names = append(names, *c.ModelCardName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeModelCard(gctx, &sagemaker.DescribeModelCardInput{ModelCardName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeModelCard %s: %w", name, derr)
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
	}, st, "sagemaker model cards")
}

func scanSageMakerFeatureGroups(ctx context.Context, client sagemakerRegistryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListFeatureGroupsPaginator(client, &sagemaker.ListFeatureGroupsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListFeatureGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListFeatureGroups: %w", perr)
		}
		for _, f := range out.FeatureGroupSummaries {
			if f.FeatureGroupName != nil {
				names = append(names, *f.FeatureGroupName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeFeatureGroup(gctx, &sagemaker.DescribeFeatureGroupInput{FeatureGroupName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeFeatureGroup %s: %w", name, derr)
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
	}, st, "sagemaker feature groups")
}

func scanSageMakerMlflowTrackingServers(ctx context.Context, client sagemakerRegistryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListMlflowTrackingServersPaginator(client, &sagemaker.ListMlflowTrackingServersInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListMlflowTrackingServers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListMlflowTrackingServers: %w", perr)
		}
		for _, s := range out.TrackingServerSummaries {
			if s.TrackingServerName != nil {
				names = append(names, *s.TrackingServerName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeMlflowTrackingServer(gctx, &sagemaker.DescribeMlflowTrackingServerInput{TrackingServerName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeMlflowTrackingServer %s: %w", name, derr)
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
	}, st, "sagemaker mlflow tracking servers")
}
