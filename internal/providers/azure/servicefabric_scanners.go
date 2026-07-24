package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicefabric/armservicefabric"
)

func init() {
	registerType(restype.Descriptor{Type: TypeServiceFabricCluster, Service: "microsoft.servicefabric", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.servicefabric",
		fn:   scanServiceFabricNamespace,
	})
}

// scanServiceFabric discovers Service Fabric clusters. Clusters.List is a
// single subscription-wide call with no pager, so it can't use azSimpleScan;
// AccessDenied is unwrapped manually like security_scanners.go.
func scanServiceFabric(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
// serviceEntry so the service name matches it.
func scanServiceFabricNamespace(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) { return scanServiceFabric(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanServiceFabricManagedClusters(ctx, sub, cred, st, scanID) },
	)
}
