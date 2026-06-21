package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicefabricmanagedclusters/armservicefabricmanagedclusters"
)

func init() {
	registerExtraEmits([]coverage.TypeDecl{
		{Service: "microsoft.servicefabric", DiscoType: TypeServiceFabricManagedCluster},
	}...)
}

// scanServiceFabricManagedClusters discovers Service Fabric managed clusters.
// The cluster properties echo the VM admin password on the list response —
// redacted via azure_redact.go. Node types and applications are parent-scoped
// and deferred.
func scanServiceFabricManagedClusters(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armservicefabricmanagedclusters.NewManagedClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armservicefabricmanagedclusters:NewManagedClustersClient: %w", err)
	}
	return azSimpleScan(ctx, "armservicefabricmanagedclusters:ManagedClusters.ListBySubscription", TypeServiceFabricManagedCluster, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armservicefabricmanagedclusters.ManagedClustersClientListBySubscriptionResponse) []*armservicefabricmanagedclusters.ManagedCluster {
			return p.Value
		},
		func(r *armservicefabricmanagedclusters.ManagedCluster) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
