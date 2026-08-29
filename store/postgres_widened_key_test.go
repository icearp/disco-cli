package store

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestPG_InsertIfAbsentSurvivesAWidenedNaturalKey pins the reason the
// first-discovery inserts name no ON CONFLICT target.
//
// An embedder may add a column to the current-by-natural-key index — a
// multi-tenant host scoping it per workspace is the case this was written for,
// where two workspaces legitimately hold the same provider/account/native id.
// Postgres requires an inferred conflict target to match a live unique index
// exactly, so an INSERT naming (provider, account_id, native_id) against a
// widened index fails 42P10 and takes the whole scan down. Untargeted, the same
// insert is correct against either shape.
//
// The test widens the index the way an embedder would and asserts both halves:
// a row under a second scope INSERTS rather than erroring or being swallowed,
// and a repeat under one scope is still the no-op the clause exists for.
func TestPG_InsertIfAbsentSurvivesAWidenedNaturalKey(t *testing.T) {
	dsn, purge := pgTestEnv(t)
	defer purge()

	s, err := OpenPostgres(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	seedFixtures(t, s)

	// Stand in for the embedder's workspace column: same name for the index,
	// so disco's own CREATE UNIQUE INDEX IF NOT EXISTS stays a no-op on a
	// later migration pass.
	for _, ddl := range []string{
		`ALTER TABLE resources ADD COLUMN scope text NOT NULL DEFAULT 'scope-a'`,
		`DROP INDEX idx_resources_current_by_natural_key`,
		`CREATE UNIQUE INDEX idx_resources_current_by_natural_key
		   ON resources (scope, provider, account_id, native_id)
		   WHERE superseded_by IS NULL`,
	} {
		if _, err := s.exec(ddl); err != nil {
			t.Fatalf("widen the natural key (%s): %v", ddl, err)
		}
	}

	newPlaceholder := func() *Resource {
		return &Resource{
			Provider: "aws", AccountID: "333333333333", Type: "aws:iam:account",
			NativeID:       "arn:aws:iam::333333333333:root",
			AttributesJSON: "{}", DiscoveredBy: pgTestScanID,
		}
	}

	n, err := s.InsertResourcesIfAbsent([]*Resource{newPlaceholder()})
	if err != nil {
		t.Fatalf("insert under scope-a: %v", err)
	}
	if n != 1 {
		t.Fatalf("inserted under scope-a = %d, want 1", n)
	}

	// Same natural key, second scope. This is the row the targeted clause lost.
	if _, err := s.exec(`ALTER TABLE resources ALTER COLUMN scope SET DEFAULT 'scope-b'`); err != nil {
		t.Fatalf("switch scope default: %v", err)
	}
	n, err = s.InsertResourcesIfAbsent([]*Resource{newPlaceholder()})
	if err != nil {
		t.Fatalf("insert under scope-b: %v", err)
	}
	if n != 1 {
		t.Errorf("inserted under scope-b = %d, want 1; the second scope's row was dropped", n)
	}

	// The race guard the clause exists for still holds within one scope.
	n, err = s.InsertResourcesIfAbsent([]*Resource{newPlaceholder()})
	if err != nil {
		t.Fatalf("repeat insert under scope-b: %v", err)
	}
	if n != 0 {
		t.Errorf("repeat insert under scope-b = %d, want 0", n)
	}

	var rows int
	if err := s.db.Get(&rows, `SELECT count(*) FROM resources
		WHERE provider = 'aws' AND account_id = '333333333333'
		  AND native_id = 'arn:aws:iam::333333333333:root'
		  AND superseded_by IS NULL`); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 2 {
		t.Errorf("current rows for the shared natural key = %d, want 2 (one per scope)", rows)
	}
}

// TestPG_ScanDataWritesSurviveWidenedKeys covers the other five changed
// statements — the edge upsert (single and batch), upsertResourcesTx's
// first-discovery INSERT, insertFirstQuota, and recordHierarchyTx's closure
// inserts plus its contains edge — against all four widened keys.
//
// The edge half guards a sharper failure than the test above. Edge endpoints are the deterministic
// ResourceID hash, so two scopes scanning one account emit byte-identical
// (from_id, to_id, kind). The clause this replaced was ON CONFLICT … DO UPDATE,
// which under an embedder's row-level security lands on a row the writer cannot
// see and raises 42501 rather than skipping — a failed scan, not an empty one.
// The old form cannot get that far HERE: the index is widened before anything
// writes, so it fails 42P10 at plan time. 42501 is what it does against the
// NARROW key, the state this change moves off of; issue #258 records that
// measurement and this test does not pin it.
//
// The scoping is modelled the way an embedder actually installs it, with a
// policy and a non-superuser role, because the UPDATE half of the upsert has no
// scope predicate of its own: it is the embedder's RLS that keeps one scope's
// UPDATE off another scope's row. A widened index with no policy behind it
// would let the UPDATE cross scopes, and this test would not show it.
func TestPG_ScanDataWritesSurviveWidenedKeys(t *testing.T) {
	dsn, purge := pgTestEnv(t)
	defer purge()

	owner, err := OpenPostgres(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = owner.Close() }()
	seedFixtures(t, owner)

	for _, ddl := range []string{
		// Three steps, as disco-saas does it: a NOT NULL column whose DEFAULT
		// reads a GUC would evaluate that default on THIS connection, which has
		// none. It survives as one statement only while the table is empty.
		`ALTER TABLE relationships ADD COLUMN scope text`,
		`ALTER TABLE relationships ALTER COLUMN scope SET DEFAULT current_setting('app.scope', true)`,
		`ALTER TABLE relationships ALTER COLUMN scope SET NOT NULL`,
		`ALTER TABLE relationships DROP CONSTRAINT relationships_from_id_to_id_kind_key`,
		`CREATE UNIQUE INDEX relationships_from_id_to_id_kind_key
		   ON relationships (scope, from_id, to_id, kind)`,
		`ALTER TABLE relationships ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE relationships FORCE ROW LEVEL SECURITY`,
		`CREATE POLICY scope_isolation ON relationships USING (scope = current_setting('app.scope', true))`,
		// resources, quotas and hierarchy_closure carry the other four changed
		// statements: the first-discovery INSERT in upsertResourcesTx,
		// insertFirstQuota, and recordHierarchyTx's two closure inserts plus its
		// contains edge. Same widening, same policy.
		`ALTER TABLE resources ADD COLUMN scope text`,
		`ALTER TABLE resources ALTER COLUMN scope SET DEFAULT current_setting('app.scope', true)`,
		`UPDATE resources SET scope = 'seed'`,
		`ALTER TABLE resources ALTER COLUMN scope SET NOT NULL`,
		`DROP INDEX idx_resources_current_by_natural_key`,
		`CREATE UNIQUE INDEX idx_resources_current_by_natural_key
		   ON resources (scope, provider, account_id, native_id) WHERE superseded_by IS NULL`,
		`ALTER TABLE resources ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE resources FORCE ROW LEVEL SECURITY`,
		`CREATE POLICY scope_isolation ON resources USING (scope = current_setting('app.scope', true))`,

		`ALTER TABLE quotas ADD COLUMN scope text`,
		`ALTER TABLE quotas ALTER COLUMN scope SET DEFAULT current_setting('app.scope', true)`,
		`ALTER TABLE quotas ALTER COLUMN scope SET NOT NULL`,
		`DROP INDEX idx_quotas_current_by_natural_key`,
		`CREATE UNIQUE INDEX idx_quotas_current_by_natural_key
		   ON quotas (scope, provider, account_id, service_code, quota_code, region, dimension_key)
		   WHERE superseded_by IS NULL`,
		`ALTER TABLE quotas ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE quotas FORCE ROW LEVEL SECURITY`,
		`CREATE POLICY scope_isolation ON quotas USING (scope = current_setting('app.scope', true))`,

		`ALTER TABLE hierarchy_closure ADD COLUMN scope text`,
		`ALTER TABLE hierarchy_closure ALTER COLUMN scope SET DEFAULT current_setting('app.scope', true)`,
		`ALTER TABLE hierarchy_closure ALTER COLUMN scope SET NOT NULL`,
		`ALTER TABLE hierarchy_closure DROP CONSTRAINT hierarchy_closure_pkey`,
		`ALTER TABLE hierarchy_closure
		   ADD CONSTRAINT hierarchy_closure_pkey PRIMARY KEY (scope, ancestor_id, descendant_id)`,
		`ALTER TABLE hierarchy_closure ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE hierarchy_closure FORCE ROW LEVEL SECURITY`,
		`CREATE POLICY scope_isolation ON hierarchy_closure USING (scope = current_setting('app.scope', true))`,

		`CREATE ROLE embedder LOGIN PASSWORD 'embedder'`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON relationships TO embedder`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON resources TO embedder`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON quotas TO embedder`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON hierarchy_closure TO embedder`,
		`GRANT SELECT ON scans TO embedder`,
		// OpenPostgres re-checks the migration ledger on open; the schema is
		// already current, so read access is all the embedder role needs.
		`GRANT SELECT ON schema_migrations TO embedder`,
	} {
		if _, err := owner.exec(ddl); err != nil {
			t.Fatalf("install the embedder shape (%s): %v", ddl, err)
		}
	}

	// A store per scope, pinning the GUC on every connection the pool opens —
	// the same shape disco-saas uses to carry tenant and workspace.
	openScoped := func(scope string) *Store {
		t.Helper()
		scoped := strings.Replace(dsn, "postgres://disco:disco@", "postgres://embedder:embedder@", 1)
		st, err := OpenPostgres(context.Background(), scoped,
			WithAfterConnect(func(ctx context.Context, c *pgconn.PgConn) error {
				_, err := c.Exec(ctx, "SELECT set_config('app.scope', '"+scope+"', false)").ReadAll()
				return err
			}))
		if err != nil {
			t.Fatalf("open as %s: %v", scope, err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return st
	}

	type row struct {
		Scope      string  `db:"scope"`
		Attributes *string `db:"attributes"`
	}

	const (
		from = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		to   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	first, second := `{"seen":"first"}`, `{"seen":"second"}`

	a := openScoped("scope-a")
	if err := a.UpsertRelationship(from, to, RelUses, "directed", &first); err != nil {
		t.Fatalf("upsert under scope-a: %v", err)
	}
	// Re-upsert in the same scope: refreshed in place, never duplicated.
	if err := a.UpsertRelationship(from, to, RelUses, "directed", &second); err != nil {
		t.Fatalf("re-upsert under scope-a: %v", err)
	}

	b := openScoped("scope-b")
	if err := b.UpsertRelationship(from, to, RelUses, "directed", &first); err != nil {
		t.Fatalf("upsert under scope-b: %v", err)
	}

	var rows []row
	if err := owner.db.Select(&rows, `SELECT scope, attributes FROM relationships
		WHERE from_id = $1 AND to_id = $2 AND kind = $3 ORDER BY scope`, from, to, RelUses); err != nil {
		t.Fatalf("select edges: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("edges for the shared key = %d, want 2 (one per scope)", len(rows))
	}
	// attributes is JSONB on Postgres, so the stored text is re-spaced; compare
	// as JSON rather than pinning the driver's rendering.
	if rows[0].Scope != "scope-a" || !jsonEqual(derefStr(rows[0].Attributes), second) {
		t.Errorf("scope-a edge = {%s %s}; want the refreshed attributes %s", rows[0].Scope, derefStr(rows[0].Attributes), second)
	}
	if rows[1].Scope != "scope-b" || !jsonEqual(derefStr(rows[1].Attributes), first) {
		t.Errorf("scope-b edge = {%s %s}; want its own attributes %s", rows[1].Scope, derefStr(rows[1].Attributes), first)
	}
	// The batch form is what scanners actually reach through BeginRelBuffer, and
	// it prepares its own statements, so cover it on its own edge key.
	const to2 = "cccccccccccccccccccccccccccccccc"
	batch := []RelEdge{
		// Already written by the single-edge path above, so the loop takes the
		// UPDATE branch for this one and the INSERT branch for the next, with
		// both prepared statements reused.
		{FromID: from, ToID: to, Kind: RelUses, Direction: "directed", Attrs: &second},
		{FromID: from, ToID: to2, Kind: RelAttachedTo, Direction: "directed", Attrs: &first},
		// Repeated inside one batch: the repeat's UPDATE has to see the INSERT
		// its predecessor made in the same uncommitted transaction.
		{FromID: from, ToID: to2, Kind: RelAttachedTo, Direction: "directed", Attrs: &second},
	}
	if err := a.UpsertRelationships(batch); err != nil {
		t.Fatalf("batch upsert under scope-a: %v", err)
	}
	if err := b.UpsertRelationships(batch); err != nil {
		t.Fatalf("batch upsert under scope-b: %v", err)
	}
	var batched []row
	if err := owner.db.Select(&batched, `SELECT scope, attributes FROM relationships
		WHERE from_id = $1 AND to_id = $2 AND kind = $3 ORDER BY scope`, from, to2, RelAttachedTo); err != nil {
		t.Fatalf("select batched edges: %v", err)
	}
	if len(batched) != 2 {
		t.Fatalf("batched edges for the shared key = %d, want 2 (one per scope, deduped within each batch)", len(batched))
	}
	for i, wantScope := range []string{"scope-a", "scope-b"} {
		if batched[i].Scope != wantScope || !jsonEqual(derefStr(batched[i].Attributes), second) {
			t.Errorf("batched edge %d = {%s %s}; want {%s %s} — the repeat inside the batch should have won",
				i, batched[i].Scope, derefStr(batched[i].Attributes), wantScope, second)
		}
	}

	// The remaining four changed statements, driven through their public entry
	// points under the same two scopes: upsertResourcesTx's first-discovery
	// INSERT, insertFirstQuota, and recordHierarchyTx's two closure inserts plus
	// its contains edge. Each must record a row per scope rather than losing the
	// second one to a key that spans both.
	mkResource := func(native string) *Resource {
		return &Resource{
			Provider: "aws", AccountID: "444444444444", Type: "aws:ec2:instance",
			NativeID: native, AttributesJSON: "{}", DiscoveredBy: pgTestScanID,
		}
	}
	// Ids are the deterministic ResourceID hash, so they are the same under both
	// scopes and can be computed rather than read back off an upserted struct.
	parentID := ResourceID("aws", "444444444444", "i-parent")
	childID := ResourceID("aws", "444444444444", "i-child")
	for _, scoped := range []struct {
		scope string
		st    *Store
	}{{"scope-a", a}, {"scope-b", b}} {
		scope, st := scoped.scope, scoped.st
		if _, err := st.UpsertResources([]*Resource{mkResource("i-parent"), mkResource("i-child")}); err != nil {
			t.Fatalf("upsert resources under %s: %v", scope, err)
		}
		if _, err := st.UpsertQuotas([]*Quota{{
			Provider: "aws", AccountID: "444444444444", Region: "us-east-1",
			ServiceCode: "ec2", QuotaCode: "L-1216C47A", Name: "Running instances",
			AttributesJSON: "{}", DiscoveredBy: pgTestScanID,
		}}); err != nil {
			t.Fatalf("upsert quotas under %s: %v", scope, err)
		}
		// The parent's own self-entry first: the ancestor rows for a child are
		// selected FROM the closure, so a parent with no row of its own
		// contributes none.
		if err := st.RecordHierarchyBatch([][2]string{{parentID, parentID}, {childID, parentID}}); err != nil {
			t.Fatalf("record hierarchy under %s: %v", scope, err)
		}
	}

	for _, c := range []struct {
		what  string
		query string
		args  []any
	}{
		{
			"current resources", `SELECT count(*) FROM resources
			WHERE account_id = $1 AND native_id = $2 AND superseded_by IS NULL`,
			[]any{"444444444444", "i-child"},
		},
		{
			"current quotas", `SELECT count(*) FROM quotas
			WHERE account_id = $1 AND quota_code = $2 AND superseded_by IS NULL`,
			[]any{"444444444444", "L-1216C47A"},
		},
		{"closure rows", `SELECT count(*) FROM hierarchy_closure
			WHERE ancestor_id = $1 AND descendant_id = $2`, []any{parentID, childID}},
		{"contains edges", `SELECT count(*) FROM relationships
			WHERE from_id = $1 AND to_id = $2 AND kind = $3`, []any{parentID, childID, RelContains}},
	} {
		var n int
		if err := owner.db.Get(&n, c.query, c.args...); err != nil {
			t.Fatalf("count %s: %v", c.what, err)
		}
		if n != 2 {
			t.Errorf("%s for the shared key = %d, want 2 (one per scope)", c.what, n)
		}
	}
}
