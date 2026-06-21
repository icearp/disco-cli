package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.keyvault",
		fn:   scanKeyVault,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.keyvault", DiscoType: TypeKeyVaultVault},
			{Service: "microsoft.keyvault", DiscoType: TypeKeyVaultManagedHSM, Leaf: true},
		},
	})
}

// scanKeyVault discovers Azure Key Vault vaults and Managed HSM pools.
func scanKeyVault(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) {
			client, err := armkeyvault.NewVaultsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armkeyvault:NewVaultsClient: %w", err)
			}
			return azSimpleScan(ctx, "armkeyvault:Vaults.ListBySubscription", TypeKeyVaultVault, sub, st, scanID,
				client.NewListBySubscriptionPager(nil),
				func(p armkeyvault.VaultsClientListBySubscriptionResponse) []*armkeyvault.Vault { return p.Value },
				func(v *armkeyvault.Vault) azTrackedBase {
					return azTrackedBase{id: sv(v.ID), name: sv(v.Name), location: sv(v.Location), tags: v.Tags, full: v}
				})
		},
		func() (int, int, error) {
			hsmClient, err := armkeyvault.NewManagedHsmsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armkeyvault:NewManagedHsmsClient: %w", err)
			}
			return azSimpleScan(ctx, "armkeyvault:ManagedHsms.ListBySubscription", TypeKeyVaultManagedHSM, sub, st, scanID,
				hsmClient.NewListBySubscriptionPager(nil),
				func(p armkeyvault.ManagedHsmsClientListBySubscriptionResponse) []*armkeyvault.ManagedHsm {
					return p.Value
				},
				func(h *armkeyvault.ManagedHsm) azTrackedBase {
					return azTrackedBase{id: sv(h.ID), name: sv(h.Name), location: sv(h.Location), tags: h.Tags, full: h}
				})
		},
	)
}
