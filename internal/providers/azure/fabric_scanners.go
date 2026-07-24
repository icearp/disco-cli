package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/fabric/armfabric"
)

func init() {
	registerType(restype.Descriptor{Type: TypeFabricCapacity, Service: "microsoft.fabric", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.fabric",
		fn:   scanFabric,
	})
}

// scanFabric discovers fabric resources.
func scanFabric(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armfabric.NewCapacitiesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armfabric:NewCapacitiesClient: %w", err)
	}
	return azSimpleScan(ctx, "armfabric:Capacities.ListBySubscription", TypeFabricCapacity, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armfabric.CapacitiesClientListBySubscriptionResponse) []*armfabric.Capacity {
			return p.Value
		},
		func(r *armfabric.Capacity) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
