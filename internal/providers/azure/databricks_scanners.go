package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks"
)

func init() { registerService(serviceEntry{name: "azure:databricks", fn: scanDatabricks}) }

// scanDatabricks discovers Azure Databricks workspaces. Per-workspace VNet
// peerings, private endpoint connections, and access connectors deferred —
// peerings + PE connections covered by the dedicated PE scanner; access
// connectors are a separate ARM type warranting their own scanner.
func scanDatabricks(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdatabricks.NewWorkspacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdatabricks:NewWorkspacesClient: %w", err)
	}
	return azPageScan(ctx, "armdatabricks:Workspaces.ListBySubscription", sub, st,
		client.NewListBySubscriptionPager(nil),
		func(page armdatabricks.WorkspacesClientListBySubscriptionResponse) ([]*store.Resource, [][2]string) {
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
					Type: TypeDatabricksWorkspace, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(w.Tags), AttributesJSON: mustJSON(w),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeDatabricksWorkspace, nativeID))
				}
			}
			return batch, pairs
		})
}
