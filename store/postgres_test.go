package store

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// pgTestEnv spins up an ephemeral postgres:16-alpine container via the
// `docker` CLI and returns a DSN once the server accepts connections.
// Callers SHOULD defer purge. If Docker/Podman is unreachable the test
// skips, not fails — CI gates this on its own job.
func pgTestEnv(t *testing.T) (dsn string, purge func()) {
	t.Helper()
	if os.Getenv("DISCO_SKIP_DOCKERTEST") != "" {
		t.Skip("DISCO_SKIP_DOCKERTEST set")
	}
	if err := exec.Command("docker", "version").Run(); err != nil {
		t.Skipf("docker daemon unreachable: %v", err)
	}

	out, err := exec.Command("docker", "run", "-d", "--rm",
		"-e", "POSTGRES_PASSWORD=disco",
		"-e", "POSTGRES_USER=disco",
		"-e", "POSTGRES_DB=disco",
		"-p", "127.0.0.1::5432",
		"postgres:16-alpine",
	).Output()
	if err != nil {
		t.Skipf("docker run: %v", err)
	}
	id := strings.TrimSpace(string(out))
	purge = func() { _ = exec.Command("docker", "rm", "-f", id).Run() }

	portOut, err := exec.Command("docker", "port", id, "5432/tcp").Output()
	if err != nil {
		purge()
		t.Skipf("docker port: %v", err)
	}
	// "docker port" prints e.g. "127.0.0.1:54321" (one line for the single mapping).
	hostPort := strings.TrimSpace(string(portOut))
	dsn = fmt.Sprintf("postgres://disco:disco@%s/disco?sslmode=disable", hostPort)

	deadline := time.Now().Add(60 * time.Second)
	for {
		s, err := OpenPostgres(context.Background(), dsn)
		if err == nil {
			_ = s.Close()
			break
		}
		if time.Now().After(deadline) {
			purge()
			t.Skipf("pg never became ready: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return dsn, purge
}

func TestPG_OpenAndMigrate(t *testing.T) {
	dsn, purge := pgTestEnv(t)
	defer purge()

	s, err := OpenPostgres(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	// migrations 001, 002, 005, 006 applied (003 + 004 were SaaS-only, relocated).
	got, err := s.CurrentSchemaVersion()
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if got < 6 {
		t.Errorf("schema version = %d; want >= 6", got)
	}
}

// TestPG_RoundTripParity exercises the full read+write surface and asserts
// shapes match SQLite's for the same fixture set — the "PG looks like
// SQLite to call sites" guarantee.
func TestPG_RoundTripParity(t *testing.T) {
	dsn, purge := pgTestEnv(t)
	defer purge()

	pgS, err := OpenPostgres(context.Background(), dsn)
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

// Tenant isolation (RLS) is no longer part of the single-tenant schema — disco-saas
// layers tenant_id + row-level security onto these tables via its own
// migration set, and the RLS isolation test lives there. This suite proves
// the public extension point those layers ride on: TestPG_WithAfterConnect
// (per-conn hook fires) and TestPG_WrapTx (tx-bound request-path store).

// TestPG_WithAfterConnect proves a WithAfterConnect hook runs on every
// physical connection — the seam disco-saas uses to SET search_path + the
// app.* RLS GUCs. The hook sets a session GUC; read back through a pooled
// handle.
func TestPG_WithAfterConnect(t *testing.T) {
	dsn, purge := pgTestEnv(t)
	defer purge()

	const marker = "disco-marker"
	s, err := OpenPostgres(context.Background(), dsn,
		WithAfterConnect(func(ctx context.Context, c *pgconn.PgConn) error {
			mrr := c.Exec(ctx, "SELECT set_config('app.test_marker', '"+marker+"', false)")
			_, err := mrr.ReadAll()
			return err
		}),
	)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	var got string
	if err := s.queryRow("SELECT current_setting('app.test_marker', true)").Scan(&got); err != nil {
		t.Fatalf("read GUC: %v", err)
	}
	if got != marker {
		t.Errorf("app.test_marker = %q; want %q (AfterConnect hook did not fire)", got, marker)
	}
}

// TestPG_WrapTx exercises read methods on a tx-bound *Store — the
// request-path primitive disco-saas wraps after pinning search_path + RLS
// GUCs on the tx. Plain pool, no schema pin: proves WrapTx reads through the
// caller's tx and Close on the tx-bound store leaves that tx open.
func TestPG_WrapTx(t *testing.T) {
	dsn, purge := pgTestEnv(t)
	defer purge()

	pool, err := OpenPostgres(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer func() { _ = pool.Close() }()
	seedFixtures(t, pool)

	tx, err := pool.DB().Beginx()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

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
	if _, err := tx.Exec("SELECT 1"); err != nil {
		t.Errorf("tx unusable after Close: %v", err)
	}
}

// TestPG_ConcurrentUpsert hammers UpsertResources from many goroutines on the
// same key set; idempotency + FK constraints must hold under concurrency.
func TestPG_ConcurrentUpsert(t *testing.T) {
	dsn, purge := pgTestEnv(t)
	defer purge()

	s, err := OpenPostgres(context.Background(), dsn)
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
