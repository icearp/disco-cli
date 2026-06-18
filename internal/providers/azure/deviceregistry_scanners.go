package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/deviceregistry/armdeviceregistry"
)

func init() {
	registerService(serviceEntry{
		name: "azure:deviceregistry",
		fn:   scanDeviceRegistry,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.deviceregistry", DiscoType: TypeDeviceRegistryAsset, Leaf: true},
			{Service: "microsoft.deviceregistry", DiscoType: TypeDeviceRegistryAssetEndpointProfile, Leaf: true},
			// Billing containers are auto-materialised + non-deletable.
			{Service: "microsoft.deviceregistry", DiscoType: TypeDeviceRegistryBillingContainer, Leaf: true},
		},
	})
}

// scanDeviceRegistry discovers Device Registry assets, asset endpoint profiles,
// and billing containers.
func scanDeviceRegistry(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	assets, err := armdeviceregistry.NewAssetsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdeviceregistry:NewAssetsClient: %w", err)
	}
	aeps, err := armdeviceregistry.NewAssetEndpointProfilesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdeviceregistry:NewAssetEndpointProfilesClient: %w", err)
	}
	bcs, err := armdeviceregistry.NewBillingContainersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdeviceregistry:NewBillingContainersClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdeviceregistry:Assets.ListBySubscription", TypeDeviceRegistryAsset, sub, st, scanID,
				assets.NewListBySubscriptionPager(nil),
				func(p armdeviceregistry.AssetsClientListBySubscriptionResponse) []*armdeviceregistry.Asset {
					return p.Value
				},
				func(a *armdeviceregistry.Asset) azTrackedBase {
					return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdeviceregistry:AssetEndpointProfiles.ListBySubscription", TypeDeviceRegistryAssetEndpointProfile, sub, st, scanID,
				aeps.NewListBySubscriptionPager(nil),
				func(p armdeviceregistry.AssetEndpointProfilesClientListBySubscriptionResponse) []*armdeviceregistry.AssetEndpointProfile {
					return p.Value
				},
				func(r *armdeviceregistry.AssetEndpointProfile) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdeviceregistry:BillingContainers.ListBySubscription", TypeDeviceRegistryBillingContainer, sub, st, scanID,
				bcs.NewListBySubscriptionPager(nil),
				func(p armdeviceregistry.BillingContainersClientListBySubscriptionResponse) []*armdeviceregistry.BillingContainer {
					return p.Value
				},
				func(r *armdeviceregistry.BillingContainer) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), managed: true, full: r}
				})
		},
	)
}
