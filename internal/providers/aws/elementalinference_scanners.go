package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/elementalinference"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeElementalInferenceFeed, Service: "elemental-inference", Upstream: "AWS::ElementalInference::Feed", Leaf: true})
	registerService(serviceEntry{
		name: "aws:elemental-inference",
		fn:   scanElementalInference,
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
