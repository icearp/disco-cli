package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Scan represents a single discovery run.
type Scan struct {
	ID            string   `db:"id"`
	StartedAt     string   `db:"started_at"`
	FinishedAt    *string  `db:"finished_at"`
	Status        string   `db:"status"`
	Providers     []string `db:"-"` // stored as JSON
	ProvidersJSON string   `db:"providers"`
	ScopeJSON     string   `db:"scope"`
	Error         *string  `db:"error"`
	// ErrorsJSON is the structured per-service failure array, JSON-encoded.
	// SQLite stores it as TEXT, PG as JSONB; both round-trip through this
	// string field. Default '[]' so SELECT * never NULL-scans.
	ErrorsJSON    *string `db:"errors"`
	ResourceCount *int    `db:"resource_count"`
	MetaJSON      *string `db:"meta"`
	// WorkspaceID is the per-workspace RLS discriminator. Omitted from the read
	// projection (scanColumns) like Resource.WorkspaceID — the single-tenant store
	// never reads it and disco-saas's RLS layer filters per-workspace, so it stays nil.
	// Kept as a field so the type still documents the column.
	WorkspaceID *string `db:"workspace_id" json:"-"`
}

// scanColumns is the explicit read projection for the scans table. Like
// resourceSelectColumns, it omits the RLS columns (workspace_id, plus the
// tenant_id disco-saas overlays onto the same table) so a read never collides
// with columns the control plane adds — disco is consumed as a module whose
// tables disco-saas extends. Keep in sync with the Scan struct's db tags.
const scanColumns = "id, started_at, finished_at, status, providers, scope, " +
	"error, errors, resource_count, meta"

// scanWire is the on-the-wire JSON shape for Scan: camelCase keys, parsed
// providers/scope/meta objects, RFC3339 timestamps. The raw `*JSON` SQLite
// column strings stay internal.
type scanWire struct {
	ID            string         `json:"id"`
	StartedAt     string         `json:"startedAt"`
	FinishedAt    *string        `json:"finishedAt"`
	Status        string         `json:"status"`
	Providers     []string       `json:"providers"`
	Scope         map[string]any `json:"scope"`
	Error         *string        `json:"error"`
	ResourceCount *int           `json:"resourceCount"`
	Meta          map[string]any `json:"meta"`
}

// MarshalJSON renders a Scan with camelCase keys, parsed providers / scope /
// meta objects, and RFC3339 timestamps. The SQLite `datetime('now')` shape
// (`YYYY-MM-DD HH:MM:SS`) is normalised so consumers can use a single
// `time.Parse(time.RFC3339, ...)` regardless of row source.
func (s Scan) MarshalJSON() ([]byte, error) {
	w := scanWire{
		ID:            s.ID,
		StartedAt:     toRFC3339(s.StartedAt),
		FinishedAt:    rfc3339Ptr(s.FinishedAt),
		Status:        s.Status,
		Providers:     s.Providers,
		Error:         s.Error,
		ResourceCount: s.ResourceCount,
	}
	if w.Providers == nil && s.ProvidersJSON != "" {
		_ = json.Unmarshal([]byte(s.ProvidersJSON), &w.Providers)
	}
	if w.Providers == nil {
		w.Providers = []string{}
	}
	if s.ScopeJSON != "" {
		_ = json.Unmarshal([]byte(s.ScopeJSON), &w.Scope)
	}
	if w.Scope == nil {
		w.Scope = map[string]any{}
	}
	if s.MetaJSON != nil && *s.MetaJSON != "" {
		_ = json.Unmarshal([]byte(*s.MetaJSON), &w.Meta)
	}
	if w.Meta == nil {
		w.Meta = map[string]any{}
	}
	return json.Marshal(w)
}

// ToRFC3339 is the exported alias for callers outside the package
// (cmd/summary.go) that need to project a SQLite-flavoured timestamp.
func ToRFC3339(s string) string { return toRFC3339(s) }

// toRFC3339 normalises a SQLite `datetime('now')` string
// (`YYYY-MM-DD HH:MM:SS` UTC) to RFC3339. Empty input passes through.
// Already-RFC3339 input round-trips unchanged. Anything unparseable is
// returned untouched — better to surface a literal than swallow the value.
func toRFC3339(s string) string {
	if s == "" {
		return s
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return s
}

func rfc3339Ptr(p *string) *string {
	if p == nil {
		return nil
	}
	v := toRFC3339(*p)
	return &v
}

// CreateScan inserts a new scan record with status "running" and returns its
// ID (freshly minted, 32-hex). Use CreateScanWithID when the caller (e.g. an
// external orchestrator) needs to assign the id ahead of time so its audit
// trail / resources / scans share a single identifier.
func (s *Store) CreateScan(providers []string, scope map[string]any) (string, error) {
	return s.CreateScanWithID("", providers, scope)
}

// CreateScanWithID inserts a new scan record with the supplied id. An empty
// id falls back to freshly-minted 32-hex (CreateScan's legacy behaviour).
func (s *Store) CreateScanWithID(id string, providers []string, scope map[string]any) (string, error) {
	if id == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return "", fmt.Errorf("generate scan id: %w", err)
		}
		id = hex.EncodeToString(b)
	}
	provJSON, err := json.Marshal(providers)
	if err != nil {
		return "", err
	}
	scopeJSON, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	// ON CONFLICT DO UPDATE so an external orchestrator that pre-claimed the
	// row with attribution metadata (principal_arn, account_id,
	// triggered_by, …) keeps that data while the scanner takes over
	// status / started_at. SQLite tolerates ON CONFLICT(id) the same way but
	// uses INSERT-OR-IGNORE semantics on the unchanged columns.
	_, err = s.exec(
		`
		INSERT INTO scans (id, started_at, status, providers, scope)
		VALUES (?, `+s.nowExpr()+`, 'running', ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			started_at = EXCLUDED.started_at,
			status     = 'running',
			providers  = EXCLUDED.providers,
			scope      = EXCLUDED.scope`,
		id, string(provJSON), string(scopeJSON),
	)
	if err != nil {
		return "", fmt.Errorf("create scan: %w", err)
	}
	return id, nil
}

// CompleteScan marks a scan as completed and records the resource count.
func (s *Store) CompleteScan(id string, resourceCount int) error {
	_, err := s.exec(
		`
		UPDATE scans
		SET status = 'completed', finished_at = `+s.nowExpr()+`, resource_count = ?
		WHERE id = ?`,
		resourceCount, id,
	)
	return err
}

// FailScan marks a scan as failed with an error message.
func (s *Store) FailScan(id string, scanErr string) error {
	_, err := s.exec(
		`
		UPDATE scans
		SET status = 'failed', finished_at = `+s.nowExpr()+`, error = ?
		WHERE id = ?`,
		scanErr, id,
	)
	return err
}

// PartialScan marks a scan as partially completed: at least one provider
// succeeded while others failed. The error message should summarize which
// providers failed and why.
func (s *Store) PartialScan(id string, resourceCount int, scanErr string) error {
	_, err := s.exec(
		`
		UPDATE scans
		SET status = 'partial', finished_at = `+s.nowExpr()+`, resource_count = ?, error = ?
		WHERE id = ?`,
		resourceCount, scanErr, id,
	)
	return err
}

// ScanErrorEntry is one structured failure row appended to scans.errors.
// service / region narrow the failure scope for UI grouping/filtering; code
// mirrors the AWS API error code (or a synthesised "transient" / "auth" /
// "throttle" for non-AWS providers); message is human-readable but terse.
type ScanErrorEntry struct {
	Service string `json:"service"`
	Region  string `json:"region"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// AppendScanError appends one structured entry to scans.errors. PG path
// uses jsonb_insert; the SQLite fallback JSON-encodes the array
// once and rewrites. Both are concurrency-safe under the existing
// per-scan write pattern (one scanner mutates one scan).
func (s *Store) AppendScanError(id string, e ScanErrorEntry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if s.driver == driverPostgres {
		_, err = s.exec(
			`UPDATE scans
			 SET errors = errors || $1::jsonb
			 WHERE id = $2`, string(b), id)
		return err
	}
	// SQLite path: read-modify-write. Acceptable for single-tenant CLI usage.
	var raw string
	if err := s.get(&raw, `SELECT COALESCE(errors, '[]') FROM scans WHERE id = ?`, id); err != nil {
		return err
	}
	var list []ScanErrorEntry
	_ = json.Unmarshal([]byte(raw), &list)
	list = append(list, e)
	out, err := json.Marshal(list)
	if err != nil {
		return err
	}
	_, err = s.exec(`UPDATE scans SET errors = ? WHERE id = ?`, string(out), id)
	return err
}

// GetScan retrieves a scan by ID.
func (s *Store) GetScan(id string) (*Scan, error) {
	var sc Scan
	err := s.get(&sc, "SELECT "+scanColumns+" FROM scans WHERE id = ?", id)
	if err != nil {
		return nil, fmt.Errorf("get scan %s: %w", id, err)
	}
	if err := json.Unmarshal([]byte(sc.ProvidersJSON), &sc.Providers); err != nil {
		return nil, fmt.Errorf("unmarshal providers: %w", err)
	}
	return &sc, nil
}

// LatestIncompleteScan returns the most-recent scan whose status is "running"
// or "partial" — the candidate for `disco scan --resume` to pick up. Returns
// (nil, nil) when no such scan exists. Status "failed" is excluded — those
// require manual triage rather than blind resume.
func (s *Store) LatestIncompleteScan() (*Scan, error) {
	var sc Scan
	err := s.get(&sc, `
		SELECT `+scanColumns+` FROM scans
		WHERE status IN ('running','partial')
		ORDER BY started_at DESC
		LIMIT 1`)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(sc.ProvidersJSON), &sc.Providers); err != nil {
		return nil, fmt.Errorf("unmarshal providers: %w", err)
	}
	return &sc, nil
}

// LatestCompleteScan returns the most-recent scan with status "complete" or
// "partial" that names the given provider (or any provider when provider="").
// Returns sql.ErrNoRows when no such scan exists. Used by
// `disco scan --if-older-than` to skip cron-driven re-scans when a recent run
// already covered the same provider scope.
func (s *Store) LatestCompleteScan(provider string) (*Scan, error) {
	var sc Scan
	q := `SELECT ` + scanColumns + ` FROM scans WHERE status IN ('completed','partial')`
	args := []any{}
	if provider != "" {
		q += ` AND providers LIKE ?`
		args = append(args, "%\""+provider+"\"%")
	}
	q += ` ORDER BY started_at DESC, rowid DESC LIMIT 1`
	if err := s.get(&sc, q, args...); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(sc.ProvidersJSON), &sc.Providers); err != nil {
		return nil, fmt.Errorf("unmarshal providers: %w", err)
	}
	return &sc, nil
}

// ListScans returns all scans ordered by start time descending. Providers
// is decoded per row so callers can render the slice without a follow-up
// GetScan fan-out.
func (s *Store) ListScans() ([]Scan, error) {
	var scans []Scan
	// Tie-break by rowid so two scans created within the same SQLite-second
	// (datetime('now') has 1s resolution) order deterministically: newer
	// rowid wins. Required by `disco scans show latest` consumers.
	if err := s.selectAll(&scans, "SELECT "+scanColumns+" FROM scans ORDER BY started_at DESC, rowid DESC"); err != nil {
		return nil, err
	}
	for i := range scans {
		if scans[i].ProvidersJSON == "" {
			continue
		}
		if err := json.Unmarshal([]byte(scans[i].ProvidersJSON), &scans[i].Providers); err != nil {
			return nil, fmt.Errorf("unmarshal providers for scan %s: %w", scans[i].ID, err)
		}
	}
	return scans, nil
}
