package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/standbypool/armstandbypool"
)

func init() {
	registerType(restype.Descriptor{Type: TypeStandbyVMPool, Service: "microsoft.standbypool", Leaf: true})
	registerType(restype.Descriptor{Type: TypeStandbyContainerGroupPool, Service: "microsoft.standbypool", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.standbypool",
		fn:   scanStandbyPool,
	})
}

// scanStandbyPool discovers Standby VM pools and Standby Container Group pools.
func scanStandbyPool(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	// Both pool types scan regardless of either failing — one's hard error
	// must not suppress the other. azRunPhases returns the first non-nil error.
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
