package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/elementalinference"
)

func init() {
	registerService(serviceEntry{
		name: "aws:elemental-inference",
		fn:   scanElementalInference,
		emits: []coverage.TypeDecl{
			{Service: "elemental-inference", DiscoType: TypeElementalInferenceFeed, Leaf: true},
		},
	})
}

// scanElementalInference discovers Elemental Inference feeds.
func scanElementalInference(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := elementalinference.NewFromConfig(acct.cfg, func(o *elementalinference.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListFeeds(ctx, &elementalinference.ListFeedsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "elemental-inference:ListFeeds", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("elemental-inference:ListFeeds: %w", err)
		}
		for _, f := range out.Feeds {
			arn := sv(f.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeElementalInferenceFeed, NativeID: arn,
				Name: f.Name, Region: &region,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "elemental-inference feeds")
}
