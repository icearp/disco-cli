//go:build paid

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/google/uuid"

	"codeberg.org/icearp/disco/store"
)

// init reassigns openWriteDBHook (declared in cmd/helpers.go OSS file) to
// dial Postgres when the SaaS-deploy env is present. OSS builds leave the
// hook nil and always open local SQLite; paid builds opt into PG when
// DISCO_PG_DSN + DISCO_TENANT_ID are set. Empty env on a paid build falls
// through to SQLite for local-dev parity.
//
// Pattern per CLAUDE.md "Hook-var indirection for paid features on OSS
// commands": OSS file declares the hook nillable, paid file assigns it in
// init() so OSS users see no env-driven branch.
func init() {
	openWriteDBHook = func() (*store.Store, error) {
		dsn := os.Getenv("DISCO_PG_DSN")
		if dsn == "" {
			return nil, nil
		}
		tenantID := os.Getenv("DISCO_TENANT_ID")
		if tenantID == "" {
			return nil, errors.New("DISCO_TENANT_ID is required when DISCO_PG_DSN is set")
		}
		if _, err := uuid.Parse(tenantID); err != nil {
			return nil, fmt.Errorf("DISCO_TENANT_ID must be a UUID: %w", err)
		}
		// DISCO_PG_SCHEMA pins search_path to the per-tenant schema. Required
		// in the SaaS deployment where every workspace lives in its own
		// tenant_<hex> schema; without it the scanner would write to public
		// and the web app's per-tenant reads would see zero rows.
		if schema := os.Getenv("DISCO_PG_SCHEMA"); schema != "" {
			return store.OpenPostgresInSchema(context.Background(), dsn, schema, tenantID)
		}
		return store.OpenPostgres(context.Background(), dsn, tenantID)
	}
}

// resolveCheckRunID expands the `latest` shorthand to the most-recent
// check_run's ID; literal IDs pass through after a presence check. Mirrors
// resolveScanID. Used by `disco findings list --check-run-id <...>`.
func resolveCheckRunID(db *store.Store, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if raw == "latest" {
		runs, err := db.ListCheckRuns()
		if err != nil {
			return "", fmt.Errorf("list check_runs: %w", err)
		}
		if len(runs) == 0 {
			return "", fmt.Errorf("no check runs recorded; --check-run-id latest has nothing to resolve")
		}
		return runs[0].ID, nil
	}
	if _, err := db.GetCheckRun(raw); err != nil {
		return "", fmt.Errorf("check run %q not found", raw)
	}
	return raw, nil
}
