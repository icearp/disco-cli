package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dataprotection/armdataprotection"
)

func init() {
	registerService(serviceEntry{
		name: "azure:dataprotection",
		fn:   scanDataProtection,
		emits: []coverage.TypeDecl{
			// Identity → MSI edges resolve centrally; no other single-hop
			// outbound edge from the vault itself, so it ships as a leaf.
			{Service: "microsoft.dataprotection", DiscoType: TypeDataProtectionBackupVault, Leaf: true},
		},
	})
}

// scanDataProtection discovers Azure Backup (Data Protection) backup vaults.
func scanDataProtection(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdataprotection.NewBackupVaultsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdataprotection:NewBackupVaultsClient: %w", err)
	}
	return azSimpleScan(ctx, "armdataprotection:BackupVaults.GetInSubscription", TypeDataProtectionBackupVault, sub, st, scanID,
		client.NewGetInSubscriptionPager(nil),
		func(p armdataprotection.BackupVaultsClientGetInSubscriptionResponse) []*armdataprotection.BackupVaultResource {
			return p.Value
		},
		func(v *armdataprotection.BackupVaultResource) azTrackedBase {
			return azTrackedBase{id: sv(v.ID), name: sv(v.Name), location: sv(v.Location), tags: v.Tags, full: v}
		})
}
