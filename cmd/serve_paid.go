//go:build paid

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"codeberg.org/icearp/disco/internal/license"
	"codeberg.org/icearp/disco/internal/serve"
	"codeberg.org/icearp/disco/internal/store"
)

var (
	serveListen        string
	serveJWTSecret     string
	servePGDSN         string
	serveTenantID      string
	serveReadTimeout   time.Duration
	serveWriteTimeout  time.Duration
	serveShutdownGrace time.Duration
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the disco scan-trigger HTTP API (paid)",
	Long: `Start a one-shot HTTP server that accepts a single POST /v1/scans,
runs the scan in the background, and self-terminates on completion.

Designed for Fargate per-scan workers: ECS RunTask launches the container
with DISCO_PG_DSN, DISCO_TENANT_ID, DISCO_JWT_SECRET in the env, Lambda
POSTs the scan scope, the container exits when the scan finishes. SaaS
polls the scans table directly for status.

Routes:
  GET  /v1/healthz   liveness, no auth
  POST /v1/scans     submit scan; 202 + scan_id; 409 if one is in-flight

Token: HS256 JWT signed with --jwt-secret. The token MUST carry a
'tenant' claim equal to --tenant-id; mismatched tenant returns 403.`,
	Args: cobra.NoArgs,
	RunE: runServe,
}

func runServe(cmd *cobra.Command, _ []string) error {
	if err := license.Require(); err != nil {
		return err
	}
	if serveJWTSecret == "" {
		serveJWTSecret = os.Getenv("DISCO_JWT_SECRET")
	}
	if serveJWTSecret == "" {
		return errors.New("--jwt-secret (or DISCO_JWT_SECRET env) is required")
	}
	if servePGDSN == "" {
		servePGDSN = os.Getenv("DISCO_PG_DSN")
	}
	if serveTenantID == "" {
		serveTenantID = os.Getenv("DISCO_TENANT_ID")
	}

	st, err := openServeStore(cmd.Context())
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	runner := serve.NewRunner(st)
	handler := serve.NewServer(serve.Config{
		Runner:    runner,
		JWTSecret: []byte(serveJWTSecret),
		TenantID:  serveTenantID,
	})

	srv := &http.Server{
		Addr:         serveListen,
		Handler:      handler,
		ReadTimeout:  serveReadTimeout,
		WriteTimeout: serveWriteTimeout,
		// disco serve handlers should never block on a slow client; idle
		// keepalive eats one container forever otherwise.
		IdleTimeout: 30 * time.Second,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "disco serve listening on %s\n", serveListen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "http listen: %v\n", err)
		}
	}()

	// One-shot lifecycle: exit when either the scan finishes (Done fires)
	// or a signal arrives. SIGTERM mid-scan: let scanrun finish writing
	// the partial scan row before shutdown to preserve audit trail.
	select {
	case <-runner.Done():
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "scan %s finished, shutting down\n", runner.CompletedScanID())
	case sig := <-sigCh:
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "received %s, shutting down\n", sig)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), serveShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if rerr := runner.CompletedErr(); rerr != nil {
		return fmt.Errorf("scan run failed: %w", rerr)
	}
	return nil
}

// openServeStore picks PG when --pg-dsn / DISCO_PG_DSN is set; falls back
// to the same SQLite path the rest of the CLI uses for local dev. Tenant
// ID is required for PG (UUID-validated) and ignored for SQLite (single
// tenant by definition).
func openServeStore(ctx context.Context) (*store.Store, error) {
	if servePGDSN == "" {
		return store.Open(defaultDBPath())
	}
	if serveTenantID == "" {
		return nil, errors.New("--tenant-id (or DISCO_TENANT_ID env) is required when --pg-dsn is set")
	}
	if _, err := uuid.Parse(serveTenantID); err != nil {
		return nil, fmt.Errorf("--tenant-id must be a UUID: %w", err)
	}
	return store.OpenPostgres(ctx, servePGDSN, serveTenantID)
}

func init() {
	serveCmd.Flags().StringVar(&serveListen, "listen", "0.0.0.0:7777",
		"address:port to listen on")
	serveCmd.Flags().StringVar(&serveJWTSecret, "jwt-secret", "",
		"HS256 secret for verifying bearer JWTs (or DISCO_JWT_SECRET env); required")
	serveCmd.Flags().StringVar(&servePGDSN, "pg-dsn", "",
		"Postgres DSN (or DISCO_PG_DSN env); when unset falls back to local SQLite for dev")
	serveCmd.Flags().StringVar(&serveTenantID, "tenant-id", "",
		"tenant UUID pinned for the lifetime of this process (or DISCO_TENANT_ID env); required when --pg-dsn is set")
	serveCmd.Flags().DurationVar(&serveReadTimeout, "read-timeout", 15*time.Second,
		"HTTP read timeout")
	serveCmd.Flags().DurationVar(&serveWriteTimeout, "write-timeout", 30*time.Second,
		"HTTP write timeout")
	serveCmd.Flags().DurationVar(&serveShutdownGrace, "shutdown-grace", 30*time.Second,
		"how long to wait for in-flight requests to drain after the scan completes / a signal arrives")
	rootCmd.AddCommand(serveCmd)
}
