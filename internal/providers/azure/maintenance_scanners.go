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
			{Service: "microsoft.maintenance", DiscoType: TypeMaintenanceConfigAssignment, Leaf: true},
			// Public maintenance configurations are platform-supplied catalog.
			{Service: "microsoft.maintenance", DiscoType: TypeMaintenancePublicConfiguration, Leaf: true},
		},
	})
}

// scanMaintenance discovers maintenance configurations,
// subscription-wide configuration assignments, and the public config catalog.
func scanMaintenance(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	configs, err := armmaintenance.NewConfigurationsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmaintenance:NewConfigurationsClient: %w", err)
	}
	assigns, err := armmaintenance.NewConfigurationAssignmentsWithinSubscriptionClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmaintenance:NewConfigurationAssignmentsWithinSubscriptionClient: %w", err)
	}
	public, err := armmaintenance.NewPublicMaintenanceConfigurationsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmaintenance:NewPublicMaintenanceConfigurationsClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmaintenance:Configurations.List", TypeMaintenanceConfiguration, sub, st, scanID,
				configs.NewListPager(nil),
				func(p armmaintenance.ConfigurationsClientListResponse) []*armmaintenance.Configuration {
					return p.Value
				},
				func(r *armmaintenance.Configuration) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmaintenance:ConfigurationAssignmentsWithinSubscription.List", TypeMaintenanceConfigAssignment, sub, st, scanID,
				assigns.NewListPager(nil),
				func(p armmaintenance.ConfigurationAssignmentsWithinSubscriptionClientListResponse) []*armmaintenance.ConfigurationAssignment {
					return p.Value
				},
				func(r *armmaintenance.ConfigurationAssignment) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmaintenance:PublicMaintenanceConfigurations.List", TypeMaintenancePublicConfiguration, sub, st, scanID,
				public.NewListPager(nil),
				func(p armmaintenance.PublicMaintenanceConfigurationsClientListResponse) []*armmaintenance.Configuration {
					return p.Value
				},
				func(r *armmaintenance.Configuration) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, managed: true, full: r}
				})
		},
	)
}
