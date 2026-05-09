package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/kinesisvideo"
)

func init() {
	registerService(serviceEntry{
		name: "aws:kinesis-video",
		fn:   scanKinesisVideo,
		emits: []coverage.TypeDecl{
			{Service: "kinesis-video", DiscoType: TypeKinesisVideoStream, Leaf: true},
			{Service: "kinesis-video", DiscoType: TypeKinesisVideoSignalingChannel, Leaf: true},
		},
	})
}

type kinesisVideoAPI interface {
	ListStreams(context.Context, *kinesisvideo.ListStreamsInput, ...func(*kinesisvideo.Options)) (*kinesisvideo.ListStreamsOutput, error)
	ListSignalingChannels(context.Context, *kinesisvideo.ListSignalingChannelsInput, ...func(*kinesisvideo.Options)) (*kinesisvideo.ListSignalingChannelsOutput, error)
}

// scanKinesisVideo discovers Kinesis Video Streams and signaling channels.
func scanKinesisVideo(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := kinesisvideo.NewFromConfig(acct.cfg, func(o *kinesisvideo.Options) { o.Region = region })

	t, i, ferr := scanKVStreams(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanKVSignalingChannels(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanKVStreams(ctx context.Context, client kinesisVideoAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListStreams(ctx, &kinesisvideo.ListStreamsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kinesis-video:ListStreams", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kinesis-video:ListStreams: %w", err)
		}
		for _, s := range out.StreamInfoList {
			arn := sv(s.StreamARN)
			if arn == "" {
				continue
			}
			status := string(s.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKinesisVideoStream, NativeID: arn,
				Name: s.StreamName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "kinesis-video streams")
}

func scanKVSignalingChannels(ctx context.Context, client kinesisVideoAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListSignalingChannels(ctx, &kinesisvideo.ListSignalingChannelsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kinesis-video:ListSignalingChannels", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kinesis-video:ListSignalingChannels: %w", err)
		}
		for _, c := range out.ChannelInfoList {
			arn := sv(c.ChannelARN)
			if arn == "" {
				continue
			}
			status := string(c.ChannelStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKinesisVideoSignalingChannel, NativeID: arn,
				Name: c.ChannelName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "kinesis-video signaling-channels")
}
