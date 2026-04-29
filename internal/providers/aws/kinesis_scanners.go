package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerService(serviceEntry{
		name: "aws:kinesis",
		fn:   scanKinesis,
		emits: []coverage.TypeDecl{
			{Service: "kinesis", DiscoType: TypeKinesisStream},
		},
	})
}

// kinesisAPI is the narrow set of Kinesis operations called by
// scanKinesisStreams. *kinesis.Client satisfies; tests inject a stub.
type kinesisAPI interface {
	ListStreams(context.Context, *kinesis.ListStreamsInput, ...func(*kinesis.Options)) (*kinesis.ListStreamsOutput, error)
	DescribeStreamSummary(context.Context, *kinesis.DescribeStreamSummaryInput, ...func(*kinesis.Options)) (*kinesis.DescribeStreamSummaryOutput, error)
	ListTagsForStream(context.Context, *kinesis.ListTagsForStreamInput, ...func(*kinesis.Options)) (*kinesis.ListTagsForStreamOutput, error)
}

// scanKinesis discovers Kinesis Data Streams in one region. ListStreams
// returns names; DescribeStreamSummary is called concurrently to fetch
// encryption + metadata without the expensive shard enumeration.
func scanKinesis(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := kinesis.NewFromConfig(acct.cfg, func(o *kinesis.Options) { o.Region = region })
	return scanKinesisStreams(ctx, client, acct, region, st, scanID)
}

// scanKinesisStreams holds the testable scan body.
func scanKinesisStreams(ctx context.Context, client kinesisAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := kinesis.NewListStreamsPaginator(client, &kinesis.ListStreamsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kinesis:ListStreams", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kinesis:ListStreams: %w", err)
		}

		var (
			mu    sync.Mutex
			batch []*store.Resource
		)
		g, gctx := errgroup.WithContext(ctx)
		for _, name := range page.StreamNames {
			g.Go(func() error {
				desc, err := client.DescribeStreamSummary(gctx, &kinesis.DescribeStreamSummaryInput{StreamName: &name})
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return fmt.Errorf("kinesis:DescribeStreamSummary %s: %w", name, err)
				}
				s := desc.StreamDescriptionSummary
				arn := sv(s.StreamARN)
				status := string(s.StreamStatus)
				r := &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeKinesisStream,
					NativeID:       arn,
					Name:           s.StreamName,
					Region:         &region,
					Status:         &status,
					CreatedAt:      tp(s.StreamCreationTimestamp),
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				}
				if tagsOut, tErr := client.ListTagsForStream(gctx, &kinesis.ListTagsForStreamInput{StreamName: &name}); tErr == nil {
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
				return 0, 0, fmt.Errorf("upsert Kinesis streams: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
