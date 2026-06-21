package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/workloads/armworkloads"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.workloads",
		fn:   scanWorkloads,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.workloads", DiscoType: TypeWorkloadsSAPVirtualInstance, Leaf: true},
			{Service: "microsoft.workloads", DiscoType: TypeWorkloadsMonitor, Leaf: true},
		},
	})
}

// scanWorkloads discovers SAP virtual instances and SAP/Az Monitor monitors.
func scanWorkloads(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	svi, err := armworkloads.NewSAPVirtualInstancesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armworkloads:NewSAPVirtualInstancesClient: %w", err)
	}
	monitors, err := armworkloads.NewMonitorsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armworkloads:NewMonitorsClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armworkloads:SAPVirtualInstances.ListBySubscription", TypeWorkloadsSAPVirtualInstance, sub, st, scanID,
				svi.NewListBySubscriptionPager(nil),
				func(p armworkloads.SAPVirtualInstancesClientListBySubscriptionResponse) []*armworkloads.SAPVirtualInstance {
					return p.Value
				},
				func(i *armworkloads.SAPVirtualInstance) azTrackedBase {
					return azTrackedBase{id: sv(i.ID), name: sv(i.Name), location: sv(i.Location), tags: i.Tags, full: i}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armworkloads:Monitors.List", TypeWorkloadsMonitor, sub, st, scanID,
				monitors.NewListPager(nil),
				func(p armworkloads.MonitorsClientListResponse) []*armworkloads.Monitor { return p.Value },
				func(r *armworkloads.Monitor) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
