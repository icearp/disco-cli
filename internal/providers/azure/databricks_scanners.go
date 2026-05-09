package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks"
)

func init() {
	registerService(serviceEntry{
		name: "azure:databricks",
		fn:   scanDatabricks,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.databricks", DiscoType: TypeDatabricksWorkspace},
		},
	})
}

// scanDatabricks discovers Azure Databricks workspaces. Per-workspace VNet
// peerings, private endpoint connections, and access connectors deferred —
// peerings + PE connections covered by the dedicated PE scanner; access
// connectors are a separate ARM type warranting their own scanner.
func scanDatabricks(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdatabricks.NewWorkspacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdatabricks:NewWorkspacesClient: %w", err)
	}
	return azSimpleScan(ctx, "armdatabricks:Workspaces.ListBySubscription", TypeDatabricksWorkspace, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armdatabricks.WorkspacesClientListBySubscriptionResponse) []*armdatabricks.Workspace {
			return p.Value
		},
		func(w *armdatabricks.Workspace) azTrackedBase {
			return azTrackedBase{id: sv(w.ID), name: sv(w.Name), location: sv(w.Location), tags: w.Tags, full: w}
		})
}
