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
			// Custom-location edge wired centrally; the asset ships scanner-only.
			{Service: "microsoft.deviceregistry", DiscoType: TypeDeviceRegistryAsset, Leaf: true},
		},
	})
}

// scanDeviceRegistry discovers Azure Device Registry assets.
func scanDeviceRegistry(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdeviceregistry.NewAssetsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdeviceregistry:NewAssetsClient: %w", err)
	}
	return azSimpleScan(ctx, "armdeviceregistry:Assets.ListBySubscription", TypeDeviceRegistryAsset, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armdeviceregistry.AssetsClientListBySubscriptionResponse) []*armdeviceregistry.Asset {
			return p.Value
		},
		func(a *armdeviceregistry.Asset) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
}
