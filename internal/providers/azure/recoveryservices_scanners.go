package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/recoveryservices/armrecoveryservices"
)

func init() {
	registerService(serviceEntry{
		name: "azure:recoveryservices",
		fn:   scanRecoveryServices,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.recoveryservices", DiscoType: TypeRecoveryServicesVault},
		},
	})
}

// scanRecoveryServices discovers Recovery Services vaults (Azure Backup +
// Site Recovery).
func scanRecoveryServices(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armrecoveryservices.NewVaultsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armrecoveryservices:NewVaultsClient: %w", err)
	}
	return azSimpleScan(ctx, "armrecoveryservices:Vaults.ListBySubscriptionID", TypeRecoveryServicesVault, sub, st, scanID,
		client.NewListBySubscriptionIDPager(nil),
		func(p armrecoveryservices.VaultsClientListBySubscriptionIDResponse) []*armrecoveryservices.Vault {
			return p.Value
		},
		func(v *armrecoveryservices.Vault) azTrackedBase {
			return azTrackedBase{id: sv(v.ID), name: sv(v.Name), location: sv(v.Location), tags: v.Tags, full: v}
		})
}
