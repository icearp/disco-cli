package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/powerplatform/armpowerplatform"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.powerplatform",
		fn:   scanPowerPlatform,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.powerplatform", DiscoType: TypePowerPlatformEnterprisePolicy, Leaf: true},
			{Service: "microsoft.powerplatform", DiscoType: TypePowerPlatformAccount, Leaf: true},
		},
	})
}

// scanPowerPlatform discovers Power Platform enterprise policies and accounts.
func scanPowerPlatform(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	policies, err := armpowerplatform.NewEnterprisePoliciesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpowerplatform:NewEnterprisePoliciesClient: %w", err)
	}
	accounts, err := armpowerplatform.NewAccountsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpowerplatform:NewAccountsClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armpowerplatform:EnterprisePolicies.ListBySubscription", TypePowerPlatformEnterprisePolicy, sub, st, scanID,
				policies.NewListBySubscriptionPager(nil),
				func(p armpowerplatform.EnterprisePoliciesClientListBySubscriptionResponse) []*armpowerplatform.EnterprisePolicy {
					return p.Value
				},
				func(r *armpowerplatform.EnterprisePolicy) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armpowerplatform:Accounts.ListBySubscription", TypePowerPlatformAccount, sub, st, scanID,
				accounts.NewListBySubscriptionPager(nil),
				func(p armpowerplatform.AccountsClientListBySubscriptionResponse) []*armpowerplatform.Account {
					return p.Value
				},
				func(r *armpowerplatform.Account) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
