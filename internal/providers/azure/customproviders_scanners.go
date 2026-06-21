package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/customproviders/armcustomproviders"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.customproviders",
		fn:   scanCustomProviders,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.customproviders", DiscoType: TypeCustomProvidersResourceProvider},
		},
	})
}

// scanCustomProviders discovers Microsoft.CustomProviders resource providers
// (custom RP manifests).
func scanCustomProviders(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcustomproviders.NewCustomResourceProviderClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcustomproviders:NewCustomResourceProviderClient: %w", err)
	}
	return azSimpleScan(ctx, "armcustomproviders:CustomResourceProvider.ListBySubscription", TypeCustomProvidersResourceProvider, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armcustomproviders.CustomResourceProviderClientListBySubscriptionResponse) []*armcustomproviders.CustomRPManifest {
			return p.Value
		},
		func(r *armcustomproviders.CustomRPManifest) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
