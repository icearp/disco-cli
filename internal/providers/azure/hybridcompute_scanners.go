package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hybridcompute/armhybridcompute"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.hybridcompute",
		fn:   scanHybridCompute,
		emits: []coverage.TypeDecl{
			// Identity → MSI edges resolved centrally; Arc machines / private-link
			// scopes carry no other in-scope ARM-ID reference, so scanner-only.
			{Service: "microsoft.hybridcompute", DiscoType: TypeHybridComputeMachine, Leaf: true},
			{Service: "microsoft.hybridcompute", DiscoType: TypeHybridComputePrivateLinkScope, Leaf: true},
		},
	})
}

// scanHybridCompute discovers Azure Arc-enabled servers (machines) and Arc
// private-link scopes, both sub-wide via armhybridcompute.
func scanHybridCompute(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	mc, err := armhybridcompute.NewMachinesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhybridcompute:NewMachinesClient: %w", err)
	}
	pls, err := armhybridcompute.NewPrivateLinkScopesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhybridcompute:NewPrivateLinkScopesClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armhybridcompute:Machines.ListBySubscription", TypeHybridComputeMachine, sub, st, scanID,
				mc.NewListBySubscriptionPager(nil),
				func(p armhybridcompute.MachinesClientListBySubscriptionResponse) []*armhybridcompute.Machine {
					return p.Value
				},
				func(m *armhybridcompute.Machine) azTrackedBase {
					return azTrackedBase{id: sv(m.ID), name: sv(m.Name), location: sv(m.Location), tags: m.Tags, full: m}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armhybridcompute:PrivateLinkScopes.List", TypeHybridComputePrivateLinkScope, sub, st, scanID,
				pls.NewListPager(nil),
				func(p armhybridcompute.PrivateLinkScopesClientListResponse) []*armhybridcompute.PrivateLinkScope {
					return p.Value
				},
				func(r *armhybridcompute.PrivateLinkScope) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
