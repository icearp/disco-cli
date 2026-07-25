package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/recoveryservices/armrecoveryservices"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRecoveryServicesVault, Service: "microsoft.recoveryservices"})
	registerService(serviceEntry{
		name: "azure:microsoft.recoveryservices",
		fn:   scanRecoveryServices,
	})
}

// scanRecoveryServices discovers Recovery Services vaults (Azure Backup +
// Site Recovery).
func scanRecoveryServices(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
