package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/azurearcdata/armazurearcdata"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAzureArcDataController, Service: "microsoft.azurearcdata", Leaf: true, Redact: []redact.Rule{{Path: "properties.logsDashboardCredential.password", Mode: redact.RedactScalar}, {Path: "properties.metricsDashboardCredential.password", Mode: redact.RedactScalar}, {Path: "properties.logAnalyticsWorkspaceConfig.primaryKey", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeAzureArcDataPostgres, Service: "microsoft.azurearcdata", Leaf: true, Redact: []redact.Rule{{Path: "properties.basicLoginInformation.password", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeAzureArcDataSQLManagedInstance, Service: "microsoft.azurearcdata", Leaf: true, Redact: []redact.Rule{{Path: "properties.basicLoginInformation.password", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeAzureArcDataSQLServerInstance, Service: "microsoft.azurearcdata", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.azurearcdata",
		fn:   scanAzureArcData,
	})
}

// scanAzureArcData discovers Azure Arc-enabled data controllers plus the
// Postgres / SQL managed / SQL Server instances projected into Azure, all
// sub-wide via armazurearcdata.
func scanAzureArcData(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	dc, err := armazurearcdata.NewDataControllersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armazurearcdata:NewDataControllersClient: %w", err)
	}
	pg, err := armazurearcdata.NewPostgresInstancesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armazurearcdata:NewPostgresInstancesClient: %w", err)
	}
	mi, err := armazurearcdata.NewSQLManagedInstancesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armazurearcdata:NewSQLManagedInstancesClient: %w", err)
	}
	si, err := armazurearcdata.NewSQLServerInstancesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armazurearcdata:NewSQLServerInstancesClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armazurearcdata:DataControllers.ListInSubscription", TypeAzureArcDataController, sub, st, scanID,
				dc.NewListInSubscriptionPager(nil),
				func(p armazurearcdata.DataControllersClientListInSubscriptionResponse) []*armazurearcdata.DataControllerResource {
					return p.Value
				},
				func(d *armazurearcdata.DataControllerResource) azTrackedBase {
					return azTrackedBase{id: sv(d.ID), name: sv(d.Name), location: sv(d.Location), tags: d.Tags, full: d}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armazurearcdata:PostgresInstances.List", TypeAzureArcDataPostgres, sub, st, scanID,
				pg.NewListPager(nil),
				func(p armazurearcdata.PostgresInstancesClientListResponse) []*armazurearcdata.PostgresInstance {
					return p.Value
				},
				func(r *armazurearcdata.PostgresInstance) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armazurearcdata:SQLManagedInstances.List", TypeAzureArcDataSQLManagedInstance, sub, st, scanID,
				mi.NewListPager(nil),
				func(p armazurearcdata.SQLManagedInstancesClientListResponse) []*armazurearcdata.SQLManagedInstance {
					return p.Value
				},
				func(r *armazurearcdata.SQLManagedInstance) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armazurearcdata:SQLServerInstances.List", TypeAzureArcDataSQLServerInstance, sub, st, scanID,
				si.NewListPager(nil),
				func(p armazurearcdata.SQLServerInstancesClientListResponse) []*armazurearcdata.SQLServerInstance {
					return p.Value
				},
				func(r *armazurearcdata.SQLServerInstance) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
