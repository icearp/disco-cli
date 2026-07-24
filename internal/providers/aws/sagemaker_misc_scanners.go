package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSageMakerCluster, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerWorkteam, Service: "sagemaker", Leaf: true})
}

// sagemakerMiscAPI is the narrow surface for the misc family —
// resources that don't fit other groupings (HyperPod cluster,
// ground-truth workteam).
type sagemakerMiscAPI interface {
	ListClusters(context.Context, *sagemaker.ListClustersInput, ...func(*sagemaker.Options)) (*sagemaker.ListClustersOutput, error)
	DescribeCluster(context.Context, *sagemaker.DescribeClusterInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeClusterOutput, error)
	ListWorkteams(context.Context, *sagemaker.ListWorkteamsInput, ...func(*sagemaker.Options)) (*sagemaker.ListWorkteamsOutput, error)
	DescribeWorkteam(context.Context, *sagemaker.DescribeWorkteamInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeWorkteamOutput, error)
}

// scanSageMakerMisc runs the misc-family phases: HyperPod clusters and
// ground-truth workteams.
func scanSageMakerMisc(ctx context.Context, client sagemakerMiscAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func(context.Context, sagemakerMiscAPI, *account, string, *store.Store, string) (int, int, error){
		scanSageMakerClusters,
		scanSageMakerWorkteams,
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

func scanSageMakerClusters(ctx context.Context, client sagemakerMiscAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListClustersPaginator(client, &sagemaker.ListClustersInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListClusters", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListClusters: %w", perr)
		}
		for _, c := range out.ClusterSummaries {
			if c.ClusterName != nil {
				names = append(names, *c.ClusterName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeCluster(gctx, &sagemaker.DescribeClusterInput{ClusterName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeCluster %s: %w", name, derr)
		}
		arn := sv(out.ClusterArn)
		if arn == "" {
			return nil, nil
		}
		cname := sv(out.ClusterName)
		status := string(out.ClusterStatus)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeSageMakerCluster,
			NativeID:       arn,
			Name:           &cname,
			Region:         &region,
			Status:         &status,
			CreatedAt:      tp(out.CreationTime),
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "sagemaker clusters")
}

// scanSageMakerWorkteams lists ground-truth workteams then fans out
// DescribeWorkteam for the full body, matching every other SageMaker
// family's per-Describe rule.
func scanSageMakerWorkteams(ctx context.Context, client sagemakerMiscAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListWorkteamsPaginator(client, &sagemaker.ListWorkteamsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListWorkteams", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListWorkteams: %w", perr)
		}
		for _, w := range out.Workteams {
			if w.WorkteamName != nil {
				names = append(names, *w.WorkteamName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeWorkteam(gctx, &sagemaker.DescribeWorkteamInput{WorkteamName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeWorkteam %s: %w", name, derr)
		}
		if out.Workteam == nil {
			return nil, nil
		}
		arn := sv(out.Workteam.WorkteamArn)
		if arn == "" {
			return nil, nil
		}
		wname := sv(out.Workteam.WorkteamName)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeSageMakerWorkteam,
			NativeID:       arn,
			Name:           &wname,
			Region:         &region,
			CreatedAt:      tp(out.Workteam.CreateDate),
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "sagemaker workteams")
}
