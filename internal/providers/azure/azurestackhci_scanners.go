package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/azurestackhci/armazurestackhci"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/azurestackhci/armazurestackhcivm"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.azurestackhci",
		fn:   scanAzureStackHCI,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.azurestackhci", DiscoType: TypeAzureStackHCICluster, Leaf: true},
			// Azure Local VM/network/storage family (armazurestackhcivm). All
			// carry an extendedLocation envelope → central
			// resolveExtendedLocationConsumers wires them to their custom-location.
			{Service: "microsoft.azurestackhci", DiscoType: TypeAzureStackHCIGalleryImage, Leaf: true},
			{Service: "microsoft.azurestackhci", DiscoType: TypeAzureStackHCILogicalNetwork, Leaf: true},
			{Service: "microsoft.azurestackhci", DiscoType: TypeAzureStackHCIMarketplaceGalleryImage, Leaf: true},
			{Service: "microsoft.azurestackhci", DiscoType: TypeAzureStackHCINetworkInterface, Leaf: true},
			{Service: "microsoft.azurestackhci", DiscoType: TypeAzureStackHCINetworkSecurityGroup, Leaf: true},
			{Service: "microsoft.azurestackhci", DiscoType: TypeAzureStackHCIStorageContainer, Leaf: true},
			{Service: "microsoft.azurestackhci", DiscoType: TypeAzureStackHCIVirtualHardDisk, Leaf: true},
		},
	})
}

// scanAzureStackHCI discovers Azure Stack HCI / Azure Local clusters plus the
// VM/network/storage resources of the connected cluster. Clusters live in
// armazurestackhci; the rest are sub-wide ListAll in the sibling
// armazurestackhcivm module.
func scanAzureStackHCI(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	clusters, err := armazurestackhci.NewClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armazurestackhci:NewClustersClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armazurestackhci:Clusters.ListBySubscription", TypeAzureStackHCICluster, sub, st, scanID,
				clusters.NewListBySubscriptionPager(nil),
				func(p armazurestackhci.ClustersClientListBySubscriptionResponse) []*armazurestackhci.Cluster {
					return p.Value
				},
				func(c *armazurestackhci.Cluster) azTrackedBase {
					return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
				})
		},
		func() (int, int, error) { return scanAzureStackHCIVM(ctx, sub, cred, st, scanID) },
	)
}

// scanAzureStackHCIVM runs the seven sub-wide ListAll scans in armazurestackhcivm.
func scanAzureStackHCIVM(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	gi, err := armazurestackhcivm.NewGalleryImagesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armazurestackhcivm:NewGalleryImagesClient: %w", err)
	}
	ln, err := armazurestackhcivm.NewLogicalNetworksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armazurestackhcivm:NewLogicalNetworksClient: %w", err)
	}
	mgi, err := armazurestackhcivm.NewMarketplaceGalleryImagesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armazurestackhcivm:NewMarketplaceGalleryImagesClient: %w", err)
	}
	nic, err := armazurestackhcivm.NewNetworkInterfacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armazurestackhcivm:NewNetworkInterfacesClient: %w", err)
	}
	nsg, err := armazurestackhcivm.NewNetworkSecurityGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armazurestackhcivm:NewNetworkSecurityGroupsClient: %w", err)
	}
	sc, err := armazurestackhcivm.NewStorageContainersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armazurestackhcivm:NewStorageContainersClient: %w", err)
	}
	vhd, err := armazurestackhcivm.NewVirtualHardDisksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armazurestackhcivm:NewVirtualHardDisksClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armazurestackhcivm:GalleryImages.ListAll", TypeAzureStackHCIGalleryImage, sub, st, scanID,
				gi.NewListAllPager(nil),
				func(p armazurestackhcivm.GalleryImagesClientListAllResponse) []*armazurestackhcivm.GalleryImage {
					return p.Value
				},
				func(r *armazurestackhcivm.GalleryImage) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armazurestackhcivm:LogicalNetworks.ListAll", TypeAzureStackHCILogicalNetwork, sub, st, scanID,
				ln.NewListAllPager(nil),
				func(p armazurestackhcivm.LogicalNetworksClientListAllResponse) []*armazurestackhcivm.LogicalNetwork {
					return p.Value
				},
				func(r *armazurestackhcivm.LogicalNetwork) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armazurestackhcivm:MarketplaceGalleryImages.ListAll", TypeAzureStackHCIMarketplaceGalleryImage, sub, st, scanID,
				mgi.NewListAllPager(nil),
				func(p armazurestackhcivm.MarketplaceGalleryImagesClientListAllResponse) []*armazurestackhcivm.MarketplaceGalleryImage {
					return p.Value
				},
				func(r *armazurestackhcivm.MarketplaceGalleryImage) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armazurestackhcivm:NetworkInterfaces.ListAll", TypeAzureStackHCINetworkInterface, sub, st, scanID,
				nic.NewListAllPager(nil),
				func(p armazurestackhcivm.NetworkInterfacesClientListAllResponse) []*armazurestackhcivm.NetworkInterface {
					return p.Value
				},
				func(r *armazurestackhcivm.NetworkInterface) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armazurestackhcivm:NetworkSecurityGroups.ListAll", TypeAzureStackHCINetworkSecurityGroup, sub, st, scanID,
				nsg.NewListAllPager(nil),
				func(p armazurestackhcivm.NetworkSecurityGroupsClientListAllResponse) []*armazurestackhcivm.NetworkSecurityGroup {
					return p.Value
				},
				func(r *armazurestackhcivm.NetworkSecurityGroup) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armazurestackhcivm:StorageContainers.ListAll", TypeAzureStackHCIStorageContainer, sub, st, scanID,
				sc.NewListAllPager(nil),
				func(p armazurestackhcivm.StorageContainersClientListAllResponse) []*armazurestackhcivm.StorageContainer {
					return p.Value
				},
				func(r *armazurestackhcivm.StorageContainer) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armazurestackhcivm:VirtualHardDisks.ListAll", TypeAzureStackHCIVirtualHardDisk, sub, st, scanID,
				vhd.NewListAllPager(nil),
				func(p armazurestackhcivm.VirtualHardDisksClientListAllResponse) []*armazurestackhcivm.VirtualHardDisk {
					return p.Value
				},
				func(r *armazurestackhcivm.VirtualHardDisk) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
