package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights"
)

func init() {
	registerType(restype.Descriptor{Type: TypeOpInsightsWorkspace, Service: "microsoft.operationalinsights"})
	registerType(restype.Descriptor{Type: TypeOpInsightsCluster, Service: "microsoft.operationalinsights", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.operationalinsights",
		fn:   scanOperationalInsights,
	})
}

// scanOperationalInsights discovers Azure Log Analytics workspaces.
// Solutions, data collection rules/endpoints, saved searches, and linked
// services are deferred: workspace rows alone unlock the diagnostic-settings
// edge target (resource → workspace) once that resolver lands; sub-resources
// add inventory volume but few cross-edges.
func scanOperationalInsights(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) {
			client, err := armoperationalinsights.NewWorkspacesClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armoperationalinsights:NewWorkspacesClient: %w", err)
			}
			return azSimpleScan(ctx, "armoperationalinsights:Workspaces.List", TypeOpInsightsWorkspace, sub, st, scanID,
				client.NewListPager(nil),
				func(p armoperationalinsights.WorkspacesClientListResponse) []*armoperationalinsights.Workspace {
					return p.Value
				},
				func(w *armoperationalinsights.Workspace) azTrackedBase {
					return azTrackedBase{id: sv(w.ID), name: sv(w.Name), location: sv(w.Location), tags: w.Tags, full: w}
				})
		},
		func() (int, int, error) {
			clClient, err := armoperationalinsights.NewClustersClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armoperationalinsights:NewClustersClient: %w", err)
			}
			return azSimpleScan(ctx, "armoperationalinsights:Clusters.List", TypeOpInsightsCluster, sub, st, scanID,
				clClient.NewListPager(nil),
				func(p armoperationalinsights.ClustersClientListResponse) []*armoperationalinsights.Cluster {
					return p.Value
				},
				func(c *armoperationalinsights.Cluster) azTrackedBase {
					return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
				})
		},
	)
}
