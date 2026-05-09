package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights"
)

func init() {
	registerService(serviceEntry{
		name: "azure:operationalinsights",
		fn:   scanOperationalInsights,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.operationalinsights", DiscoType: TypeOpInsightsWorkspace},
		},
	})
}

// scanOperationalInsights discovers Azure Log Analytics workspaces.
// Solutions, data collection rules/endpoints, saved searches, and linked
// services are deferred — workspace rows alone unlock the diagnostic-settings
// edge target story (resource → workspace) once the diagnostic-settings
// resolver lands; the sub-resources add inventory volume but few cross-edges.
func scanOperationalInsights(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
}
