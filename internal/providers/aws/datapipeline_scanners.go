package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/datapipeline"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDataPipelinePipeline, Service: "datapipeline", Leaf: true})
	registerService(serviceEntry{
		name: "aws:datapipeline",
		fn:   scanDataPipeline,
	})
}

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

func scanDataPipelinePipelines(ctx context.Context, client dataPipelineAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var ids []string
	pager := datapipeline.NewListPipelinesPaginator(client, &datapipeline.ListPipelinesInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isClosedToNewCustomers(perr) {
				return 0, 0, markServiceNotEntitled(perr)
			}
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "datapipeline:ListPipelines", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("datapipeline:ListPipelines: %w", perr)
		}
		for _, p := range out.PipelineIdList {
			if id := sv(p.Id); id != "" {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return 0, 0, nil
	}

	var batch []*store.Resource
	// DescribePipelines accepts up to 25 IDs per call.
	for i := 0; i < len(ids); i += 25 {
		end := min(i+25, len(ids))
		out, derr := client.DescribePipelines(ctx, &datapipeline.DescribePipelinesInput{PipelineIds: ids[i:end]})
		if derr != nil {
			if isAccessDenied(derr) {
				return 0, 0, skipIfAccessDenied(st, "datapipeline:DescribePipelines", acct.ID, region, derr)
			}
			return 0, 0, fmt.Errorf("datapipeline:DescribePipelines: %w", derr)
		}
		for _, p := range out.PipelineDescriptionList {
			id := sv(p.PipelineId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:datapipeline:%s:%s:pipeline/%s", region, acct.ID, id)
			name := sv(p.Name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataPipelinePipeline, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "datapipeline pipelines")
}
