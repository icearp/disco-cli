package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/pricingplanmanager"
	ppmtypes "github.com/aws/aws-sdk-go-v2/service/pricingplanmanager/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// pricingPlanManagerRegion is the service's only endpoint: its endpoint rules
// route every region to pricingplanmanager.us-east-1.api.aws, signed for
// us-east-1, and subscription ARNs carry no region segment.
const pricingPlanManagerRegion = "us-east-1"

func init() {
	registerType(restype.Descriptor{Type: TypePricingPlanManagerSubscription, Service: "pricingplanmanager", Untaggable: true})
	registerService(serviceEntry{
		name:   "aws:pricingplanmanager",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
			region, err := acct.homeRegion("pricingplanmanager")
			if err != nil {
				return 0, 0, err
			}
			client := pricingplanmanager.NewFromConfig(acct.cfg, func(o *pricingplanmanager.Options) { o.Region = region })
			return scanPricingPlanManagerSubscriptions(ctx, client, acct, st, scanID)
		},
	})
}

// scanPricingPlanManagerSubscriptions lists the account's flat-rate pricing
// subscriptions. GetSubscription is not called: its Subscription body carries
// no field the list summary lacks.
func scanPricingPlanManagerSubscriptions(ctx context.Context, client pricingplanmanager.ListSubscriptionsAPIClient, acct *account, st *store.Store, scanID string) (int, int, error) {
	region := pricingPlanManagerRegion
	// ListSubscriptionsInput has no page-size field, so there is no cap to set.
	p := pricingplanmanager.NewListSubscriptionsPaginator(client, &pricingplanmanager.ListSubscriptionsInput{},
		func(o *pricingplanmanager.ListSubscriptionsPaginatorOptions) { o.StopOnDuplicateToken = true })
	return pageScan(ctx, "pricingplanmanager:ListSubscriptions", acct, region, st,
		p.HasMorePages,
		func(ctx context.Context) (*pricingplanmanager.ListSubscriptionsOutput, error) { return p.NextPage(ctx) },
		func(page *pricingplanmanager.ListSubscriptionsOutput) []ppmtypes.SubscriptionSummary {
			return page.SubscriptionSummaries
		},
		func(s ppmtypes.SubscriptionSummary) *store.Resource {
			arn := sv(s.Arn)
			if arn == "" {
				return nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePricingPlanManagerSubscription, NativeID: arn,
				Region: regionGlobal, Status: nonEmptyPtr(string(s.Status)), CreatedAt: tp(s.CreatedAt),
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			}
		})
}
