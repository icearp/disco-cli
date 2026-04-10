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
	client, err := armkeyvault.NewVaultsClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armkeyvault:NewVaultsClient: %w", err)
	}

	pager := client.NewListBySubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armkeyvault:Vaults.ListBySubscription", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armkeyvault:Vaults.ListBySubscription: %w", err)
		}
		var batch []*store.Resource
		for _, vault := range page.Value {
			if vault.ID == nil {
				continue
			}
			name := sv(vault.Name)
			location := sv(vault.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeKeyVaultVault,
				NativeID:       sv(vault.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(vault),
				DiscoveredBy:   scanID,
			}
			if vault.Tags != nil {
				s := mustJSON(vault.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Key Vaults: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
