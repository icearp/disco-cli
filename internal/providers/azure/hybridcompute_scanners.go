package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hybridcompute/armhybridcompute"
)

func init() {
	registerService(serviceEntry{
		name: "azure:hybridcompute",
		fn:   scanHybridCompute,
		emits: []coverage.TypeDecl{
			// Identity → MSI edges resolved centrally; Arc machines carry no
			// other in-scope ARM-ID reference, so this ships scanner-only.
			{Service: "microsoft.hybridcompute", DiscoType: TypeHybridComputeMachine, Leaf: true},
		},
	})
}

// scanHybridCompute discovers Azure Arc-enabled servers (machines).
func scanHybridCompute(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armhybridcompute.NewMachinesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhybridcompute:NewMachinesClient: %w", err)
	}
	return azSimpleScan(ctx, "armhybridcompute:Machines.ListBySubscription", TypeHybridComputeMachine, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armhybridcompute.MachinesClientListBySubscriptionResponse) []*armhybridcompute.Machine {
			return p.Value
		},
		func(m *armhybridcompute.Machine) azTrackedBase {
			return azTrackedBase{id: sv(m.ID), name: sv(m.Name), location: sv(m.Location), tags: m.Tags, full: m}
		})
}
