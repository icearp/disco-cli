package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dashboard/armdashboard"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDashboardGrafana, Service: "microsoft.dashboard", Leaf: true, Redact: []redact.Rule{{Path: "properties.grafanaConfigurations.smtp.password", Mode: redact.RedactScalar}}})
	registerService(serviceEntry{
		name: "azure:microsoft.dashboard",
		fn:   scanDashboard,
	})
}

// scanDashboard discovers Azure Managed Grafana workspaces.
func scanDashboard(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
