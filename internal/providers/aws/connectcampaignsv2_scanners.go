package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/connectcampaignsv2"
)

func init() {
	registerService(serviceEntry{
		name: "aws:connect-campaigns-v2",
		fn:   scanConnectCampaignsV2,
		emits: []coverage.TypeDecl{
			{Service: "connect-campaigns-v2", DiscoType: TypeConnectCampaignsV2Campaign, Leaf: true},
		},
	})
}

// scanConnectCampaignsV2 discovers Connect Campaigns v2 campaigns.
func scanConnectCampaignsV2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := connectcampaignsv2.NewFromConfig(acct.cfg, func(o *connectcampaignsv2.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListCampaigns(ctx, &connectcampaignsv2.ListCampaignsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "connect-campaigns-v2:ListCampaigns", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("connect-campaigns-v2:ListCampaigns: %w", err)
		}
		for _, c := range out.CampaignSummaryList {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeConnectCampaignsV2Campaign, NativeID: arn,
				Name: c.Name, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "connect-campaigns-v2 campaigns")
}
