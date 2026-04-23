package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/firehose"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:firehose", fn: scanFirehose}) }

// scanFirehose discovers Kinesis Firehose delivery streams in one region.
// ListDeliveryStreams returns names (paginated via ExclusiveStartDeliveryStreamName);
// DescribeDeliveryStream is called concurrently per name for full config
// including destinations and encryption.
func scanFirehose(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := firehose.NewFromConfig(acct.cfg, func(o *firehose.Options) { o.Region = region })

	var exclusiveStart *string
	for {
		out, err := client.ListDeliveryStreams(ctx, &firehose.ListDeliveryStreamsInput{
			ExclusiveStartDeliveryStreamName: exclusiveStart,
		})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "firehose:ListDeliveryStreams", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("firehose:ListDeliveryStreams: %w", err)
		}

		var (
			mu    sync.Mutex
			batch []*store.Resource
		)
		g, gctx := errgroup.WithContext(ctx)
		for _, name := range out.DeliveryStreamNames {
			g.Go(func() error {
				desc, err := client.DescribeDeliveryStream(gctx, &firehose.DescribeDeliveryStreamInput{DeliveryStreamName: &name})
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return fmt.Errorf("firehose:DescribeDeliveryStream %s: %w", name, err)
				}
				d := desc.DeliveryStreamDescription
				arn := sv(d.DeliveryStreamARN)
				status := string(d.DeliveryStreamStatus)
				r := &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeFirehoseDeliveryStream,
					NativeID:       arn,
					Name:           d.DeliveryStreamName,
					Region:         &region,
					Status:         &status,
					CreatedAt:      tp(d.CreateTimestamp),
					AttributesJSON: mustJSON(d),
					DiscoveredBy:   scanID,
				}
				// Firehose tag listing is paginated via ExclusiveStartTagKey.
				if tagsOut, tErr := client.ListTagsForDeliveryStream(gctx, &firehose.ListTagsForDeliveryStreamInput{DeliveryStreamName: &name}); tErr == nil {
					r.TagsJSON = awsTagsJSON(tagsOut.Tags)
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
				return 0, 0, fmt.Errorf("upsert Firehose delivery streams: %w", err)
			}
			total += len(batch)
			inserted += n
		}

		if out.HasMoreDeliveryStreams == nil || !*out.HasMoreDeliveryStreams || len(out.DeliveryStreamNames) == 0 {
			break
		}
		last := out.DeliveryStreamNames[len(out.DeliveryStreamNames)-1]
		exclusiveStart = &last
	}
	return total, inserted, nil
}
