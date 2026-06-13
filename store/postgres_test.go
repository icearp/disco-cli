package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
)

// pgTestEnv spins up an ephemeral postgres:16-alpine container and returns a
// DSN once the server accepts connections. Callers SHOULD defer the returned
// purge function. If Docker / Podman is unreachable the test is skipped, not
// failed — CI gates dockertest on its own job.
func pgTestEnv(t *testing.T) (dsn string, purge func()) {
	t.Helper()
	if os.Getenv("DISCO_SKIP_DOCKERTEST") != "" {
		t.Skip("DISCO_SKIP_DOCKERTEST set")
	}
	pool, err := dockertest.NewPool("")
	if err != nil {
		t.Skipf("dockertest pool unavailable: %v", err)
	}
	pool.MaxWait = 60 * time.Second
	if err := pool.Client.Ping(); err != nil {
		t.Skipf("docker daemon unreachable: %v", err)
	}
	res, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres",
		Tag:        "16-alpine",
		Env: []string{
			"POSTGRES_PASSWORD=disco",
			"POSTGRES_USER=disco",
			"POSTGRES_DB=disco",
			"listen_addresses='*'",
		},
	}, func(c *docker.HostConfig) {
		c.AutoRemove = true
		c.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	if err != nil {
		t.Skipf("dockertest run: %v", err)
	}
	purge = func() { _ = pool.Purge(res) }

	hostPort := res.GetHostPort("5432/tcp")
	dsn = fmt.Sprintf("postgres://disco:disco@%s/disco?sslmode=disable", hostPort)

	tenantID := uuid.NewString()
	if err := pool.Retry(func() error {
		s, err := OpenPostgres(context.Background(), dsn, tenantID)
		if err != nil {
			return err
		}
		_ = s.Close()
		return nil
	}); err != nil {
		purge()
		t.Skipf("pg never became ready: %v", err)
	}
	return dsn, purge
}

// pgAppRole creates a dedicated non-superuser, non-BYPASSRLS login role with
// full DML on the public schema and returns a DSN authenticating as it. RLS
// only constrains ordinary roles — the superuser pgTestEnv hands back bypasses
// row-level security even under FORCE — so any tenant-isolation assertion must
// run as this role. Call AFTER pgTestEnv's readiness open has migrated public
// (so GRANT ON ALL TABLES covers the full set). DROP ROLE IF EXISTS first keeps
// it idempotent within a container reused across pool.Retry attempts.
func pgAppRole(t *testing.T, adminDSN string) (appDSN string) {
	t.Helper()
	cfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	db := stdlib.OpenDB(*cfg)
	defer func() { _ = db.Close() }()

	for _, stmt := range []string{
		`DROP ROLE IF EXISTS disco_app`,
		`CREATE ROLE disco_app LOGIN PASSWORD 'disco' NOSUPERUSER NOBYPASSRLS`,
		`GRANT USAGE, CREATE ON SCHEMA public TO disco_app`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO disco_app`,
		`GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO disco_app`,
	} {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("app-role setup %q: %v", stmt, err)
		}
	}
	// pgTestEnv builds the DSN as postgres://disco:disco@host/disco?...; swap
	// the superuser userinfo for the app role's.
	return strings.Replace(adminDSN, "disco:disco@", "disco_app:disco@", 1)
}

func TestPG_OpenAndMigrate(t *testing.T) {
	dsn, purge := pgTestEnv(t)
	defer purge()
	tenantID := uuid.NewString()

	s, err := OpenPostgres(context.Background(), dsn, tenantID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	// migrations 001..005 applied.
	got, err := s.CurrentSchemaVersion()
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if got < 5 {
		t.Errorf("schema version = %d; want >= 5", got)
	}
}

// TestPG_RoundTripParity exercises the full read+write surface and asserts
// shapes match what SQLite returns for the same fixture set. This is the
// "PG looks like SQLite to call sites" guarantee.
func TestPG_RoundTripParity(t *testing.T) {
	dsn, purge := pgTestEnv(t)
	defer purge()
	tenantID := uuid.NewString()
	workspaceID := uuid.NewString()

	pgS, err := OpenPostgresWithWorkspace(context.Background(), dsn, tenantID, workspaceID)
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	defer func() { _ = pgS.Close() }()

	liteS := openTestStore(t)

	for _, st := range []*Store{liteS, pgS} {
		seedFixtures(t, st)
		got, err := st.ListResources(ResourceFilter{IncludeManaged: true, Limit: 100})
		if err != nil {
			t.Fatalf("[%s] list: %v", st.driver, err)
		}
		if len(got) != 3 {
			t.Errorf("[%s] list count = %d; want 3", st.driver, len(got))
		}
	}
}

// TestPG_RLS confirms that rows written under tenant A are invisible when a
// new pool is opened under tenant B. RLS is the single most load-bearing
// security guarantee for a multi-tenant deploy — break this and tenants leak.
func TestPG_RLS(t *testing.T) {
	dsn, purge := pgTestEnv(t)
	defer purge()

	// Migrations (incl. 007 FORCE ROW LEVEL SECURITY) have run under the
	// superuser in pgTestEnv. Assert isolation as a non-superuser role so RLS
	// is actually enforced — superusers bypass it even under FORCE.
	appDSN := pgAppRole(t, dsn)

	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	workspaceA := uuid.NewString()
	workspaceB := uuid.NewString()

	a, err := OpenPostgresWithWorkspace(context.Background(), appDSN, tenantA, workspaceA)
	if err != nil {
		t.Fatalf("open A: %v", err)
	}
	seedFixtures(t, a)
	gotA, err := a.ListResources(ResourceFilter{IncludeManaged: true, Limit: 100})
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(gotA) == 0 {
		t.Fatalf("tenant A wrote rows but list returned 0")
	}
	_ = a.Close()

	b, err := OpenPostgresWithWorkspace(context.Background(), appDSN, tenantB, workspaceB)
	if err != nil {
		t.Fatalf("open B: %v", err)
	}
	defer func() { _ = b.Close() }()
	gotB, err := b.ListResources(ResourceFilter{IncludeManaged: true, Limit: 100})
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(gotB) != 0 {
		t.Errorf("tenant B saw %d rows from tenant A", len(gotB))
	}
}

// TestPG_ConcurrentUpsert hammers UpsertResources from many goroutines on the
// same key set; idempotency + FK constraints must hold under concurrency.
func TestPG_ConcurrentUpsert(t *testing.T) {
	dsn, purge := pgTestEnv(t)
	defer purge()
	tenantID := uuid.NewString()
	workspaceID := uuid.NewString()

	s, err := OpenPostgresWithWorkspace(context.Background(), dsn, tenantID, workspaceID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	seedFixtures(t, s) // ensures scan row + at least one resource exist

	const goroutines = 20
	var (
		wg       sync.WaitGroup
		errCount atomic.Int64
	)
	scanID := pgTestScanID
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := &Resource{
				Provider: "aws", AccountID: "111111111111", Type: "aws:ec2:instance",
				NativeID:     "arn:aws:ec2:us-east-1:111111111111:instance/i-concurrent",
				DiscoveredBy: scanID, AttributesJSON: "{}",
			}
			if _, err := s.UpsertResources([]*Resource{r}); err != nil {
				errCount.Add(1)
				t.Logf("upsert err: %v", err)
			}
		}()
	}
	wg.Wait()
	if n := errCount.Load(); n > 0 {
		t.Errorf("concurrent upsert errors = %d; want 0", n)
	}
}

const pgTestScanID = "00000000000000000000000000000000"

// seedFixtures inserts a scan row + three resources. Used by both backends so
// the parity test exercises a non-trivial dataset.
func seedFixtures(t *testing.T, s *Store) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.exec(`
		INSERT INTO scans (id, started_at, status, providers, scope)
		VALUES (?, ?, 'running', '["aws"]', '{}')
		ON CONFLICT (id) DO NOTHING`, pgTestScanID, now); err != nil {
		t.Fatalf("seed scan: %v", err)
	}
	rs := []*Resource{
		{
			Provider: "aws", AccountID: "111111111111", Type: "aws:ec2:instance",
			NativeID:     "arn:aws:ec2:us-east-1:111111111111:instance/i-aaa",
			DiscoveredBy: pgTestScanID, AttributesJSON: `{"State":"running"}`,
		},
		{
			Provider: "aws", AccountID: "111111111111", Type: "aws:s3:bucket",
			NativeID:     "arn:aws:s3:::test-bucket-1",
			DiscoveredBy: pgTestScanID, AttributesJSON: `{"Region":"us-east-1"}`,
		},
		{
			Provider: "aws", AccountID: "111111111111", Type: "aws:iam:role",
			NativeID:     "arn:aws:iam::111111111111:role/foo",
			DiscoveredBy: pgTestScanID, AttributesJSON: `{}`,
		},
	}
	if _, err := s.UpsertResources(rs); err != nil {
		t.Fatalf("seed resources: %v", err)
	}
}
