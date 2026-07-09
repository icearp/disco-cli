package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/connectedvmware/armconnectedvmware"
)

func init() {
	registerType(restype.Descriptor{Type: TypeConnectedVMwareVCenter, Service: "microsoft.connectedvmwarevsphere", Leaf: true, Redact: []redact.Rule{{Path: "properties.credentials.password", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeConnectedVMwareCluster, Service: "microsoft.connectedvmwarevsphere", Leaf: true})
	registerType(restype.Descriptor{Type: TypeConnectedVMwareDatastore, Service: "microsoft.connectedvmwarevsphere", Leaf: true})
	registerType(restype.Descriptor{Type: TypeConnectedVMwareHost, Service: "microsoft.connectedvmwarevsphere", Leaf: true})
	registerType(restype.Descriptor{Type: TypeConnectedVMwareResourcePool, Service: "microsoft.connectedvmwarevsphere", Leaf: true})
	registerType(restype.Descriptor{Type: TypeConnectedVMwareVMTemplate, Service: "microsoft.connectedvmwarevsphere", Leaf: true})
	registerType(restype.Descriptor{Type: TypeConnectedVMwareVirtualNetwork, Service: "microsoft.connectedvmwarevsphere", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.connectedvmwarevsphere",
		fn:   scanConnectedVMware,
	})
}

// scanConnectedVMware discovers Arc-connected VMware vCenters plus their
// inventory resources (clusters, datastores, hosts, resource pools, VM
// templates, virtual networks), all sub-wide via armconnectedvmware.
func scanConnectedVMware(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	vc, err := armconnectedvmware.NewVCentersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armconnectedvmware:NewVCentersClient: %w", err)
	}
	cl, err := armconnectedvmware.NewClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armconnectedvmware:NewClustersClient: %w", err)
	}
	ds, err := armconnectedvmware.NewDatastoresClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armconnectedvmware:NewDatastoresClient: %w", err)
	}
	ho, err := armconnectedvmware.NewHostsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armconnectedvmware:NewHostsClient: %w", err)
	}
	rp, err := armconnectedvmware.NewResourcePoolsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armconnectedvmware:NewResourcePoolsClient: %w", err)
	}
	vt, err := armconnectedvmware.NewVirtualMachineTemplatesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armconnectedvmware:NewVirtualMachineTemplatesClient: %w", err)
	}
	vn, err := armconnectedvmware.NewVirtualNetworksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armconnectedvmware:NewVirtualNetworksClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armconnectedvmware:VCenters.List", TypeConnectedVMwareVCenter, sub, st, scanID,
				vc.NewListPager(nil),
				func(p armconnectedvmware.VCentersClientListResponse) []*armconnectedvmware.VCenter { return p.Value },
				func(v *armconnectedvmware.VCenter) azTrackedBase {
					return azTrackedBase{id: sv(v.ID), name: sv(v.Name), location: sv(v.Location), tags: v.Tags, full: v}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armconnectedvmware:Clusters.List", TypeConnectedVMwareCluster, sub, st, scanID,
				cl.NewListPager(nil),
				func(p armconnectedvmware.ClustersClientListResponse) []*armconnectedvmware.Cluster { return p.Value },
				func(r *armconnectedvmware.Cluster) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armconnectedvmware:Datastores.List", TypeConnectedVMwareDatastore, sub, st, scanID,
				ds.NewListPager(nil),
				func(p armconnectedvmware.DatastoresClientListResponse) []*armconnectedvmware.Datastore {
					return p.Value
				},
				func(r *armconnectedvmware.Datastore) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armconnectedvmware:Hosts.List", TypeConnectedVMwareHost, sub, st, scanID,
				ho.NewListPager(nil),
				func(p armconnectedvmware.HostsClientListResponse) []*armconnectedvmware.Host { return p.Value },
				func(r *armconnectedvmware.Host) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armconnectedvmware:ResourcePools.List", TypeConnectedVMwareResourcePool, sub, st, scanID,
				rp.NewListPager(nil),
				func(p armconnectedvmware.ResourcePoolsClientListResponse) []*armconnectedvmware.ResourcePool {
					return p.Value
				},
				func(r *armconnectedvmware.ResourcePool) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armconnectedvmware:VirtualMachineTemplates.List", TypeConnectedVMwareVMTemplate, sub, st, scanID,
				vt.NewListPager(nil),
				func(p armconnectedvmware.VirtualMachineTemplatesClientListResponse) []*armconnectedvmware.VirtualMachineTemplate {
					return p.Value
				},
				func(r *armconnectedvmware.VirtualMachineTemplate) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armconnectedvmware:VirtualNetworks.List", TypeConnectedVMwareVirtualNetwork, sub, st, scanID,
				vn.NewListPager(nil),
				func(p armconnectedvmware.VirtualNetworksClientListResponse) []*armconnectedvmware.VirtualNetwork {
					return p.Value
				},
				func(r *armconnectedvmware.VirtualNetwork) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
