package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/iot"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTIndex, Service: "iot", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTJob, Service: "iot", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTOTAUpdate, Service: "iot", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTStream, Service: "iot", Leaf: true})
}

type iotInventoryAPI interface {
	ListIndices(context.Context, *iot.ListIndicesInput, ...func(*iot.Options)) (*iot.ListIndicesOutput, error)
	DescribeIndex(context.Context, *iot.DescribeIndexInput, ...func(*iot.Options)) (*iot.DescribeIndexOutput, error)
	ListJobs(context.Context, *iot.ListJobsInput, ...func(*iot.Options)) (*iot.ListJobsOutput, error)
	ListOTAUpdates(context.Context, *iot.ListOTAUpdatesInput, ...func(*iot.Options)) (*iot.ListOTAUpdatesOutput, error)
	ListStreams(context.Context, *iot.ListStreamsInput, ...func(*iot.Options)) (*iot.ListStreamsOutput, error)
}

func scanIoTInventory(ctx context.Context, client iotInventoryAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanIoTIndices(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTJobRecords(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTOTAUpdates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTStreams(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// scanIoTIndices enumerates fleet-indexing indices (ListIndices returns only
// names) then enriches each with IndexStatus + Schema via DescribeIndex. The
// index has no AWS-issued ARN, so the NativeID is synthesized.
func scanIoTIndices(ctx context.Context, client iotInventoryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListIndicesPaginator(client, &iot.ListIndicesInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListIndices", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListIndices: %w", perr)
		}
		names = append(names, out.IndexNames...)
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeIndex(gctx, &iot.DescribeIndexInput{IndexName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeIndex %s: %w", name, derr)
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
	}, st, "iot indices")
}

func scanIoTJobRecords(ctx context.Context, client iotInventoryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListJobsPaginator(client, &iot.ListJobsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListJobs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListJobs: %w", perr)
		}
		for _, j := range out.Jobs {
			arn := sv(j.JobArn)
			if arn == "" {
				continue
			}
			label := sv(j.JobId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTJob, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(j), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iot jobs")
}

func scanIoTOTAUpdates(ctx context.Context, client iotInventoryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListOTAUpdatesPaginator(client, &iot.ListOTAUpdatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListOTAUpdates", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListOTAUpdates: %w", perr)
		}
		for _, u := range out.OtaUpdates {
			arn := sv(u.OtaUpdateArn)
			if arn == "" {
				continue
			}
			label := sv(u.OtaUpdateId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTOTAUpdate, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iot ota-updates")
}

func scanIoTStreams(ctx context.Context, client iotInventoryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListStreamsPaginator(client, &iot.ListStreamsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListStreams", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListStreams: %w", perr)
		}
		for _, s := range out.Streams {
			arn := sv(s.StreamArn)
			if arn == "" {
				continue
			}
			label := sv(s.StreamId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTStream, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iot streams")
}
