package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/eks"
)

// scanEKS discovers EKS clusters in one region. ListClusters returns names
// only; we fetch full details via DescribeCluster for each.
func scanEKS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error {
	client := eks.NewFromConfig(acct.cfg, func(o *eks.Options) { o.Region = region })

	pager := eks.NewListClustersPaginator(client, &eks.ListClustersInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("eks:ListClusters", acct.ID, region, err)
			}
			return fmt.Errorf("eks:ListClusters: %w", err)
		}
		var batch []*store.Resource
		for _, name := range page.Clusters {
			desc, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: &name})
			if err != nil {
				if isAccessDenied(err) {
					continue
				}
				return fmt.Errorf("eks:DescribeCluster %s: %w", name, err)
			}
			c := desc.Cluster
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           "aws:eks:cluster",
				NativeID:       sv(c.Arn),
				Name:           c.Name,
				Region:         &region,
				Status:         sp(string(c.Status)),
				AttributesJSON: mustJSON(c),
				ScanID:         scanID,
			}
			if len(c.Tags) > 0 {
				s := mustJSON(c.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert EKS clusters: %w", err)
			}
		}
	}
	return nil
}
