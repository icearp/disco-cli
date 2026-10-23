package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/datapipeline"
	dptypes "github.com/aws/aws-sdk-go-v2/service/datapipeline/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDataPipelinePipeline, Service: "datapipeline"})
	registerService(serviceEntry{
		name: "aws:datapipeline",
		fn:   scanDataPipeline,
	})
}

const (
	dataPipelineListOp     = "datapipeline:ListPipelines"
	dataPipelineDescribeOp = "datapipeline:DescribePipelines"
	// dataPipelineDescribeBatch is the most ids DescribePipelines accepts in
	// one call (the model's pipelineIds doc: "as many as 25").
	dataPipelineDescribeBatch = 25
)

type dataPipelineAPI interface {
	ListPipelines(context.Context, *datapipeline.ListPipelinesInput, ...func(*datapipeline.Options)) (*datapipeline.ListPipelinesOutput, error)
	DescribePipelines(context.Context, *datapipeline.DescribePipelinesInput, ...func(*datapipeline.Options)) (*datapipeline.DescribePipelinesOutput, error)
}

// scanDataPipeline discovers AWS Data Pipeline pipelines. The service is closed
// to new customers (2024); accounts that never onboarded can't self-enable it,
// so the scanner marks it not-entitled → (account: not entitled).
func scanDataPipeline(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := datapipeline.NewFromConfig(acct.cfg, func(o *datapipeline.Options) { o.Region = region })
	return scanDataPipelinePipelines(ctx, client, acct, region, st, scanID)
}

// scanDataPipelinePipelines lists the pipeline ids, then stores each
// pipeline's DescribePipelines description. ListPipelines returns ids and
// names only, so a pipeline whose description is not read has no row to keep:
// a failed list page keeps the pipelines of the pages read before it, and a
// failed describe batch costs only its own pipelines. A denial is warned about
// once per op; any other error is returned after the rows are stored.
func scanDataPipelinePipelines(ctx context.Context, client dataPipelineAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed, listErr := readTokenPages(ctx, func(ctx context.Context, marker *string) ([]dptypes.PipelineIdName, *string, error) {
		out, err := client.ListPipelines(ctx, &datapipeline.ListPipelinesInput{Marker: marker})
		if err != nil {
			return nil, nil, err
		}
		return out.PipelineIdList, out.Marker, nil
	})
	if len(listed) == 0 && isClosedToNewCustomers(listErr) {
		return 0, 0, markServiceNotEntitled(listErr)
	}

	var (
		skips notStoredSkips
		ids   []string
	)
	for _, p := range listed {
		if id := sv(p.Id); id != "" {
			ids = append(ids, id)
		} else {
			skips.skipped("pipelines have an empty Id", cmp.Or(sv(p.Name), "one without a Name"))
		}
	}
	var describeSkips notStoredSkips
	rows, describeDenied, describeErr := describeDataPipelines(ctx, client, acct, region, scanID, ids, &describeSkips)

	var listDenied error
	if isAccessDenied(listErr) {
		listDenied, listErr = listErr, nil
	}
	total, inserted, err := upsertListed(st, rows, dataPipelineListOp, listErr)
	err = errors.Join(err, describeErr)
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, err
	}
	if listDenied != nil {
		_ = skipIfAccessDenied(st, dataPipelineListOp, acct.ID, region, listDenied)
	}
	if describeDenied.count > 0 {
		_ = skipIfAccessDenied(st, dataPipelineDescribeOp, acct.ID, region,
			fmt.Errorf("%d batch(es) denied, first starting at %s: %w", describeDenied.count, describeDenied.first, describeDenied.err))
	}
	skips.warn(st, dataPipelineListOp, acct, region)
	describeSkips.warn(st, dataPipelineDescribeOp, acct, region)
	return total, inserted, err
}

// dataPipelineGoneCodes are the codes DescribePipelines models for a pipeline
// deleted since it was listed.
var dataPipelineGoneCodes = []string{"PipelineNotFoundException", "PipelineDeletedException"}

// describeDataPipelines describes ids in batches of dataPipelineDescribeBatch
// and builds a row per description. A denied batch is tallied under its first
// id; any other failed batch is returned in the joined error. A batch that
// fails with one of dataPipelineGoneCodes is described again one id at a
// time, because the error does not say which id it names; only the ids that
// fail with such a code alone are skipped, as deleted since listed.
func describeDataPipelines(ctx context.Context, client dataPipelineAPI, acct *account, region, scanID string, ids []string, skips *notStoredSkips) ([]*store.Resource, *skipTally, error) {
	var (
		rows     []*store.Resource
		denied   skipTally
		errs     []error
		describe func(batch []string)
	)
	// describeIfLive reports whether ctx still allows describing; once it is
	// done, the ids left are recorded as not described.
	describeIfLive := func(left []string) bool {
		if err := ctx.Err(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %d pipeline(s) from %s on not described: %w", dataPipelineDescribeOp, len(left), left[0], err))
			return false
		}
		return true
	}
	describe = func(batch []string) {
		out, err := client.DescribePipelines(ctx, &datapipeline.DescribePipelinesInput{PipelineIds: batch})
		switch {
		case err == nil:
		case isAPIErrorCode(err, dataPipelineGoneCodes...) && len(batch) == 1:
			return
		case isAPIErrorCode(err, dataPipelineGoneCodes...):
			for i, id := range batch {
				if !describeIfLive(batch[i:]) {
					return
				}
				describe([]string{id})
			}
			return
		case isAccessDenied(err):
			denied.skipped(batch[0], err)
			return
		default:
			errs = append(errs, fmt.Errorf("%s %s (batch of %d): %w", dataPipelineDescribeOp, batch[0], len(batch), err))
			return
		}
		rows = buildListedRows(out.PipelineDescriptionList, func(p dptypes.PipelineDescription) (*store.Resource, string, string) {
			id := sv(p.PipelineId)
			if id == "" {
				return nil, "described pipelines have an empty PipelineId", cmp.Or(sv(p.Name), "one without a Name")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataPipelinePipeline, NativeID: fmt.Sprintf("arn:aws:datapipeline:%s:%s:pipeline/%s", region, acct.ID, id),
				Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), TagsJSON: kvTagsJSON(p.Tags, dataPipelineTagKV), DiscoveredBy: scanID,
			}, "", ""
		}, rows, skips)
	}
	for start := 0; start < len(ids); start += dataPipelineDescribeBatch {
		if !describeIfLive(ids[start:]) {
			break
		}
		describe(ids[start:min(start+dataPipelineDescribeBatch, len(ids))])
	}
	return rows, &denied, errors.Join(errs...)
}

func dataPipelineTagKV(t dptypes.Tag) (key, value *string) { return t.Key, t.Value }
