package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/eks"
)

func init() {
	registerService(serviceEntry{
		name: "aws:eks",
		fn:   scanEKS,
		emits: []coverage.TypeDecl{
			{Service: "eks", DiscoType: TypeEKSCluster},
			{Service: "eks", DiscoType: TypeEKSAccessEntry},
			{Service: "eks", DiscoType: TypeEKSAddon},
			{Service: "eks", DiscoType: TypeEKSCapability},
			{Service: "eks", DiscoType: TypeEKSFargateProfile},
			{Service: "eks", DiscoType: TypeEKSIdentityProviderConfig},
			{Service: "eks", DiscoType: TypeEKSNodegroup},
			{Service: "eks", DiscoType: TypeEKSPodIdentityAssociation},
		},
	})
}

// eksAPI is the narrow set of EKS operations called by scanEKSClusters.
// *eks.Client satisfies; tests substitute a stub.
type eksAPI interface {
	ListClusters(context.Context, *eks.ListClustersInput, ...func(*eks.Options)) (*eks.ListClustersOutput, error)
	DescribeCluster(context.Context, *eks.DescribeClusterInput, ...func(*eks.Options)) (*eks.DescribeClusterOutput, error)
}

// scanEKS discovers EKS clusters in one region. ListClusters returns names
// only; clusters are described in parallel to avoid N+1 sequential API calls.
func scanEKS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := eks.NewFromConfig(acct.cfg, func(o *eks.Options) { o.Region = region })
	t, i, ferr := scanEKSClusters(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanEKSExtended(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

// scanEKSClusters holds the testable scan body.
func scanEKSClusters(ctx context.Context, client eksAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := eks.NewListClustersPaginator(client, &eks.ListClustersInput{})
	return pageScanConcurrent(ctx, "eks:ListClusters", acct, region, st,
		p.HasMorePages,
		func(c context.Context) (*eks.ListClustersOutput, error) { return p.NextPage(c) },
		func(o *eks.ListClustersOutput) []string { return o.Clusters },
		func(gctx context.Context, name string) (*store.Resource, error) {
			desc, err := client.DescribeCluster(gctx, &eks.DescribeClusterInput{Name: &name})
			if err != nil {
				if isAccessDenied(err) {
					return nil, nil
				}
				return nil, fmt.Errorf("eks:DescribeCluster %s: %w", name, err)
			}
			c := desc.Cluster
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEKSCluster,
				NativeID:       sv(c.Arn),
				Name:           c.Name,
				Region:         &region,
				CreatedAt:      tp(c.CreatedAt),
				Status:         sp(string(c.Status)),
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			}
			if len(c.Tags) > 0 {
				s := mustJSON(c.Tags)
				r.TagsJSON = &s
			}
			return r, nil
		}, 0)
}
