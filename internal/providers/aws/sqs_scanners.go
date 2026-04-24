package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:sqs", fn: scanSQS}) }

// scanSQS discovers SQS queues in one region. ListQueues returns URLs;
// GetQueueAttributes is called concurrently to fetch the queue ARN (used as
// the NativeID so other services that reference queues by ARN resolve) and
// attributes (KmsMasterKeyId, RedrivePolicy) needed by the resolver.
func scanSQS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := sqs.NewFromConfig(acct.cfg, func(o *sqs.Options) { o.Region = region })

	p := sqs.NewListQueuesPaginator(client, &sqs.ListQueuesInput{
		MaxResults: sdkaws.Int32(1000),
	})
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "sqs:ListQueues", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("sqs:ListQueues: %w", err)
		}

		var (
			mu    sync.Mutex
			batch []*store.Resource
		)
		g, gctx := errgroup.WithContext(ctx)
		for _, url := range out.QueueUrls {
			g.Go(func() error {
				attrsOut, err := client.GetQueueAttributes(gctx, &sqs.GetQueueAttributesInput{
					QueueUrl:       &url,
					AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
				})
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return fmt.Errorf("sqs:GetQueueAttributes %s: %w", url, err)
				}
				arn := attrsOut.Attributes["QueueArn"]
				if arn == "" {
					return nil // queue deleted mid-scan or missing permission for QueueArn
				}
				// Include the URL alongside AWS attributes so the URL is still
				// queryable. AWS uses "QueueUrl" naming elsewhere.
				attrsOut.Attributes["QueueUrl"] = url
				r := &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeSQSQueue,
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
				return 0, 0, fmt.Errorf("upsert SQS queues: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
