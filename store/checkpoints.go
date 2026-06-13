package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Checkpoint is a per-(scan, provider, service, scope) progress marker.
// Scanners save it after each successfully-upserted page; on resume they read
// the latest row and pass `LastToken` back to the upstream SDK as a
// continuation cursor. The token is opaque to the store — this schema is the
// foundation for a future incremental-scan feature (see ROADMAP.md).
type Checkpoint struct {
	ScanID    string
	Provider  string
	Service   string
	Scope     string
	LastToken string
	UpdatedAt time.Time
}

// SaveCheckpoint upserts a checkpoint row. lastToken may be empty (the page
// returned no continuation token); callers persist that case so resume logic
// distinguishes "completed" from "never started". Updates `updated_at` on
// every call so stale checkpoints can be detected.
func (s *Store) SaveCheckpoint(scanID, provider, service, scope, lastToken string) error {
	_, err := s.exec(
		`
		INSERT INTO scan_checkpoints (scan_id, provider, service, scope, last_token, updated_at)
		VALUES (?, ?, ?, ?, ?, `+s.nowExpr()+`)
		ON CONFLICT (scan_id, provider, service, scope) DO UPDATE
		SET last_token = excluded.last_token,
			updated_at = excluded.updated_at`,
		scanID, provider, service, scope, lastToken,
	)
	if err != nil {
		return fmt.Errorf("save checkpoint: %w", err)
	}
	return nil
}

// GetCheckpoint returns the latest token for the (scan, provider, service,
// scope) tuple, or ("", false, nil) when no row exists. Resume logic should
// fall back to a fresh scan when ok=false.
func (s *Store) GetCheckpoint(scanID, provider, service, scope string) (lastToken string, ok bool, err error) {
	row := s.queryRow(
		`
		SELECT last_token FROM scan_checkpoints
		WHERE scan_id = ? AND provider = ? AND service = ? AND scope = ?`,
		scanID, provider, service, scope,
	)
	var t sql.NullString
	switch err := row.Scan(&t); {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("get checkpoint: %w", err)
	}
	if !t.Valid {
		return "", true, nil
	}
	return t.String, true, nil
}

// ListCheckpoints returns every checkpoint for a scan, ordered by service then
// scope so callers iterate deterministically. Used by the --resume CLI to
// summarise pending work, and by a future incremental scanner that pre-fetches
// all checkpoints in one round-trip.
func (s *Store) ListCheckpoints(scanID string) ([]Checkpoint, error) {
	rows, err := s.query(
		`
		SELECT scan_id, provider, service, scope, last_token, updated_at
		FROM scan_checkpoints
		WHERE scan_id = ?
		ORDER BY service, scope`, scanID,
	)
	if err != nil {
		return nil, fmt.Errorf("list checkpoints: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Checkpoint
	for rows.Next() {
		var c Checkpoint
		var token sql.NullString
		var updatedAt string
		if err := rows.Scan(&c.ScanID, &c.Provider, &c.Service, &c.Scope, &token, &updatedAt); err != nil {
			return nil, err
		}
		if token.Valid {
			c.LastToken = token.String
		}
		// SQLite datetime('now') returns "YYYY-MM-DD HH:MM:SS"; parse loosely
		// — failure is non-fatal, leave UpdatedAt zero.
		if t, terr := time.Parse("2006-01-02 15:04:05", updatedAt); terr == nil {
			c.UpdatedAt = t
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteScanCheckpoints removes every checkpoint for a scan. Called once the
// scan completes so the table stays bounded — abandoned scan rows live until
// explicit cleanup or the next resume of the same scan_id.
func (s *Store) DeleteScanCheckpoints(scanID string) error {
	_, err := s.exec("DELETE FROM scan_checkpoints WHERE scan_id = ?", scanID)
	if err != nil {
		return fmt.Errorf("delete scan checkpoints: %w", err)
	}
	return nil
}
