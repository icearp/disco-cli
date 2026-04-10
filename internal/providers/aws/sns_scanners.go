package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

func init() { registerService(serviceEntry{name: "aws:sns", fn: scanSNS}) }

// scanSNS discovers SNS topics in one region.
func scanSNS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := sns.NewFromConfig(acct.cfg, func(o *sns.Options) { o.Region = region })

	pager := sns.NewListTopicsPaginator(client, &sns.ListTopicsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("sns:ListTopics", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("sns:ListTopics: %w", err)
		}
		var batch []*store.Resource
		for _, topic := range page.Topics {
			arn := sv(topic.TopicArn)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSNSTopic,
				NativeID:       arn,
				Name:           &arn,
				Region:         &region,
				AttributesJSON: mustJSON(topic),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
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
