package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresql"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.dbforpostgresql",
		fn:   scanDBforPostgreSQLNamespace,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.dbforpostgresql", DiscoType: TypePostgreSQLFlexibleServer},
			{Service: "microsoft.dbforpostgresql", DiscoType: TypePostgreSQLSingleServer, Leaf: true},
		},
	})
}

// scanPostgreSQL discovers Azure Database for PostgreSQL flexible servers and
// the deprecated Single Server tier. Single Server's RP is being retired; the
// graceful-skip error classifier tolerates dead-RP responses at scan time.
func scanPostgreSQL(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armpostgresqlflexibleservers.NewServersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpostgresqlflexibleservers:NewServersClient: %w", err)
	}
	total, inserted, err = azSimpleScan(ctx, "armpostgresqlflexibleservers:Servers.List", TypePostgreSQLFlexibleServer, sub, st, scanID,
		client.NewListPager(nil),
		func(p armpostgresqlflexibleservers.ServersClientListResponse) []*armpostgresqlflexibleservers.Server {
			return p.Value
		},
		func(r *armpostgresqlflexibleservers.Server) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
	if err != nil {
		return total, inserted, err
	}

	singleClient, err := armpostgresql.NewServersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armpostgresql:NewServersClient: %w", err)
	}
	st1, si1, err := azSimpleScan(ctx, "armpostgresql:Servers.List", TypePostgreSQLSingleServer, sub, st, scanID,
		singleClient.NewListPager(nil),
		func(p armpostgresql.ServersClientListResponse) []*armpostgresql.Server { return p.Value },
		func(r *armpostgresql.Server) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
	total += st1
	inserted += si1
	return total, inserted, err
}

// scanDBforPostgreSQLNamespace runs every Microsoft.dbforpostgresql scanner phase concurrently. The
// dbforpostgresql ARM namespace spans several disco scanners merged under one
// serviceEntry so the service name aligns to the namespace.
func scanDBforPostgreSQLNamespace(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) { return scanPostgreSQL(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanPostgreSQLHSC(ctx, sub, cred, st, scanID) },
	)
}
