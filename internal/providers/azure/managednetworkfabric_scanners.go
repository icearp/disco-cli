package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/managednetworkfabric/armmanagednetworkfabric"
)

func init() {
	registerService(serviceEntry{
		name: "azure:managednetworkfabric",
		fn:   scanManagedNetworkFabric,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.managednetworkfabric", DiscoType: TypeManagedNetworkFabric, Leaf: true},
		},
	})
}

// scanManagedNetworkFabric discovers Azure Managed Network Fabrics.
func scanManagedNetworkFabric(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armmanagednetworkfabric.NewNetworkFabricsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewNetworkFabricsClient: %w", err)
	}
	return azSimpleScan(ctx, "armmanagednetworkfabric:NetworkFabrics.ListBySubscription", TypeManagedNetworkFabric, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armmanagednetworkfabric.NetworkFabricsClientListBySubscriptionResponse) []*armmanagednetworkfabric.NetworkFabric {
			return p.Value
		},
		func(f *armmanagednetworkfabric.NetworkFabric) azTrackedBase {
			return azTrackedBase{id: sv(f.ID), name: sv(f.Name), location: sv(f.Location), tags: f.Tags, full: f}
		})
}
