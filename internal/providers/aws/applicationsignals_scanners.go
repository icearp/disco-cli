package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/applicationsignals"
)

func init() {
	registerService(serviceEntry{
		name: "aws:applicationsignals",
		fn:   scanApplicationSignals,
		emits: []coverage.TypeDecl{
			{Service: "applicationsignals", DiscoType: TypeApplicationSignalsSLO},
		},
	})
}

type applicationSignalsAPI interface {
	ListServiceLevelObjectives(context.Context, *applicationsignals.ListServiceLevelObjectivesInput, ...func(*applicationsignals.Options)) (*applicationsignals.ListServiceLevelObjectivesOutput, error)
}

func scanApplicationSignals(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := applicationsignals.NewFromConfig(acct.cfg, func(o *applicationsignals.Options) { o.Region = region })
	return scanApplicationSignalsSLOs(ctx, client, acct, region, st, scanID)
}

func scanApplicationSignalsSLOs(ctx context.Context, client applicationSignalsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := applicationsignals.NewListServiceLevelObjectivesPaginator(client, &applicationsignals.ListServiceLevelObjectivesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "applicationsignals:ListServiceLevelObjectives", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("applicationsignals:ListServiceLevelObjectives: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.SloSummaries))
		for _, s := range page.SloSummaries {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			name := sv(s.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeApplicationSignalsSLO,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(s.CreatedTime),
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert applicationsignals slos: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
