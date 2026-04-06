package aws

import (
	"context"
	"fmt"
	"sync"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:eks", fn: scanEKS}) }

// scanEKS discovers EKS clusters in one region. ListClusters returns names
// only; we describe each cluster in parallel to avoid N+1 sequential API calls.
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

		// Describe all clusters in the page concurrently.
		var (
			mu    sync.Mutex
			batch []*store.Resource
		)
		g, gctx := errgroup.WithContext(ctx)
		for _, name := range page.Clusters {
			g.Go(func() error {
				desc, err := client.DescribeCluster(gctx, &eks.DescribeClusterInput{Name: &name})
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return fmt.Errorf("eks:DescribeCluster %s: %w", name, err)
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
					DiscoveredBy:         scanID,
				}
				if len(c.Tags) > 0 {
					s := mustJSON(c.Tags)
					r.TagsJSON = &s
				}
				mu.Lock()
				batch = append(batch, r)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return err
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert EKS clusters: %w", err)
			}
		}
	}
	return nil
}
