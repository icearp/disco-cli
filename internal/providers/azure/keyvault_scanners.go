package azure

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
)

func init() { registerService(serviceEntry{name: "azure:keyvault", fn: scanKeyVault}) }

// scanKeyVault discovers Azure Key Vault vaults.
func scanKeyVault(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armkeyvault.NewVaultsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armkeyvault:NewVaultsClient: %w", err)
	}
	return azPageScan(ctx, "armkeyvault:Vaults.ListBySubscription", sub, st,
		client.NewListBySubscriptionPager(nil),
		func(page armkeyvault.VaultsClientListBySubscriptionResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, vault := range page.Value {
				if vault.ID == nil {
					continue
				}
				name, loc := sv(vault.Name), sv(vault.Location)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeKeyVaultVault, NativeID: sv(vault.ID),
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(vault.Tags), AttributesJSON: mustJSON(vault),
					DiscoveredBy: scanID,
				})
			}
			return batch, nil
		})
}
