package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDatabricksWorkspace, Service: "microsoft.databricks"})
	registerType(restype.Descriptor{Type: TypeDatabricksAccessConnector, Service: "microsoft.databricks", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.databricks",
		fn:   scanDatabricks,
	})
}

// scanDatabricks discovers Azure Databricks workspaces and access connectors.
// Per-workspace VNet peerings and private endpoint connections deferred —
// covered by the dedicated PE scanner.
func scanDatabricks(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) {
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
		},
		func() (int, int, error) {
			acClient, err := armdatabricks.NewAccessConnectorsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armdatabricks:NewAccessConnectorsClient: %w", err)
			}
			return azSimpleScan(ctx, "armdatabricks:AccessConnectors.ListBySubscription", TypeDatabricksAccessConnector, sub, st, scanID,
				acClient.NewListBySubscriptionPager(nil),
				func(p armdatabricks.AccessConnectorsClientListBySubscriptionResponse) []*armdatabricks.AccessConnector {
					return p.Value
				},
				func(a *armdatabricks.AccessConnector) azTrackedBase {
					return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
				})
		},
	)
}
