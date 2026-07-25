package aws

import (
	"context"
	"fmt"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSQSQueue, Service: "sqs", Upstream: "AWS::SQS::Queue"})
	registerService(serviceEntry{
		name: "aws:sqs",
		fn:   scanSQS,
	})
}

// sqsAPI is the narrow set of SQS operations scanSQSQueues calls. *sqs.Client
// satisfies it; tests supply a hand-rolled stub. Per AWS SDK Go v2's "Mocking
// client operations" guidance: each method matches the SDK shape so pagination
// + option fns keep working unchanged.
type sqsAPI interface {
	ListQueues(context.Context, *sqs.ListQueuesInput, ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error)
	GetQueueAttributes(context.Context, *sqs.GetQueueAttributesInput, ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error)
}

// scanSQS discovers SQS queues in one region. ListQueues returns URLs;
// GetQueueAttributes fetches concurrently the queue ARN (used as the NativeID
// so other services referencing queues by ARN resolve) and attributes
// (KmsMasterKeyId, RedrivePolicy) needed by the resolver.
func scanSQS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := sqs.NewFromConfig(acct.cfg, func(o *sqs.Options) { o.Region = region })
	return scanSQSQueues(ctx, client, acct, region, st, scanID)
}

// scanSQSQueues is the testable scan body: depends only on the narrow sqsAPI
// interface so unit tests inject a stub client without standing up HTTP
// mocks. Top-level scanSQS wires the concrete *sqs.Client.
func scanSQSQueues(ctx context.Context, client sqsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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
				// Include the URL alongside AWS attributes so it stays queryable;
				// AWS uses "QueueUrl" naming elsewhere.
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
