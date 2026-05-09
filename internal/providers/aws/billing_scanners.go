package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/billing"
	billingtypes "github.com/aws/aws-sdk-go-v2/service/billing/types"
)

// AWS Billing API is global — endpoints resolve only via us-east-1.
const billingRegion = "us-east-1"

func init() {
	registerService(serviceEntry{
		name:   "aws:billing",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
			client := billing.NewFromConfig(acct.cfg, func(o *billing.Options) { o.Region = billingRegion })
			return scanBillingViews(ctx, client, acct, st, scanID)
		},
		emits: []coverage.TypeDecl{
			{Service: "billing", DiscoType: TypeBillingView, Leaf: true},
		},
	})
}

// billingAPI is the narrow surface scanBillingViews uses. ListBillingViews is
// paginator-native and returns BillingViewListElement summaries that already
// carry the canonical ARN — no Get fan-out needed for identity.
type billingAPI interface {
	ListBillingViews(context.Context, *billing.ListBillingViewsInput, ...func(*billing.Options)) (*billing.ListBillingViewsOutput, error)
}

func scanBillingViews(ctx context.Context, client billingAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	region := billingRegion
	pager := billing.NewListBillingViewsPaginator(client, &billing.ListBillingViewsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "billing:ListBillingViews", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("billing:ListBillingViews: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(out.BillingViews))
		for _, bv := range out.BillingViews {
			arn := sv(bv.Arn)
			if arn == "" {
				continue
			}
			name := sv(bv.Name)
			batch = append(batch, &store.Resource{
				Provider:    "aws",
				AccountID:   acct.ID,
				AccountName: &acct.Name,
				Type:        TypeBillingView,
				NativeID:    arn,
				Name:        &name,
				Region:      regionGlobal,
				// AWS-supplied default billing view carries BillingViewType="Primary";
				// customer-created views carry "Custom".
				ManagedByProvider: bv.BillingViewType == billingtypes.BillingViewTypePrimary,
				AttributesJSON:    mustJSON(bv),
				DiscoveredBy:      scanID,
			})
		}
		if len(batch) > 0 {
			n, uerr := st.UpsertResources(batch)
			if uerr != nil {
				return total, inserted, fmt.Errorf("upsert billing views: %w", uerr)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
