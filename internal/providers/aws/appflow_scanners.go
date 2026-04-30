package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/appflow"
)

func init() {
	registerService(serviceEntry{
		name: "aws:appflow",
		fn:   scanAppFlow,
		emits: []coverage.TypeDecl{
			{Service: "appflow", DiscoType: TypeAppFlowFlow},
		},
	})
}

// appflowAPI is the narrow surface scanAppFlowFlows uses. ListFlows returns
// FlowDefinition entries with full body; no Describe fan-out needed.
type appflowAPI interface {
	ListFlows(context.Context, *appflow.ListFlowsInput, ...func(*appflow.Options)) (*appflow.ListFlowsOutput, error)
}

func scanAppFlow(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := appflow.NewFromConfig(acct.cfg, func(o *appflow.Options) { o.Region = region })
	return scanAppFlowFlows(ctx, client, acct, region, st, scanID)
}

func scanAppFlowFlows(ctx context.Context, client appflowAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := appflow.NewListFlowsPaginator(client, &appflow.ListFlowsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "appflow:ListFlows", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("appflow:ListFlows: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.Flows))
		for _, f := range page.Flows {
			arn := sv(f.FlowArn)
			if arn == "" {
				continue
			}
			name := sv(f.FlowName)
			status := string(f.FlowStatus)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppFlowFlow,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(f.CreatedAt),
				AttributesJSON: mustJSON(f),
				TagsJSON:       mapTagsJSON(f.Tags),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert appflow flows: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
