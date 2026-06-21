package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/azurearcdata/armazurearcdata"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.azurearcdata",
		fn:   scanAzureArcData,
		emits: []coverage.TypeDecl{
			// Custom-location edge wired centrally (resolveExtendedLocationConsumers);
			// scanner-only here.
			{Service: "microsoft.azurearcdata", DiscoType: TypeAzureArcDataController, Leaf: true},
			{Service: "microsoft.azurearcdata", DiscoType: TypeAzureArcDataPostgres, Leaf: true},
			{Service: "microsoft.azurearcdata", DiscoType: TypeAzureArcDataSQLManagedInstance, Leaf: true},
			{Service: "microsoft.azurearcdata", DiscoType: TypeAzureArcDataSQLServerInstance, Leaf: true},
		},
	})
}

// scanAzureArcData discovers Azure Arc-enabled data controllers plus the
// Postgres / SQL managed / SQL Server instances projected into Azure, all
// sub-wide via armazurearcdata.
func scanAzureArcData(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
