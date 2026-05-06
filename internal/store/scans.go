package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	_, err = s.db.Exec(`
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
	_, err := s.db.Exec(`
		UPDATE scans
		SET status = 'completed', finished_at = datetime('now'), resource_count = ?
		WHERE id = ?`,
		resourceCount, id,
	)
	return err
}

// FailScan marks a scan as failed with an error message.
func (s *Store) FailScan(id string, scanErr string) error {
	_, err := s.db.Exec(`
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
	_, err := s.db.Exec(`
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
