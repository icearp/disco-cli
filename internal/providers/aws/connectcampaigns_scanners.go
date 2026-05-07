package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/connectcampaigns"
)

func init() {
	registerService(serviceEntry{
		name: "aws:connect-campaigns",
		fn:   scanConnectCampaigns,
		emits: []coverage.TypeDecl{
			{Service: "connect-campaigns", DiscoType: TypeConnectCampaignsCampaign},
		},
	})
}

// scanConnectCampaigns discovers Connect Campaigns v1 campaigns.
func scanConnectCampaigns(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := connectcampaigns.NewFromConfig(acct.cfg, func(o *connectcampaigns.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListCampaigns(ctx, &connectcampaigns.ListCampaignsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "connect-campaigns:ListCampaigns", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("connect-campaigns:ListCampaigns: %w", err)
		}
		for _, c := range out.CampaignSummaryList {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeConnectCampaignsCampaign, NativeID: arn,
				Name: c.Name, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "connect-campaigns campaigns")
}
