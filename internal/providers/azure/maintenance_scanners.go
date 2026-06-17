package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/maintenance/armmaintenance"
)

func init() {
	registerService(serviceEntry{
		name: "azure:maintenance",
		fn:   scanMaintenance,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.maintenance", DiscoType: TypeMaintenanceConfiguration, Leaf: true},
		},
	})
}

// scanMaintenance discovers maintenance resources.
func scanMaintenance(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armmaintenance.NewConfigurationsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmaintenance:NewConfigurationsClient: %w", err)
	}
	return azSimpleScan(ctx, "armmaintenance:Configurations.List", TypeMaintenanceConfiguration, sub, st, scanID,
		client.NewListPager(nil),
		func(p armmaintenance.ConfigurationsClientListResponse) []*armmaintenance.Configuration {
			return p.Value
		},
		func(r *armmaintenance.Configuration) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
