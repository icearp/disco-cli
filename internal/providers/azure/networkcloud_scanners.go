package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/networkcloud/armnetworkcloud"
)

func init() {
	registerType(restype.Descriptor{Type: TypeNetworkCloudCluster, Service: "microsoft.networkcloud", Leaf: true, Redact: []redact.Rule{{Path: "properties.aggregatorOrSingleRackDefinition.bareMetalMachineConfigurationData[*].bmcCredentials.password", Mode: redact.RedactScalar}, {Path: "properties.aggregatorOrSingleRackDefinition.storageApplianceConfigurationData[*].adminCredentials.password", Mode: redact.RedactScalar}, {Path: "properties.computeRackDefinitions[*].bareMetalMachineConfigurationData[*].bmcCredentials.password", Mode: redact.RedactScalar}, {Path: "properties.computeRackDefinitions[*].storageApplianceConfigurationData[*].adminCredentials.password", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeNetworkCloudBareMetalMachine, Service: "microsoft.networkcloud", Leaf: true, Redact: []redact.Rule{{Path: "properties.bmcCredentials.password", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeNetworkCloudServicesNetwork, Service: "microsoft.networkcloud", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkCloudClusterManager, Service: "microsoft.networkcloud", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkCloudKubernetesCluster, Service: "microsoft.networkcloud", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkCloudL2Network, Service: "microsoft.networkcloud", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkCloudL3Network, Service: "microsoft.networkcloud", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkCloudRack, Service: "microsoft.networkcloud", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkCloudRackSKU, Service: "microsoft.networkcloud", Leaf: true, Managed: true})
	registerType(restype.Descriptor{Type: TypeNetworkCloudStorageAppliance, Service: "microsoft.networkcloud", Leaf: true, Redact: []redact.Rule{{Path: "properties.administratorCredentials.password", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeNetworkCloudTrunkedNetwork, Service: "microsoft.networkcloud", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkCloudVirtualMachine, Service: "microsoft.networkcloud", Leaf: true, Redact: []redact.Rule{{Path: "properties.vmImageRepositoryCredentials.password", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeNetworkCloudVolume, Service: "microsoft.networkcloud", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.networkcloud",
		fn:   scanNetworkCloud,
	})
}

// scanNetworkCloud discovers Microsoft.NetworkCloud (Operator Nexus) resources.
// Every type exposes a subscription-wide ListBySubscription pager.
//
// SECURITY: bare-metal-machines, storage-appliances, and virtual-machines
// return admin / BMC / image-repository credentials inline in the list response,
// redacted via rules in azure_redact.go.
func scanNetworkCloud(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	clClient, err := armnetworkcloud.NewClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetworkcloud:NewClustersClient: %w", err)
	}
	bmmClient, err := armnetworkcloud.NewBareMetalMachinesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetworkcloud:NewBareMetalMachinesClient: %w", err)
	}
	csnClient, err := armnetworkcloud.NewCloudServicesNetworksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetworkcloud:NewCloudServicesNetworksClient: %w", err)
	}
	cmClient, err := armnetworkcloud.NewClusterManagersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetworkcloud:NewClusterManagersClient: %w", err)
	}
	kcClient, err := armnetworkcloud.NewKubernetesClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetworkcloud:NewKubernetesClustersClient: %w", err)
	}
	l2Client, err := armnetworkcloud.NewL2NetworksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetworkcloud:NewL2NetworksClient: %w", err)
	}
	l3Client, err := armnetworkcloud.NewL3NetworksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetworkcloud:NewL3NetworksClient: %w", err)
	}
	rackClient, err := armnetworkcloud.NewRacksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetworkcloud:NewRacksClient: %w", err)
	}
	rackSKUClient, err := armnetworkcloud.NewRackSKUsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetworkcloud:NewRackSKUsClient: %w", err)
	}
	saClient, err := armnetworkcloud.NewStorageAppliancesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetworkcloud:NewStorageAppliancesClient: %w", err)
	}
	tnClient, err := armnetworkcloud.NewTrunkedNetworksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetworkcloud:NewTrunkedNetworksClient: %w", err)
	}
	vmClient, err := armnetworkcloud.NewVirtualMachinesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetworkcloud:NewVirtualMachinesClient: %w", err)
	}
	volClient, err := armnetworkcloud.NewVolumesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetworkcloud:NewVolumesClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetworkcloud:Clusters.ListBySubscription", TypeNetworkCloudCluster, sub, st, scanID,
				clClient.NewListBySubscriptionPager(nil),
				func(p armnetworkcloud.ClustersClientListBySubscriptionResponse) []*armnetworkcloud.Cluster {
					return p.Value
				},
				func(r *armnetworkcloud.Cluster) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetworkcloud:BareMetalMachines.ListBySubscription", TypeNetworkCloudBareMetalMachine, sub, st, scanID,
				bmmClient.NewListBySubscriptionPager(nil),
				func(p armnetworkcloud.BareMetalMachinesClientListBySubscriptionResponse) []*armnetworkcloud.BareMetalMachine {
					return p.Value
				},
				func(r *armnetworkcloud.BareMetalMachine) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetworkcloud:CloudServicesNetworks.ListBySubscription", TypeNetworkCloudServicesNetwork, sub, st, scanID,
				csnClient.NewListBySubscriptionPager(nil),
				func(p armnetworkcloud.CloudServicesNetworksClientListBySubscriptionResponse) []*armnetworkcloud.CloudServicesNetwork {
					return p.Value
				},
				func(r *armnetworkcloud.CloudServicesNetwork) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetworkcloud:ClusterManagers.ListBySubscription", TypeNetworkCloudClusterManager, sub, st, scanID,
				cmClient.NewListBySubscriptionPager(nil),
				func(p armnetworkcloud.ClusterManagersClientListBySubscriptionResponse) []*armnetworkcloud.ClusterManager {
					return p.Value
				},
				func(r *armnetworkcloud.ClusterManager) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetworkcloud:KubernetesClusters.ListBySubscription", TypeNetworkCloudKubernetesCluster, sub, st, scanID,
				kcClient.NewListBySubscriptionPager(nil),
				func(p armnetworkcloud.KubernetesClustersClientListBySubscriptionResponse) []*armnetworkcloud.KubernetesCluster {
					return p.Value
				},
				func(r *armnetworkcloud.KubernetesCluster) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetworkcloud:L2Networks.ListBySubscription", TypeNetworkCloudL2Network, sub, st, scanID,
				l2Client.NewListBySubscriptionPager(nil),
				func(p armnetworkcloud.L2NetworksClientListBySubscriptionResponse) []*armnetworkcloud.L2Network {
					return p.Value
				},
				func(r *armnetworkcloud.L2Network) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetworkcloud:L3Networks.ListBySubscription", TypeNetworkCloudL3Network, sub, st, scanID,
				l3Client.NewListBySubscriptionPager(nil),
				func(p armnetworkcloud.L3NetworksClientListBySubscriptionResponse) []*armnetworkcloud.L3Network {
					return p.Value
				},
				func(r *armnetworkcloud.L3Network) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetworkcloud:Racks.ListBySubscription", TypeNetworkCloudRack, sub, st, scanID,
				rackClient.NewListBySubscriptionPager(nil),
				func(p armnetworkcloud.RacksClientListBySubscriptionResponse) []*armnetworkcloud.Rack { return p.Value },
				func(r *armnetworkcloud.Rack) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) { return scanNetworkCloudRackSKUs(ctx, sub, rackSKUClient, st, scanID) },
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetworkcloud:StorageAppliances.ListBySubscription", TypeNetworkCloudStorageAppliance, sub, st, scanID,
				saClient.NewListBySubscriptionPager(nil),
				func(p armnetworkcloud.StorageAppliancesClientListBySubscriptionResponse) []*armnetworkcloud.StorageAppliance {
					return p.Value
				},
				func(r *armnetworkcloud.StorageAppliance) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetworkcloud:TrunkedNetworks.ListBySubscription", TypeNetworkCloudTrunkedNetwork, sub, st, scanID,
				tnClient.NewListBySubscriptionPager(nil),
				func(p armnetworkcloud.TrunkedNetworksClientListBySubscriptionResponse) []*armnetworkcloud.TrunkedNetwork {
					return p.Value
				},
				func(r *armnetworkcloud.TrunkedNetwork) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetworkcloud:VirtualMachines.ListBySubscription", TypeNetworkCloudVirtualMachine, sub, st, scanID,
				vmClient.NewListBySubscriptionPager(nil),
				func(p armnetworkcloud.VirtualMachinesClientListBySubscriptionResponse) []*armnetworkcloud.VirtualMachine {
					return p.Value
				},
				func(r *armnetworkcloud.VirtualMachine) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetworkcloud:Volumes.ListBySubscription", TypeNetworkCloudVolume, sub, st, scanID,
				volClient.NewListBySubscriptionPager(nil),
				func(p armnetworkcloud.VolumesClientListBySubscriptionResponse) []*armnetworkcloud.Volume {
					return p.Value
				},
				func(r *armnetworkcloud.Volume) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}

// scanNetworkCloudRackSKUs scans the platform-supplied rack-SKU catalog: not
// user-created (Azure materialises them, undeletable), so each row is flagged
// ManagedByProvider. Also proxy resources (no location / tags / RG), so
// azSimpleScan's tracked shape doesn't fit.
func scanNetworkCloudRackSKUs(ctx context.Context, sub *subscription, client *armnetworkcloud.RackSKUsClient, st *store.Store, scanID string) (total, inserted int, err error) {
	return azPageScan(ctx, "armnetworkcloud:RackSKUs.ListBySubscription", sub, st,
		client.NewListBySubscriptionPager(nil),
		func(page armnetworkcloud.RackSKUsClientListBySubscriptionResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, sku := range page.Value {
				if sku == nil || sku.ID == nil {
					continue
				}
				name := sv(sku.Name)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeNetworkCloudRackSKU, NativeID: sv(sku.ID),
					Name: &name, AttributesJSON: mustJSON(sku),
					// ManagedByProvider stamped by type via registerType(Managed: true).
					DiscoveredBy: scanID,
				})
			}
			return batch, nil
		})
}
