package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

func init() { registerService(serviceEntry{name: "aws:sqs", fn: scanSQS}) }

// scanSQS discovers SQS queues in one region. SQS has no paginator type;
// we iterate manually using NextToken.
func scanSQS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := sqs.NewFromConfig(acct.cfg, func(o *sqs.Options) { o.Region = region })

	var nextToken *string
	for {
		out, err := client.ListQueues(ctx, &sqs.ListQueuesInput{
			MaxResults: sdkaws.Int32(1000),
			NextToken:  nextToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "sqs:ListQueues", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("sqs:ListQueues: %w", err)
		}
		var batch []*store.Resource
		for _, url := range out.QueueUrls {
			// Use the queue URL as NativeID; it uniquely identifies the queue.
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSQSQueue,
				NativeID:       url,
				Name:           &url,
				Region:         &region,
				AttributesJSON: mustJSON(map[string]string{"url": url}),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert SQS queues: %w", err)
			}
			total += len(batch)
			inserted += n
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	return total, inserted, nil
}
