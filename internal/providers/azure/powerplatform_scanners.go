package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/powerplatform/armpowerplatform"
)

func init() {
	registerService(serviceEntry{
		name: "azure:powerplatform",
		fn:   scanPowerPlatform,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.powerplatform", DiscoType: TypePowerPlatformEnterprisePolicy, Leaf: true},
		},
	})
}

// scanPowerPlatform discovers powerplatform resources.
func scanPowerPlatform(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armpowerplatform.NewEnterprisePoliciesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpowerplatform:NewEnterprisePoliciesClient: %w", err)
	}
	return azSimpleScan(ctx, "armpowerplatform:EnterprisePolicies.ListBySubscription", TypePowerPlatformEnterprisePolicy, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armpowerplatform.EnterprisePoliciesClientListBySubscriptionResponse) []*armpowerplatform.EnterprisePolicy {
			return p.Value
		},
		func(r *armpowerplatform.EnterprisePolicy) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
