package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/kendraranking"
)

func init() {
	registerService(serviceEntry{
		name: "aws:kendra-ranking",
		fn:   scanKendraRanking,
		emits: []coverage.TypeDecl{
			{Service: "kendra-ranking", DiscoType: TypeKendraRankingExecutionPlan, Leaf: true},
		},
	})
}

// scanKendraRanking discovers Kendra Ranking rescore execution plans. Synth
// ARN: arn:aws:kendra-ranking:{r}:{a}:rescore-execution-plan/{id}.
func scanKendraRanking(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := kendraranking.NewFromConfig(acct.cfg, func(o *kendraranking.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListRescoreExecutionPlans(ctx, &kendraranking.ListRescoreExecutionPlansInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kendra-ranking:ListRescoreExecutionPlans", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kendra-ranking:ListRescoreExecutionPlans: %w", err)
		}
		for _, p := range out.SummaryItems {
			id := sv(p.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:kendra-ranking:%s:%s:rescore-execution-plan/%s", region, acct.ID, id)
			status := string(p.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKendraRankingExecutionPlan, NativeID: arn,
				Name: p.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "kendra-ranking execution-plans")
}
