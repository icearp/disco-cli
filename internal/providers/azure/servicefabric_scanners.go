package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicefabric/armservicefabric"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.servicefabric",
		fn:   scanServiceFabricNamespace,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.servicefabric", DiscoType: TypeServiceFabricCluster, Leaf: true},
		},
	})
}

// scanServiceFabric discovers Service Fabric clusters. The Clusters.List op is
// a single non-paginated subscription-wide call (no pager), so it can't use
// azSimpleScan; AccessDenied is unwrapped manually like security_scanners.go.
func scanServiceFabric(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armservicefabric.NewClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armservicefabric:NewClustersClient: %w", err)
	}
	resp, err := client.List(ctx, nil)
	if err != nil {
		if isSkippableScanError(err) {
			return 0, 0, skipIfAccessDenied(st, "armservicefabric:Clusters.List", sub.ID, err)
		}
		return 0, 0, fmt.Errorf("armservicefabric:Clusters.List: %w", err)
	}
	batch, pairs := azTrackedRows(sub, scanID, TypeServiceFabricCluster, resp.Value,
		func(c *armservicefabric.Cluster) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert armservicefabric:Clusters.List: %w", err)
	}
	if len(pairs) > 0 {
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return len(batch), n, fmt.Errorf("closure armservicefabric:Clusters.List: %w", err)
		}
	}
	return len(batch), n, nil
}

// scanServiceFabricNamespace runs every Microsoft.servicefabric scanner phase concurrently. The
// servicefabric ARM namespace spans several disco scanners merged under one
// serviceEntry so the service name aligns to the namespace.
func scanServiceFabricNamespace(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) { return scanServiceFabric(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanServiceFabricManagedClusters(ctx, sub, cred, st, scanID) },
	)
}
