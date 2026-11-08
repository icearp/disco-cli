package aws

import (
	"context"
	"maps"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/kinesisvideo"
	kvstypes "github.com/aws/aws-sdk-go-v2/service/kinesisvideo/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

const (
	kvsListStreamsOp           = "kinesis-video:ListStreams"
	kvsListSignalingChannelsOp = "kinesis-video:ListSignalingChannels"
	kvsStreamTagsOp            = "kinesis-video:ListTagsForStream"
	kvsChannelTagsOp           = "kinesis-video:ListTagsForResource"
	// kvsPageCap is the MaxResults range maximum (1-10000) the model gives
	// ListStreams and ListSignalingChannels.
	kvsPageCap int32 = 10000
	// kvsThrottleCode is Kinesis Video Streams' modeled throttle ("you have
	// exceeded the limit of allowed client calls. Try making the call
	// later"), an HTTP 400 that every op disco calls lists. The SDK retryer
	// recognises neither the code nor the status.
	kvsThrottleCode = "ClientLimitExceededException"
)

// kvsNotFound is the not-found code ListTagsForStream and ListTagsForResource
// model: the resource was deleted after it was listed.
var kvsNotFound = []string{"ResourceNotFoundException"}

func init() {
	registerType(restype.Descriptor{Type: TypeKinesisVideoStream, Service: "kinesis-video"})
	registerType(restype.Descriptor{Type: TypeKinesisVideoSignalingChannel, Service: "kinesis-video"})
	registerService(serviceEntry{
		name: "aws:kinesis-video",
		fn:   scanKinesisVideo,
	})
}

type kinesisVideoAPI interface {
	ListStreams(context.Context, *kinesisvideo.ListStreamsInput, ...func(*kinesisvideo.Options)) (*kinesisvideo.ListStreamsOutput, error)
	ListSignalingChannels(context.Context, *kinesisvideo.ListSignalingChannelsInput, ...func(*kinesisvideo.Options)) (*kinesisvideo.ListSignalingChannelsOutput, error)
	ListTagsForStream(context.Context, *kinesisvideo.ListTagsForStreamInput, ...func(*kinesisvideo.Options)) (*kinesisvideo.ListTagsForStreamOutput, error)
	ListTagsForResource(context.Context, *kinesisvideo.ListTagsForResourceInput, ...func(*kinesisvideo.Options)) (*kinesisvideo.ListTagsForResourceOutput, error)
}

// scanKinesisVideo discovers Kinesis Video Streams streams and signaling
// channels, each with its tags (kvsStreamTags, kvsChannelTags). Both listings
// run as phases, one at a time and in that order: a phase's error does not
// stop the later one; a failed write and ctx's end do, and so does a stream
// phase that stores nothing and fails only with NXDOMAIN (runHostPhases).
// Each listing is written a page at a time, and each tag op's denials are
// one warning for the whole scan.
func scanKinesisVideo(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := kinesisvideo.NewFromConfig(acct.cfg, kvsClientOptions(region))
	return runHostPhases(ctx, st, acct, region, "kinesis-video", 0, []hostPhase{
		{op: kvsListStreamsOp, run: func(p *phaseTotals) bool {
			return scanKVStreams(ctx, client, acct, region, st, scanID, kvsStreamTags(client), p)
		}},
		{op: kvsListSignalingChannelsOp, run: func(p *phaseTotals) bool {
			return scanKVSignalingChannels(ctx, client, acct, region, st, scanID, kvsChannelTags(client), p)
		}},
	})
}

// kvsClientOptions pins the region and retries kvsThrottleCode, which would
// otherwise fail a throttled call, a tag read of the concurrent fan-out
// included, on its first attempt. The code is retried with the retryer's
// standard backoff only: AddWithErrorCodes changes which errors are
// retryable, not which count as throttles, so adaptive mode's client-side
// rate limiting does not learn from it.
func kvsClientOptions(region string) func(*kinesisvideo.Options) {
	return func(o *kinesisvideo.Options) {
		o.Region = region
		o.Retryer = retry.AddWithErrorCodes(o.Retryer, kvsThrottleCode)
	}
}

// kvsStreamTags reads a stream's tags with ListTagsForStream, sent the row's
// NativeID: the StreamARN its listed element carries. The Service Reference
// lists stream for the op and gives it aws:ResourceTag as a condition key,
// and CreateStream takes Tags; no listed element carries them. The SDK
// documents ListTagsForResource for signaling channels only, so streams use
// their own op although the Service Reference lists stream there too.
func kvsStreamTags(client kinesisVideoAPI) rowTagReader {
	return kvsPagedTags(kvsStreamTagsOp, func(ctx context.Context, arn string, token *string) (map[string]string, *string, error) {
		out, err := client.ListTagsForStream(ctx, &kinesisvideo.ListTagsForStreamInput{StreamARN: sdkaws.String(arn), NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		return out.Tags, out.NextToken, nil
	})
}

// kvsChannelTags reads a signaling channel's tags with ListTagsForResource,
// sent the row's NativeID: the ChannelARN its listed element carries. The
// Service Reference lists channel for the op and gives it aws:ResourceTag as
// a condition key, and CreateSignalingChannel takes Tags; no listed element
// carries them.
func kvsChannelTags(client kinesisVideoAPI) rowTagReader {
	return kvsPagedTags(kvsChannelTagsOp, func(ctx context.Context, arn string, token *string) (map[string]string, *string, error) {
		out, err := client.ListTagsForResource(ctx, &kinesisvideo.ListTagsForResourceInput{ResourceARN: sdkaws.String(arn), NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		return out.Tags, out.NextToken, nil
	})
}

// kvsPagedTags is the rowTagReader for op, whose page returns one page of the
// tags of the resource arn names. Both tag ops page their tags with NextToken
// and take no page size; every page is read, and a failed page or a repeated
// NextToken fails the read. kvsNotFound drops a resource deleted after it was
// listed. Neither listing can return another account's resources (the
// service has no sharing), and the model has no AWS-owned stream or channel
// (no row is ManagedByProvider), so every row is read, a DELETING one
// included: whether a tag read of a resource being deleted succeeds is
// unverified; if it answers ResourceNotFoundException, the row is dropped as
// deleted.
func kvsPagedTags(op string, page func(ctx context.Context, arn string, token *string) (map[string]string, *string, error)) rowTagReader {
	return rowTagReader{
		op:       op,
		notFound: kvsNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			pages, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]map[string]string, *string, error) {
				tags, next, err := page(ctx, r.NativeID, token)
				if err != nil {
					return nil, nil, err
				}
				return []map[string]string{tags}, next, nil
			})
			if err != nil {
				return nil, err
			}
			tags := map[string]string{}
			for _, p := range pages {
				maps.Copy(tags, p)
			}
			return mapTagsJSON(tags), nil
		},
	}
}

// scanKVStreams stores the streams, each page written as soon as its rows'
// tags are read. It reports whether a write failed, after which the scan must
// stop.
func scanKVStreams(ctx context.Context, client kinesisVideoAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, kvsListStreamsOp,
		func(ctx context.Context, token *string) ([]kvstypes.StreamInfo, *string, error) {
			out, err := client.ListStreams(ctx, &kinesisvideo.ListStreamsInput{MaxResults: sdkaws.Int32(kvsPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.StreamInfoList, out.NextToken, nil
		},
		func(s kvstypes.StreamInfo) (*store.Resource, string, string) {
			status := string(s.Status)
			return arnRow("streams", s.StreamARN, s.StreamName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKinesisVideoStream, NativeID: sv(s.StreamARN),
				Name: s.StreamName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanKVSignalingChannels stores the signaling channels, each page written as
// soon as its rows' tags are read. It reports whether a write failed, after
// which the scan must stop.
func scanKVSignalingChannels(ctx context.Context, client kinesisVideoAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, kvsListSignalingChannelsOp,
		func(ctx context.Context, token *string) ([]kvstypes.ChannelInfo, *string, error) {
			out, err := client.ListSignalingChannels(ctx, &kinesisvideo.ListSignalingChannelsInput{MaxResults: sdkaws.Int32(kvsPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ChannelInfoList, out.NextToken, nil
		},
		func(c kvstypes.ChannelInfo) (*store.Resource, string, string) {
			status := string(c.ChannelStatus)
			return arnRow("signaling channels", c.ChannelARN, c.ChannelName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKinesisVideoSignalingChannel, NativeID: sv(c.ChannelARN),
				Name: c.ChannelName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}, tags, p)
}
