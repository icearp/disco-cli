package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeKinesisStream, Service: "kinesis"})
	registerType(restype.Descriptor{Type: TypeKinesisStreamConsumer, Service: "kinesis"})
	registerType(restype.Descriptor{Type: TypeKinesisResourcePolicy, Service: "kinesis"})
	registerType(restype.Descriptor{Type: TypeKinesisChannel, Service: "kinesis"})
	registerService(serviceEntry{
		name: "aws:kinesis",
		fn:   scanKinesis,
	})
}

// kinesisAPI is the narrow set of Kinesis operations called by
// scanKinesisStreams. *kinesis.Client satisfies; tests inject a stub.
type kinesisAPI interface {
	ListStreams(context.Context, *kinesis.ListStreamsInput, ...func(*kinesis.Options)) (*kinesis.ListStreamsOutput, error)
	DescribeStreamSummary(context.Context, *kinesis.DescribeStreamSummaryInput, ...func(*kinesis.Options)) (*kinesis.DescribeStreamSummaryOutput, error)
	ListTagsForStream(context.Context, *kinesis.ListTagsForStreamInput, ...func(*kinesis.Options)) (*kinesis.ListTagsForStreamOutput, error)
	ListStreamConsumers(context.Context, *kinesis.ListStreamConsumersInput, ...func(*kinesis.Options)) (*kinesis.ListStreamConsumersOutput, error)
	GetResourcePolicy(context.Context, *kinesis.GetResourcePolicyInput, ...func(*kinesis.Options)) (*kinesis.GetResourcePolicyOutput, error)
	ListChannels(context.Context, *kinesis.ListChannelsInput, ...func(*kinesis.Options)) (*kinesis.ListChannelsOutput, error)
}

// scanKinesis discovers Kinesis Data Streams, their enhanced-fan-out consumers,
// the resource policies on streams and consumers, and channels in one region.
//
// The three phases are independent: the extended phase lists the streams
// itself. Every phase runs whatever another phase returns; the counts are
// summed and the phase errors joined, so one failing phase never costs
// another's rows.
func scanKinesis(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := kinesis.NewFromConfig(acct.cfg, func(o *kinesis.Options) { o.Region = region })
	var errs []error
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanKinesisStreams(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanKinesisExtended(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanKinesisChannels(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		total += t
		inserted += i
		errs = append(errs, perr)
	}
	return total, inserted, errors.Join(errs...)
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
