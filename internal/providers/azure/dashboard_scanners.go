package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dashboard/armdashboard"
)

func init() {
	registerService(serviceEntry{
		name: "azure:dashboard",
		fn:   scanDashboard,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.dashboard", DiscoType: TypeDashboardGrafana, Leaf: true},
		},
	})
}

// scanDashboard discovers Azure Managed Grafana workspaces.
func scanDashboard(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdashboard.NewGrafanaClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdashboard:NewGrafanaClient: %w", err)
	}
	return azSimpleScan(ctx, "armdashboard:Grafana.List", TypeDashboardGrafana, sub, st, scanID,
		client.NewListPager(nil),
		func(p armdashboard.GrafanaClientListResponse) []*armdashboard.ManagedGrafana { return p.Value },
		func(g *armdashboard.ManagedGrafana) azTrackedBase {
			return azTrackedBase{id: sv(g.ID), name: sv(g.Name), location: sv(g.Location), tags: g.Tags, full: g}
		})
}
