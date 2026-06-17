package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/powerbidedicated/armpowerbidedicated"
)

func init() {
	registerService(serviceEntry{
		name: "azure:powerbidedicated",
		fn:   scanPowerBIDedicated,
		emits: []coverage.TypeDecl{
			// Capacities carry no in-scope ARM-ID reference, so this ships
			// scanner-only.
			{Service: "microsoft.powerbidedicated", DiscoType: TypePowerBIDedicatedCapacity, Leaf: true},
		},
	})
}

// scanPowerBIDedicated discovers Power BI Dedicated (Embedded) capacities.
func scanPowerBIDedicated(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armpowerbidedicated.NewCapacitiesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpowerbidedicated:NewCapacitiesClient: %w", err)
	}
	return azSimpleScan(ctx, "armpowerbidedicated:Capacities.List", TypePowerBIDedicatedCapacity, sub, st, scanID,
		client.NewListPager(nil),
		func(p armpowerbidedicated.CapacitiesClientListResponse) []*armpowerbidedicated.DedicatedCapacity {
			return p.Value
		},
		func(c *armpowerbidedicated.DedicatedCapacity) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
}
