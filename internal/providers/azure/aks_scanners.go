package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservicefleet/armcontainerservicefleet/v3"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.containerservice",
		fn:   scanAKS,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.containerservice", DiscoType: TypeContainerServiceManagedCluster},
			{Service: "microsoft.containerservice", DiscoType: TypeContainerServiceSnapshot},
			{Service: "microsoft.containerservice", DiscoType: TypeContainerServiceFleet},
		},
	})
}

// scanAKS discovers Microsoft.ContainerService resources — AKS managed
// clusters, node-pool/managed-cluster snapshots, and AKS fleets (the fleet
// type lives in the separate containerservicefleet SDK module).
func scanAKS(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcontainerservice.NewManagedClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcontainerservice:NewManagedClustersClient: %w", err)
	}
	mt, mi, err := azPageScan(ctx, "armcontainerservice:ManagedClusters.List", sub, st,
		client.NewListPager(nil),
		func(page armcontainerservice.ManagedClustersClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, cluster := range page.Value {
				if cluster.ID == nil {
					continue
				}
				name, loc := sv(cluster.Name), sv(cluster.Location)
				var status string
				if cluster.Properties != nil && cluster.Properties.ProvisioningState != nil {
					status = *cluster.Properties.ProvisioningState
				}
				r := &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeContainerServiceManagedCluster, NativeID: sv(cluster.ID),
					Name: &name, Region: &loc, Status: &status,
					TagsJSON: azTagsJSON(cluster.Tags), AttributesJSON: mustJSON(cluster),
					DiscoveredBy: scanID,
				}
				if cluster.SystemData != nil {
					r.CreatedAt = tp(cluster.SystemData.CreatedAt)
				}
				batch = append(batch, r)
				if rgFromID(sv(cluster.ID)) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeContainerServiceManagedCluster, sv(cluster.ID)))
				}
			}
			return batch, pairs
		})
	total += mt
	inserted += mi
	if err != nil {
		return total, inserted, err
	}

	snapClient, err := armcontainerservice.NewSnapshotsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armcontainerservice:NewSnapshotsClient: %w", err)
	}
	st1, si, err := azSimpleScan(ctx, "armcontainerservice:Snapshots.List", TypeContainerServiceSnapshot, sub, st, scanID,
		snapClient.NewListPager(nil),
		func(p armcontainerservice.SnapshotsClientListResponse) []*armcontainerservice.Snapshot {
			return p.Value
		},
		func(r *armcontainerservice.Snapshot) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
	total += st1
	inserted += si
	if err != nil {
		return total, inserted, err
	}

	fleetClient, err := armcontainerservicefleet.NewFleetsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armcontainerservicefleet:NewFleetsClient: %w", err)
	}
	ft, fi, err := azSimpleScan(ctx, "armcontainerservicefleet:Fleets.ListBySubscription", TypeContainerServiceFleet, sub, st, scanID,
		fleetClient.NewListBySubscriptionPager(nil),
		func(p armcontainerservicefleet.FleetsClientListBySubscriptionResponse) []*armcontainerservicefleet.Fleet {
			return p.Value
		},
		func(r *armcontainerservicefleet.Fleet) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
	total += ft
	inserted += fi
	return total, inserted, err
}
