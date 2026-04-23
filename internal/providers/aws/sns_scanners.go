package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:sns", fn: scanSNS}) }

// scanSNS discovers SNS topics in one region. ListTopics returns only ARNs;
// GetTopicAttributes is called concurrently to fetch the attributes map
// (KmsMasterKeyId, RedrivePolicy, etc.) needed by the resolver.
func scanSNS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := sns.NewFromConfig(acct.cfg, func(o *sns.Options) { o.Region = region })

	pager := sns.NewListTopicsPaginator(client, &sns.ListTopicsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "sns:ListTopics", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("sns:ListTopics: %w", err)
		}

		var (
			mu    sync.Mutex
			batch []*store.Resource
		)
		g, gctx := errgroup.WithContext(ctx)
		for _, topic := range page.Topics {
			arn := sv(topic.TopicArn)
			g.Go(func() error {
				attrsOut, err := client.GetTopicAttributes(gctx, &sns.GetTopicAttributesInput{TopicArn: &arn})
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return fmt.Errorf("sns:GetTopicAttributes %s: %w", arn, err)
				}
				r := &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeSNSTopic,
					NativeID:       arn,
					Name:           &arn,
					Region:         &region,
					AttributesJSON: mustJSON(attrsOut.Attributes),
					DiscoveredBy:   scanID,
				}
				mu.Lock()
				batch = append(batch, r)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return 0, 0, err
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert SNS topics: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
