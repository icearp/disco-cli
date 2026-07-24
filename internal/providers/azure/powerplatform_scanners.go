package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/powerplatform/armpowerplatform"
)

func init() {
	registerType(restype.Descriptor{Type: TypePowerPlatformEnterprisePolicy, Service: "microsoft.powerplatform", Leaf: true})
	registerType(restype.Descriptor{Type: TypePowerPlatformAccount, Service: "microsoft.powerplatform", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.powerplatform",
		fn:   scanPowerPlatform,
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
