package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/elastic/armelastic"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeElasticMonitor, Service: "microsoft.elastic"})
	registerService(serviceEntry{
		name: "azure:microsoft.elastic",
		fn:   scanElastic,
	})
}

// scanElastic discovers Elastic Cloud (Microsoft.Elastic) monitor resources.
func scanElastic(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armelastic.NewMonitorsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armelastic:NewMonitorsClient: %w", err)
	}
	return azSimpleScan(ctx, "armelastic:Monitors.List", TypeElasticMonitor, sub, st, scanID,
		client.NewListPager(nil),
		func(p armelastic.MonitorsClientListResponse) []*armelastic.MonitorResource { return p.Value },
		func(r *armelastic.MonitorResource) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
