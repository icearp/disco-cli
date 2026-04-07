package aws

import (
	"context"
	"fmt"
	"sync"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:cloudfront",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) error {
			return scanCloudFront(ctx, acct, st, scanID)
		},
	})
}

// scanCloudFront discovers CloudFront distributions. CloudFront is a global
// service; distributions are not tied to a specific region.
// ListDistributions returns DistributionSummary items which contain the full
// operational configuration — no separate GetDistribution call is needed.
// Tags are fetched concurrently via ListTagsForResource.
func scanCloudFront(ctx context.Context, acct *account, st *store.Store, scanID string) error {
	// CloudFront is always accessed via us-east-1.
	client := cloudfront.NewFromConfig(acct.cfg, func(o *cloudfront.Options) { o.Region = "us-east-1" })

	pager := cloudfront.NewListDistributionsPaginator(client, &cloudfront.ListDistributionsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("cloudfront:ListDistributions", acct.ID, "global", err)
			}
			return fmt.Errorf("cloudfront:ListDistributions: %w", err)
		}
		if page.DistributionList == nil {
			continue
		}

		// Fetch tags for all distributions on this page concurrently.
		var mu sync.Mutex
		tagsByARN := make(map[string]*string, len(page.DistributionList.Items))
		g, gctx := errgroup.WithContext(ctx)
		for _, d := range page.DistributionList.Items {
			arn := sv(d.ARN)
			g.Go(func() error {
				out, err := client.ListTagsForResource(gctx, &cloudfront.ListTagsForResourceInput{Resource: &arn})
				if err != nil || out.Tags == nil {
					return nil // tags are best-effort
				}
				if t := awsTagsJSON(out.Tags.Items); t != nil {
					mu.Lock()
					tagsByARN[arn] = t
					mu.Unlock()
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return err
		}

		var batch []*store.Resource
		for _, d := range page.DistributionList.Items {
			arn := sv(d.ARN)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudFrontDistribution,
				NativeID:       arn,
				Name:           d.Id,
				CreatedAt:      tp(d.LastModifiedTime),
				Status:         d.Status,
				AttributesJSON: mustJSON(d),
				TagsJSON:       tagsByARN[arn],
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert CloudFront distributions: %w", err)
			}
		}
	}
	return nil
}
