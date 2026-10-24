package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	iottypes "github.com/aws/aws-sdk-go-v2/service/iot/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTIndex, Service: "iot", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeIoTJob, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTOTAUpdate, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTStream, Service: "iot"})
}

type iotInventoryAPI interface {
	iotTagsAPI
	ListIndices(context.Context, *iot.ListIndicesInput, ...func(*iot.Options)) (*iot.ListIndicesOutput, error)
	DescribeIndex(context.Context, *iot.DescribeIndexInput, ...func(*iot.Options)) (*iot.DescribeIndexOutput, error)
	ListJobs(context.Context, *iot.ListJobsInput, ...func(*iot.Options)) (*iot.ListJobsOutput, error)
	ListOTAUpdates(context.Context, *iot.ListOTAUpdatesInput, ...func(*iot.Options)) (*iot.ListOTAUpdatesOutput, error)
	ListStreams(context.Context, *iot.ListStreamsInput, ...func(*iot.Options)) (*iot.ListStreamsOutput, error)
}

// scanIoTInventory runs the fleet-index, job, OTA-update and stream phases, adding each to p, and reports
// whether a write failed.
func scanIoTInventory(ctx context.Context, client iotInventoryAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	return runIoTPhases(p,
		func(p *phaseTotals) bool { return scanIoTIndices(ctx, client, acct, region, st, scanID, p) },
		func(p *phaseTotals) bool { return scanIoTJobRecords(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool { return scanIoTOTAUpdates(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool { return scanIoTStreams(ctx, client, acct, region, st, scanID, p, tags) },
	)
}

// iotIndicesPageCap is ListIndices' maxResults cap (1-500).
const iotIndicesPageCap = 500

// scanIoTIndices enumerates fleet-indexing indices (ListIndices returns only
// names) then enriches each with IndexStatus + Schema via DescribeIndex. The
// index has no AWS-issued ARN, so the NativeID is synthesized.
func scanIoTIndices(ctx context.Context, client iotInventoryAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListIndices", "iot indices", "IndexName",
		func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListIndices(ctx, &iot.ListIndicesInput{NextToken: token, MaxResults: sdkaws.Int32(iotIndicesPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.IndexNames, out.NextToken, nil
		},
		iotName, nil)
	d := list.describe(ctx, st, acct, region, "iot:DescribeIndex", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, derr := client.DescribeIndex(ctx, &iot.DescribeIndexInput{IndexName: &name})
			if derr != nil {
				return nil, derr
			}
			iname := sv(out.IndexName)
			arn := iotARN(region, acct.ID, "index", iname)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTIndex,
				NativeID:       arn,
				Name:           &iname,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStore(st, acct, region, p, list, d)
}

// scanIoTJobRecords stores every job ListJobs returns, each with the tags
// iotTagReader reads for it; counts and errors go to p as storeTokenListTags
// says.
func scanIoTJobRecords(ctx context.Context, client iotInventoryAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "iot:ListJobs",
		func(ctx context.Context, token *string) ([]iottypes.JobSummary, *string, error) {
			out, err := client.ListJobs(ctx, &iot.ListJobsInput{NextToken: token, MaxResults: sdkaws.Int32(iotListPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Jobs, out.NextToken, nil
		},
		func(j iottypes.JobSummary) (*store.Resource, string, string) {
			arn := sv(j.JobArn)
			if arn == "" {
				return nil, "jobs have an empty JobArn", cmp.Or(sv(j.JobId), "(a job with no JobId)")
			}
			label := cmp.Or(sv(j.JobId), arn)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTJob, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(j), DiscoveredBy: scanID,
			}, "", ""
		},
		tags.reader(), p)
}

// scanIoTOTAUpdates stores every OTA update ListOTAUpdates returns, each with
// the tags iotTagReader reads for it; counts and errors go to p as
// storeTokenListTags says.
func scanIoTOTAUpdates(ctx context.Context, client iotInventoryAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "iot:ListOTAUpdates",
		func(ctx context.Context, token *string) ([]iottypes.OTAUpdateSummary, *string, error) {
			out, err := client.ListOTAUpdates(ctx, &iot.ListOTAUpdatesInput{NextToken: token, MaxResults: sdkaws.Int32(iotListPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.OtaUpdates, out.NextToken, nil
		},
		func(u iottypes.OTAUpdateSummary) (*store.Resource, string, string) {
			arn := sv(u.OtaUpdateArn)
			if arn == "" {
				return nil, "OTA updates have an empty OtaUpdateArn", cmp.Or(sv(u.OtaUpdateId), "(an OTA update with no OtaUpdateId)")
			}
			label := cmp.Or(sv(u.OtaUpdateId), arn)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTOTAUpdate, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
			}, "", ""
		},
		tags.reader(), p)
}

// scanIoTStreams stores every stream ListStreams returns, each with the tags
// iotTagReader reads for it; counts and errors go to p as storeTokenListTags
// says.
func scanIoTStreams(ctx context.Context, client iotInventoryAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "iot:ListStreams",
		func(ctx context.Context, token *string) ([]iottypes.StreamSummary, *string, error) {
			out, err := client.ListStreams(ctx, &iot.ListStreamsInput{NextToken: token, MaxResults: sdkaws.Int32(iotListPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Streams, out.NextToken, nil
		},
		func(s iottypes.StreamSummary) (*store.Resource, string, string) {
			arn := sv(s.StreamArn)
			if arn == "" {
				return nil, "streams have an empty StreamArn", cmp.Or(sv(s.StreamId), "(a stream with no StreamId)")
			}
			label := cmp.Or(sv(s.StreamId), arn)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTStream, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			}, "", ""
		},
		tags.reader(), p)
}
