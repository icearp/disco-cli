package azure

import (
	"context"
	"errors"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/desktopvirtualization/armdesktopvirtualization/v2"
)

func init() {
	registerService(serviceEntry{
		name: "azure:desktopvirtualization",
		fn:   scanDesktopVirtualization,
		emits: []coverage.TypeDecl{
			// host-pool is a resolver target only (Leaf); the other three are
			// resolver sources (application-group/workspace/scaling-plan).
			{Service: "microsoft.desktopvirtualization", DiscoType: TypeDVCHostPool, Leaf: true},
			{Service: "microsoft.desktopvirtualization", DiscoType: TypeDVCApplicationGroup},
			{Service: "microsoft.desktopvirtualization", DiscoType: TypeDVCWorkspace},
			{Service: "microsoft.desktopvirtualization", DiscoType: TypeDVCScalingPlan},
		},
	})
}

// scanDesktopVirtualization discovers Azure Virtual Desktop host pools,
// application groups, workspaces, and scaling plans.
func scanDesktopVirtualization(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	hpClient, err := armdesktopvirtualization.NewHostPoolsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdesktopvirtualization:NewHostPoolsClient: %w", err)
	}
	agClient, err := armdesktopvirtualization.NewApplicationGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdesktopvirtualization:NewApplicationGroupsClient: %w", err)
	}
	wsClient, err := armdesktopvirtualization.NewWorkspacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdesktopvirtualization:NewWorkspacesClient: %w", err)
	}
	spClient, err := armdesktopvirtualization.NewScalingPlansClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdesktopvirtualization:NewScalingPlansClient: %w", err)
	}

	t1, i1, e1 := azSimpleScan(ctx, "armdesktopvirtualization:HostPools.List", TypeDVCHostPool, sub, st, scanID,
		hpClient.NewListPager(nil),
		func(p armdesktopvirtualization.HostPoolsClientListResponse) []*armdesktopvirtualization.HostPool {
			return p.Value
		},
		func(h *armdesktopvirtualization.HostPool) azTrackedBase {
			return azTrackedBase{id: sv(h.ID), name: sv(h.Name), location: sv(h.Location), tags: h.Tags, full: h}
		})
	t2, i2, e2 := azSimpleScan(ctx, "armdesktopvirtualization:ApplicationGroups.ListBySubscription", TypeDVCApplicationGroup, sub, st, scanID,
		agClient.NewListBySubscriptionPager(nil),
		func(p armdesktopvirtualization.ApplicationGroupsClientListBySubscriptionResponse) []*armdesktopvirtualization.ApplicationGroup {
			return p.Value
		},
		func(a *armdesktopvirtualization.ApplicationGroup) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
	t3, i3, e3 := azSimpleScan(ctx, "armdesktopvirtualization:Workspaces.ListBySubscription", TypeDVCWorkspace, sub, st, scanID,
		wsClient.NewListBySubscriptionPager(nil),
		func(p armdesktopvirtualization.WorkspacesClientListBySubscriptionResponse) []*armdesktopvirtualization.Workspace {
			return p.Value
		},
		func(w *armdesktopvirtualization.Workspace) azTrackedBase {
			return azTrackedBase{id: sv(w.ID), name: sv(w.Name), location: sv(w.Location), tags: w.Tags, full: w}
		})
	t4, i4, e4 := azSimpleScan(ctx, "armdesktopvirtualization:ScalingPlans.ListBySubscription", TypeDVCScalingPlan, sub, st, scanID,
		spClient.NewListBySubscriptionPager(nil),
		func(p armdesktopvirtualization.ScalingPlansClientListBySubscriptionResponse) []*armdesktopvirtualization.ScalingPlan {
			return p.Value
		},
		func(s *armdesktopvirtualization.ScalingPlan) azTrackedBase {
			return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
		})
	return t1 + t2 + t3 + t4, i1 + i2 + i3 + i4, errors.Join(e1, e2, e3, e4)
}
