package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appconfiguration/armappconfiguration"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAppConfigurationStore, Service: "microsoft.appconfiguration"})
	registerService(serviceEntry{
		name: "azure:microsoft.appconfiguration",
		fn:   scanAppConfiguration,
	})
}

// scanAppConfiguration discovers Azure App Configuration stores.
func scanAppConfiguration(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armappconfiguration.NewConfigurationStoresClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armappconfiguration:NewConfigurationStoresClient: %w", err)
	}
	return azSimpleScan(ctx, "armappconfiguration:ConfigurationStores.List", TypeAppConfigurationStore, sub, st, scanID,
		client.NewListPager(nil),
		func(p armappconfiguration.ConfigurationStoresClientListResponse) []*armappconfiguration.ConfigurationStore {
			return p.Value
		},
		func(c *armappconfiguration.ConfigurationStore) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
}
