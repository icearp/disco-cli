package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/scvmm/armscvmm"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.scvmm",
		fn:   scanScVmm,
		emits: []coverage.TypeDecl{
			// VMM server is the Arc-SCVMM root; clouds/availability-sets/templates/
			// networks carry an extendedLocation envelope → custom-location edge
			// wired centrally. All ship Leaf.
			{Service: "microsoft.scvmm", DiscoType: TypeScVmmServer, Leaf: true},
			{Service: "microsoft.scvmm", DiscoType: TypeScVmmCloud, Leaf: true},
			{Service: "microsoft.scvmm", DiscoType: TypeScVmmAvailabilitySet, Leaf: true},
			{Service: "microsoft.scvmm", DiscoType: TypeScVmmVMTemplate, Leaf: true},
			{Service: "microsoft.scvmm", DiscoType: TypeScVmmVirtualNetwork, Leaf: true},
		},
	})
}

// scanScVmm discovers Arc-connected System Center VMM servers plus their
// inventory (clouds, availability sets, VM templates, virtual networks),
// all sub-wide via armscvmm.
func scanScVmm(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	srv, err := armscvmm.NewVmmServersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armscvmm:NewVmmServersClient: %w", err)
	}
	cloud, err := armscvmm.NewCloudsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armscvmm:NewCloudsClient: %w", err)
	}
	avs, err := armscvmm.NewAvailabilitySetsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armscvmm:NewAvailabilitySetsClient: %w", err)
	}
	vt, err := armscvmm.NewVirtualMachineTemplatesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armscvmm:NewVirtualMachineTemplatesClient: %w", err)
	}
	vn, err := armscvmm.NewVirtualNetworksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armscvmm:NewVirtualNetworksClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armscvmm:VmmServers.ListBySubscription", TypeScVmmServer, sub, st, scanID,
				srv.NewListBySubscriptionPager(nil),
				func(p armscvmm.VmmServersClientListBySubscriptionResponse) []*armscvmm.VmmServer { return p.Value },
				func(s *armscvmm.VmmServer) azTrackedBase {
					return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armscvmm:Clouds.ListBySubscription", TypeScVmmCloud, sub, st, scanID,
				cloud.NewListBySubscriptionPager(nil),
				func(p armscvmm.CloudsClientListBySubscriptionResponse) []*armscvmm.Cloud { return p.Value },
				func(r *armscvmm.Cloud) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armscvmm:AvailabilitySets.ListBySubscription", TypeScVmmAvailabilitySet, sub, st, scanID,
				avs.NewListBySubscriptionPager(nil),
				func(p armscvmm.AvailabilitySetsClientListBySubscriptionResponse) []*armscvmm.AvailabilitySet {
					return p.Value
				},
				func(r *armscvmm.AvailabilitySet) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armscvmm:VirtualMachineTemplates.ListBySubscription", TypeScVmmVMTemplate, sub, st, scanID,
				vt.NewListBySubscriptionPager(nil),
				func(p armscvmm.VirtualMachineTemplatesClientListBySubscriptionResponse) []*armscvmm.VirtualMachineTemplate {
					return p.Value
				},
				func(r *armscvmm.VirtualMachineTemplate) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armscvmm:VirtualNetworks.ListBySubscription", TypeScVmmVirtualNetwork, sub, st, scanID,
				vn.NewListBySubscriptionPager(nil),
				func(p armscvmm.VirtualNetworksClientListBySubscriptionResponse) []*armscvmm.VirtualNetwork {
					return p.Value
				},
				func(r *armscvmm.VirtualNetwork) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
