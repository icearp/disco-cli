package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypePostgreSQLFlexibleServer, Service: "microsoft.dbforpostgresql", Redact: []redact.Rule{{Path: "properties.administratorLoginPassword", Mode: redact.RedactScalar}}})
	registerService(serviceEntry{
		name: "azure:microsoft.dbforpostgresql",
		fn:   scanDBforPostgreSQLNamespace,
	})
}

// scanPostgreSQL discovers Azure Database for PostgreSQL flexible servers.
func scanPostgreSQL(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armpostgresqlflexibleservers.NewServersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpostgresqlflexibleservers:NewServersClient: %w", err)
	}
	return azSimpleScan(ctx, "armpostgresqlflexibleservers:Servers.List", TypePostgreSQLFlexibleServer, sub, st, scanID,
		client.NewListPager(nil),
		func(p armpostgresqlflexibleservers.ServersClientListResponse) []*armpostgresqlflexibleservers.Server {
			return p.Value
		},
		func(r *armpostgresqlflexibleservers.Server) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}

// scanDBforPostgreSQLNamespace runs every Microsoft.dbforpostgresql scanner
// phase concurrently — the namespace spans several disco scanners merged
// under one serviceEntry so the service name aligns to it.
func scanDBforPostgreSQLNamespace(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) { return scanPostgreSQL(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanPostgreSQLHSC(ctx, sub, cred, st, scanID) },
	)
}
