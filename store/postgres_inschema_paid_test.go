//go:build paid

package store

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestValidateSchemaName(t *testing.T) {
	good := "tenant_" + strings.Repeat("a", 32)
	if err := validateSchemaName(good); err != nil {
		t.Fatalf("good name rejected: %v", err)
	}
	bad := []string{
		"",
		"tenant_",
		"tenant_" + strings.Repeat("a", 31),
		"tenant_" + strings.Repeat("a", 33),
		"TENANT_" + strings.Repeat("a", 32),
		"tenant_" + strings.Repeat("g", 32), // non-hex
		`tenant_"; DROP TABLE x; --`,
		"public",
	}
	for _, b := range bad {
		if err := validateSchemaName(b); err == nil {
			t.Errorf("expected reject for %q", b)
		}
	}
}

func TestPgQuoteIdent(t *testing.T) {
	cases := map[string]string{
		"tenant_aa": `"tenant_aa"`,
		`a"b`:       `"a""b"`,
		"":          `""`,
	}
	for in, want := range cases {
		if got := pgQuoteIdent(in); got != want {
			t.Errorf("pgQuoteIdent(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestPG_OpenPostgresInSchema verifies a fresh per-tenant schema is created,
// migrated, and isolated from a sibling schema in the same database.
func TestPG_OpenPostgresInSchema(t *testing.T) {
	dsn, purge := pgTestEnv(t)
	defer purge()

	schemaA := "tenant_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	schemaB := "tenant_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	workspaceA := uuid.NewString()
	workspaceB := uuid.NewString()

	a, err := OpenPostgresInSchemaWithWorkspace(context.Background(), dsn, schemaA, tenantA, workspaceA)
	if err != nil {
		t.Fatalf("open A: %v", err)
	}
	defer func() { _ = a.Close() }()
	seedFixtures(t, a)

	b, err := OpenPostgresInSchemaWithWorkspace(context.Background(), dsn, schemaB, tenantB, workspaceB)
	if err != nil {
		t.Fatalf("open B: %v", err)
	}
	defer func() { _ = b.Close() }()

	gotA, err := a.ListResources(ResourceFilter{IncludeManaged: true, Limit: 100})
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(gotA) != 3 {
		t.Errorf("schema A list = %d; want 3", len(gotA))
	}
	gotB, err := b.ListResources(ResourceFilter{IncludeManaged: true, Limit: 100})
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(gotB) != 0 {
		t.Errorf("schema B saw %d rows from schema A", len(gotB))
	}

	// schema_migrations bookkeeping must live INSIDE each per-tenant schema,
	// not in public. Confirms the SET search_path took effect during migrate.
	var countA int
	if err := a.queryRow(
		"SELECT count(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'schema_migrations'",
		schemaA,
	).Scan(&countA); err != nil {
		t.Fatalf("inspect schema_migrations: %v", err)
	}
	if countA != 1 {
		t.Errorf("schema_migrations count in %s = %d; want 1", schemaA, countA)
	}
}

func TestPG_OpenPostgresInSchema_BoundaryValidation(t *testing.T) {
	// No DB needed — error path returns before any connection.
	_, err := OpenPostgresInSchema(context.Background(), "postgres://x@y/z", "evil; DROP SCHEMA public", uuid.NewString())
	if err == nil {
		t.Fatal("expected validation error for malformed schema name")
	}
}

// TestPG_WrapTx exercises read methods on a tx-bound *Store with SET LOCAL
// search_path + app.tenant_id — the SaaS request-path pattern.
func TestPG_WrapTx(t *testing.T) {
	dsn, purge := pgTestEnv(t)
	defer purge()

	schema := "tenant_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	tenantID := uuid.NewString()
	workspaceID := uuid.NewString()

	provisioner, err := OpenPostgresInSchemaWithWorkspace(context.Background(), dsn, schema, tenantID, workspaceID)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	seedFixtures(t, provisioner)
	_ = provisioner.Close()

	// Reopen on the connection's default search_path (no schema pin) so
	// the request-path SET LOCAL is the *only* thing that selects the tenant
	// schema — proves the GUC is the actual filter.
	pool, err := OpenPostgres(context.Background(), dsn, tenantID)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	tx, err := pool.DB().Beginx()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec("SET LOCAL search_path = " + pgQuoteIdent(schema) + ", public"); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	if _, err := tx.Exec("SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		t.Fatalf("set tenant_id: %v", err)
	}
	if _, err := tx.Exec("SELECT set_config('app.workspace_id', $1, true)", workspaceID); err != nil {
		t.Fatalf("set workspace_id: %v", err)
	}

	st := WrapTx(tx, DriverPostgres)
	got, err := st.ListResources(ResourceFilter{IncludeManaged: true, Limit: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("tx-bound list = %d; want 3", len(got))
	}

	// Close on tx-bound store must not close the caller's tx.
	if err := st.Close(); err != nil {
		t.Errorf("Close on tx-bound store: %v", err)
	}
	// Tx is still usable.
	if _, err := tx.Exec("SELECT 1"); err != nil {
		t.Errorf("tx unusable after Close: %v", err)
	}
}
