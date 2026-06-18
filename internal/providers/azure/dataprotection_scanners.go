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
			// outbound edge, so both ship as leaves.
			{Service: "microsoft.dataprotection", DiscoType: TypeDataProtectionBackupVault, Leaf: true},
			{Service: "microsoft.dataprotection", DiscoType: TypeDataProtectionResourceGuard, Leaf: true},
		},
	})
}

// scanDataProtection discovers Azure Backup (Data Protection) backup vaults and
// resource guards.
func scanDataProtection(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	vaults, err := armdataprotection.NewBackupVaultsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdataprotection:NewBackupVaultsClient: %w", err)
	}
	guards, err := armdataprotection.NewResourceGuardsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdataprotection:NewResourceGuardsClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdataprotection:BackupVaults.GetInSubscription", TypeDataProtectionBackupVault, sub, st, scanID,
				vaults.NewGetInSubscriptionPager(nil),
				func(p armdataprotection.BackupVaultsClientGetInSubscriptionResponse) []*armdataprotection.BackupVaultResource {
					return p.Value
				},
				func(v *armdataprotection.BackupVaultResource) azTrackedBase {
					return azTrackedBase{id: sv(v.ID), name: sv(v.Name), location: sv(v.Location), tags: v.Tags, full: v}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdataprotection:ResourceGuards.GetResourcesInSubscription", TypeDataProtectionResourceGuard, sub, st, scanID,
				guards.NewGetResourcesInSubscriptionPager(nil),
				func(p armdataprotection.ResourceGuardsClientGetResourcesInSubscriptionResponse) []*armdataprotection.ResourceGuardResource {
					return p.Value
				},
				func(r *armdataprotection.ResourceGuardResource) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
