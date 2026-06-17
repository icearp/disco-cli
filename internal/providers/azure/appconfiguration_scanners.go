package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appconfiguration/armappconfiguration"
)

func init() {
	registerService(serviceEntry{
		name: "azure:appconfiguration",
		fn:   scanAppConfiguration,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.appconfiguration", DiscoType: TypeAppConfigurationStore},
		},
	})
}

// scanAppConfiguration discovers Azure App Configuration stores.
func scanAppConfiguration(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
