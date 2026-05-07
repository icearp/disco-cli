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
	ResourceCount *int     `db:"resource_count"`
	MetaJSON      *string  `db:"meta"`
}

// scanWire is the on-the-wire JSON shape for Scan: snake_case keys, parsed
// providers/scope/meta objects, RFC3339 timestamps. The raw `*JSON` SQLite
// column strings stay internal.
type scanWire struct {
	ID            string         `json:"id"`
	StartedAt     string         `json:"started_at"`
	FinishedAt    *string        `json:"finished_at"`
	Status        string         `json:"status"`
	Providers     []string       `json:"providers"`
	Scope         map[string]any `json:"scope"`
	Error         *string        `json:"error"`
	ResourceCount *int           `json:"resource_count"`
	Meta          map[string]any `json:"meta"`
}

// MarshalJSON renders a Scan with snake_case keys, parsed providers / scope
// / meta objects, and RFC3339 timestamps. The SQLite `datetime('now')`
// shape (`YYYY-MM-DD HH:MM:SS`) is normalised so consumers can use a
// single `time.Parse(time.RFC3339, ...)` regardless of the row source.
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

// CreateScan inserts a new scan record with status "running" and returns its ID.
func (s *Store) CreateScan(providers []string, scope map[string]any) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate scan id: %w", err)
	}
	id := hex.EncodeToString(b)
	provJSON, err := json.Marshal(providers)
	if err != nil {
		return "", err
	}
	scopeJSON, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(
		`
		INSERT INTO scans (id, started_at, status, providers, scope)
		VALUES (?, datetime('now'), 'running', ?, ?)`,
		id, string(provJSON), string(scopeJSON),
	)
	if err != nil {
		return "", fmt.Errorf("create scan: %w", err)
	}
	return id, nil
}

// CompleteScan marks a scan as completed and records the resource count.
func (s *Store) CompleteScan(id string, resourceCount int) error {
	_, err := s.db.Exec(
		`
		UPDATE scans
		SET status = 'completed', finished_at = datetime('now'), resource_count = ?
		WHERE id = ?`,
		resourceCount, id,
	)
	return err
}

// FailScan marks a scan as failed with an error message.
func (s *Store) FailScan(id string, scanErr string) error {
	_, err := s.db.Exec(
		`
		UPDATE scans
		SET status = 'failed', finished_at = datetime('now'), error = ?
		WHERE id = ?`,
		scanErr, id,
	)
	return err
}

// PartialScan marks a scan as partially completed: at least one provider
// succeeded while others failed. The error message should summarize which
// providers failed and why.
func (s *Store) PartialScan(id string, resourceCount int, scanErr string) error {
	_, err := s.db.Exec(
		`
		UPDATE scans
		SET status = 'partial', finished_at = datetime('now'), resource_count = ?, error = ?
		WHERE id = ?`,
		resourceCount, scanErr, id,
	)
	return err
}

// GetScan retrieves a scan by ID.
func (s *Store) GetScan(id string) (*Scan, error) {
	var sc Scan
	err := s.db.Get(&sc, "SELECT * FROM scans WHERE id = ?", id)
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
	err := s.db.Get(&sc, `
		SELECT * FROM scans
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
	q := `SELECT * FROM scans WHERE status IN ('completed','partial')`
	args := []any{}
	if provider != "" {
		q += ` AND providers LIKE ?`
		args = append(args, "%\""+provider+"\"%")
	}
	q += ` ORDER BY started_at DESC, rowid DESC LIMIT 1`
	if err := s.db.Get(&sc, q, args...); err != nil {
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
	if err := s.db.Select(&scans, "SELECT * FROM scans ORDER BY started_at DESC, rowid DESC"); err != nil {
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
