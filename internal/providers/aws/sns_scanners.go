package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

func init() { registerService(serviceEntry{name: "aws:sns", fn: scanSNS}) }

// scanSNS discovers SNS topics in one region. ListTopics returns only ARNs;
// GetTopicAttributes is called concurrently to fetch the attributes map
// (KmsMasterKeyId, RedrivePolicy, etc.) needed by the resolver.
func scanSNS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := sns.NewFromConfig(acct.cfg, func(o *sns.Options) { o.Region = region })
	p := sns.NewListTopicsPaginator(client, &sns.ListTopicsInput{})
	return pageScanConcurrent(ctx, "sns:ListTopics", acct, region, st,
		p.HasMorePages,
		func(c context.Context) (*sns.ListTopicsOutput, error) { return p.NextPage(c) },
		func(o *sns.ListTopicsOutput) []string {
			out := make([]string, 0, len(o.Topics))
			for _, t := range o.Topics {
				out = append(out, sv(t.TopicArn))
			}
			return out
		},
		func(gctx context.Context, arn string) (*store.Resource, error) {
			attrsOut, err := client.GetTopicAttributes(gctx, &sns.GetTopicAttributesInput{TopicArn: &arn})
			if err != nil {
				if isAccessDenied(err) {
					return nil, nil
				}
				return nil, fmt.Errorf("sns:GetTopicAttributes %s: %w", arn, err)
			}
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSNSTopic,
				NativeID:       arn,
				Name:           &arn,
				Region:         &region,
				AttributesJSON: mustJSON(attrsOut.Attributes),
				DiscoveredBy:   scanID,
			}, nil
		}, 0)
}
