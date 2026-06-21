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
		name: "azure:microsoft.powerbidedicated",
		fn:   scanPowerBIDedicated,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.powerbidedicated", DiscoType: TypePowerBIDedicatedCapacity, Leaf: true},
			{Service: "microsoft.powerbidedicated", DiscoType: TypePowerBIDedicatedAutoScaleVCore, Leaf: true},
		},
	})
}

// scanPowerBIDedicated discovers Power BI Dedicated capacities and autoscale v-cores.
func scanPowerBIDedicated(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	caps, err := armpowerbidedicated.NewCapacitiesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpowerbidedicated:NewCapacitiesClient: %w", err)
	}
	vcores, err := armpowerbidedicated.NewAutoScaleVCoresClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpowerbidedicated:NewAutoScaleVCoresClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armpowerbidedicated:Capacities.List", TypePowerBIDedicatedCapacity, sub, st, scanID,
				caps.NewListPager(nil),
				func(p armpowerbidedicated.CapacitiesClientListResponse) []*armpowerbidedicated.DedicatedCapacity {
					return p.Value
				},
				func(c *armpowerbidedicated.DedicatedCapacity) azTrackedBase {
					return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armpowerbidedicated:AutoScaleVCores.ListBySubscription", TypePowerBIDedicatedAutoScaleVCore, sub, st, scanID,
				vcores.NewListBySubscriptionPager(nil),
				func(p armpowerbidedicated.AutoScaleVCoresClientListBySubscriptionResponse) []*armpowerbidedicated.AutoScaleVCore {
					return p.Value
				},
				func(r *armpowerbidedicated.AutoScaleVCore) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
