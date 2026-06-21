package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/datamigration/armdatamigration"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.datamigration",
		fn:   scanDataMigration,
		emits: []coverage.TypeDecl{
			// resolveDataMigrationRelationships wires the virtualSubnetId (VNet)
			// edge below.
			{Service: "microsoft.datamigration", DiscoType: TypeDataMigrationService},
		},
	})
}

// scanDataMigration discovers Azure Database Migration Service instances.
func scanDataMigration(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdatamigration.NewServicesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdatamigration:NewServicesClient: %w", err)
	}
	return azSimpleScan(ctx, "armdatamigration:Services.List", TypeDataMigrationService, sub, st, scanID,
		client.NewListPager(nil),
		func(p armdatamigration.ServicesClientListResponse) []*armdatamigration.Service { return p.Value },
		func(s *armdatamigration.Service) azTrackedBase {
			return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
		})
}
