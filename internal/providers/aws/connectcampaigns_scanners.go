package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/connectcampaigns"
)

func init() {
	registerType(restype.Descriptor{Type: TypeConnectCampaignsCampaign, Service: "connect-campaigns", Upstream: "AWS::ConnectCampaigns::Campaign", Leaf: true})
	registerService(serviceEntry{
		name: "aws:connect-campaigns",
		fn:   scanConnectCampaigns,
	})
}

// connectCampaignsNotProvisioned reports the denials Connect Campaigns (v1/v2)
// return when no Connect instance backs the account/region: null-action
// AccessDenied ("not authorized to perform: null") or bare ForbiddenException
// ("Forbidden"). Environmental state, not a real IAM denial — callers silent-skip.
func connectCampaignsNotProvisioned(err error) bool {
	return isAccessDeniedWithMessage(err, "not authorized to perform: null") ||
		isAPIErrorWithMessage(err, "ForbiddenException", "Forbidden")
}

// scanConnectCampaigns discovers Connect Campaigns v1 campaigns.
func scanConnectCampaigns(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := connectcampaigns.NewFromConfig(acct.cfg, func(o *connectcampaigns.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListCampaigns(ctx, &connectcampaigns.ListCampaignsInput{NextToken: nextToken})
		if err != nil {
			if connectCampaignsNotProvisioned(err) {
				return 0, 0, nil
			}
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
