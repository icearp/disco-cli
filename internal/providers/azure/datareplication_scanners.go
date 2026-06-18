package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/recoveryservicesdatareplication/armrecoveryservicesdatareplication"
)

func init() {
	registerService(serviceEntry{
		name: "azure:datareplication",
		fn:   scanDataReplication,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.datareplication", DiscoType: TypeDataReplicationFabric, Leaf: true},
			{Service: "microsoft.datareplication", DiscoType: TypeDataReplicationVault, Leaf: true},
		},
	})
}

// scanDataReplication discovers Azure Site Recovery (data replication)
// fabrics and replication vaults.
func scanDataReplication(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	fabricClient, err := armrecoveryservicesdatareplication.NewFabricClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armrecoveryservicesdatareplication:NewFabricClient: %w", err)
	}
	total, inserted, err = azSimpleScan(ctx, "armrecoveryservicesdatareplication:Fabric.ListBySubscription", TypeDataReplicationFabric, sub, st, scanID,
		fabricClient.NewListBySubscriptionPager(nil),
		func(p armrecoveryservicesdatareplication.FabricClientListBySubscriptionResponse) []*armrecoveryservicesdatareplication.FabricModel {
			return p.Value
		},
		func(f *armrecoveryservicesdatareplication.FabricModel) azTrackedBase {
			return azTrackedBase{id: sv(f.ID), name: sv(f.Name), location: sv(f.Location), tags: f.Tags, full: f}
		})
	if err != nil {
		return total, inserted, err
	}

	vaultClient, err := armrecoveryservicesdatareplication.NewVaultClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armrecoveryservicesdatareplication:NewVaultClient: %w", err)
	}
	vt, vi, err := azSimpleScan(ctx, "armrecoveryservicesdatareplication:Vault.ListBySubscription", TypeDataReplicationVault, sub, st, scanID,
		vaultClient.NewListBySubscriptionPager(nil),
		func(p armrecoveryservicesdatareplication.VaultClientListBySubscriptionResponse) []*armrecoveryservicesdatareplication.VaultModel {
			return p.Value
		},
		func(v *armrecoveryservicesdatareplication.VaultModel) azTrackedBase {
			return azTrackedBase{id: sv(v.ID), name: sv(v.Name), location: sv(v.Location), tags: v.Tags, full: v}
		})
	total += vt
	inserted += vi
	return total, inserted, err
}
