package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

// scanSNS discovers SNS topics in one region.
func scanSNS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error {
	client := sns.NewFromConfig(acct.cfg, func(o *sns.Options) { o.Region = region })

	pager := sns.NewListTopicsPaginator(client, &sns.ListTopicsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("sns:ListTopics", acct.ID, region, err)
			}
			return fmt.Errorf("sns:ListTopics: %w", err)
		}
		var batch []*store.Resource
		for _, topic := range page.Topics {
			arn := sv(topic.TopicArn)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           "aws:sns:topic",
				NativeID:       arn,
				Name:           &arn,
				Region:         &region,
				AttributesJSON: mustJSON(topic),
				ScanID:         scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert SNS topics: %w", err)
			}
		}
	}
	return nil
}
