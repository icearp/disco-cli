package azure

import (
	"context"
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
			// App-group/workspace/scaling-plan are resolver sources (see
			// desktopvirtualization_resolvers.go) — not Leaf.
			{Service: "microsoft.desktopvirtualization", DiscoType: TypeDVCHostPool, Leaf: true},
			{Service: "microsoft.desktopvirtualization", DiscoType: TypeDVCApplicationGroup},
			{Service: "microsoft.desktopvirtualization", DiscoType: TypeDVCWorkspace},
			{Service: "microsoft.desktopvirtualization", DiscoType: TypeDVCScalingPlan},
			{Service: "microsoft.desktopvirtualization", DiscoType: TypeDesktopVirtAppAttachPackage, Leaf: true},
		},
	})
}

// scanDesktopVirtualization discovers Azure Virtual Desktop host pools,
// application groups, workspaces, scaling plans, and app-attach packages.
func scanDesktopVirtualization(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	hp, err := armdesktopvirtualization.NewHostPoolsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdesktopvirtualization:NewHostPoolsClient: %w", err)
	}
	ag, err := armdesktopvirtualization.NewApplicationGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdesktopvirtualization:NewApplicationGroupsClient: %w", err)
	}
	ws, err := armdesktopvirtualization.NewWorkspacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdesktopvirtualization:NewWorkspacesClient: %w", err)
	}
	sp, err := armdesktopvirtualization.NewScalingPlansClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdesktopvirtualization:NewScalingPlansClient: %w", err)
	}
	aap, err := armdesktopvirtualization.NewAppAttachPackageClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdesktopvirtualization:NewAppAttachPackageClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdesktopvirtualization:HostPools.List", TypeDVCHostPool, sub, st, scanID,
				hp.NewListPager(nil),
				func(p armdesktopvirtualization.HostPoolsClientListResponse) []*armdesktopvirtualization.HostPool {
					return p.Value
				},
				func(h *armdesktopvirtualization.HostPool) azTrackedBase {
					return azTrackedBase{id: sv(h.ID), name: sv(h.Name), location: sv(h.Location), tags: h.Tags, full: h}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdesktopvirtualization:ApplicationGroups.ListBySubscription", TypeDVCApplicationGroup, sub, st, scanID,
				ag.NewListBySubscriptionPager(nil),
				func(p armdesktopvirtualization.ApplicationGroupsClientListBySubscriptionResponse) []*armdesktopvirtualization.ApplicationGroup {
					return p.Value
				},
				func(a *armdesktopvirtualization.ApplicationGroup) azTrackedBase {
					return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdesktopvirtualization:Workspaces.ListBySubscription", TypeDVCWorkspace, sub, st, scanID,
				ws.NewListBySubscriptionPager(nil),
				func(p armdesktopvirtualization.WorkspacesClientListBySubscriptionResponse) []*armdesktopvirtualization.Workspace {
					return p.Value
				},
				func(w *armdesktopvirtualization.Workspace) azTrackedBase {
					return azTrackedBase{id: sv(w.ID), name: sv(w.Name), location: sv(w.Location), tags: w.Tags, full: w}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdesktopvirtualization:ScalingPlans.ListBySubscription", TypeDVCScalingPlan, sub, st, scanID,
				sp.NewListBySubscriptionPager(nil),
				func(p armdesktopvirtualization.ScalingPlansClientListBySubscriptionResponse) []*armdesktopvirtualization.ScalingPlan {
					return p.Value
				},
				func(s *armdesktopvirtualization.ScalingPlan) azTrackedBase {
					return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdesktopvirtualization:AppAttachPackage.ListBySubscription", TypeDesktopVirtAppAttachPackage, sub, st, scanID,
				aap.NewListBySubscriptionPager(nil),
				func(p armdesktopvirtualization.AppAttachPackageClientListBySubscriptionResponse) []*armdesktopvirtualization.AppAttachPackage {
					return p.Value
				},
				func(r *armdesktopvirtualization.AppAttachPackage) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
