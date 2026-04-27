package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights"
)

func init() {
	registerService(serviceEntry{name: "azure:operationalinsights", fn: scanOperationalInsights})
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
	return azPageScan(ctx, "armoperationalinsights:Workspaces.List", sub, st,
		client.NewListPager(nil),
		func(page armoperationalinsights.WorkspacesClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, w := range page.Value {
				if w == nil || w.ID == nil {
					continue
				}
				name, loc := sv(w.Name), sv(w.Location)
				nativeID := sv(w.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeOpInsightsWorkspace, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(w.Tags), AttributesJSON: mustJSON(w),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeOpInsightsWorkspace, nativeID))
				}
			}
			return batch, pairs
		})
}
