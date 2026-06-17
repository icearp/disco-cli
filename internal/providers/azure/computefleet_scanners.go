package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet"
)

func init() {
	registerService(serviceEntry{
		name: "azure:azurefleet",
		fn:   scanComputeFleet,
		emits: []coverage.TypeDecl{
			// Identity → MSI edges resolved centrally; the fleet ships
			// scanner-only.
			{Service: "microsoft.azurefleet", DiscoType: TypeComputeFleet, Leaf: true},
		},
	})
}

// scanComputeFleet discovers Azure Compute Fleets.
func scanComputeFleet(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcomputefleet.NewFleetsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcomputefleet:NewFleetsClient: %w", err)
	}
	return azSimpleScan(ctx, "armcomputefleet:Fleets.ListBySubscription", TypeComputeFleet, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armcomputefleet.FleetsClientListBySubscriptionResponse) []*armcomputefleet.Fleet {
			return p.Value
		},
		func(f *armcomputefleet.Fleet) azTrackedBase {
			return azTrackedBase{id: sv(f.ID), name: sv(f.Name), location: sv(f.Location), tags: f.Tags, full: f}
		})
}
