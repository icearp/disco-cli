package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/standbypool/armstandbypool"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.standbypool",
		fn:   scanStandbyPool,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.standbypool", DiscoType: TypeStandbyVMPool, Leaf: true},
			{Service: "microsoft.standbypool", DiscoType: TypeStandbyContainerGroupPool, Leaf: true},
		},
	})
}

// scanStandbyPool discovers Standby VM pools and Standby Container Group pools.
func scanStandbyPool(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	// Both pool types are scanned regardless of either failing — a hard error
	// on one must not suppress the other. The first non-nil error is returned.
	return azRunPhases(
		func() (int, int, error) {
			vmClient, err := armstandbypool.NewStandbyVirtualMachinePoolsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armstandbypool:NewStandbyVirtualMachinePoolsClient: %w", err)
			}
			return azSimpleScan(ctx, "armstandbypool:StandbyVirtualMachinePools.ListBySubscription", TypeStandbyVMPool, sub, st, scanID,
				vmClient.NewListBySubscriptionPager(nil),
				func(p armstandbypool.StandbyVirtualMachinePoolsClientListBySubscriptionResponse) []*armstandbypool.StandbyVirtualMachinePoolResource {
					return p.Value
				},
				func(r *armstandbypool.StandbyVirtualMachinePoolResource) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			cgClient, err := armstandbypool.NewStandbyContainerGroupPoolsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armstandbypool:NewStandbyContainerGroupPoolsClient: %w", err)
			}
			return azSimpleScan(ctx, "armstandbypool:StandbyContainerGroupPools.ListBySubscription", TypeStandbyContainerGroupPool, sub, st, scanID,
				cgClient.NewListBySubscriptionPager(nil),
				func(p armstandbypool.StandbyContainerGroupPoolsClientListBySubscriptionResponse) []*armstandbypool.StandbyContainerGroupPoolResource {
					return p.Value
				},
				func(r *armstandbypool.StandbyContainerGroupPoolResource) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
