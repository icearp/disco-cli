package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/osis"
)

func init() {
	registerService(serviceEntry{
		name: "aws:osis",
		fn:   scanOSIS,
		emits: []coverage.TypeDecl{
			{Service: "osis", DiscoType: TypeOSISPipeline},
		},
	})
}

// scanOSIS discovers OpenSearch Ingestion Service (OSIS) pipelines.
func scanOSIS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := osis.NewFromConfig(acct.cfg, func(o *osis.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListPipelines(ctx, &osis.ListPipelinesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "osis:ListPipelines", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("osis:ListPipelines: %w", err)
		}
		for _, p := range out.Pipelines {
			arn := sv(p.PipelineArn)
			if arn == "" {
				continue
			}
			status := string(p.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOSISPipeline, NativeID: arn,
				Name: p.PipelineName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "osis pipelines")
}
