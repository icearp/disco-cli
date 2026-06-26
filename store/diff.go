package store

import (
	"fmt"

	sq "github.com/Masterminds/squirrel"
)

// ScanDiff summarizes the resource delta between two scan runs A and B,
// where B is assumed to have run after A and to have scanned the same scope.
//
// Limitations: the current schema stores only the *latest* state of each
// resource, not per-scan snapshots. This means:
//   - Added is precise: resources whose discovered_by == B.ID were first
//     observed in scan B.
//   - Stale is approximate: resources whose verified_by == A.ID have not
//     been re-verified by B (or any later scan). They may be deleted in
//     the cloud, or simply outside B's scope.
//   - Updated (attribute drift) cannot be computed without historical
//     snapshots — not implemented.
type ScanDiff struct {
	FromScanID string     `json:"from_scan_id"` // "A"
	ToScanID   string     `json:"to_scan_id"`   // "B"
	Added      []Resource `json:"added"`        // first seen in scan B
	Stale      []Resource `json:"stale"`        // last seen in scan A, not refreshed by B
}

// DiffScans returns the resource delta between two scan runs.
// See ScanDiff for semantics and limitations.
func (s *Store) DiffScans(fromScanID, toScanID string) (*ScanDiff, error) {
	if fromScanID == toScanID {
		return nil, fmt.Errorf("from and to scan IDs must differ")
	}
	// Sanity-check both scans exist; surface a clear error otherwise.
	if _, err := s.GetScan(fromScanID); err != nil {
		return nil, fmt.Errorf("from scan: %w", err)
	}
	if _, err := s.GetScan(toScanID); err != nil {
		return nil, fmt.Errorf("to scan: %w", err)
	}

	added, err := s.selectResources(sq.Eq{"discovered_by": toScanID})
	if err != nil {
		return nil, fmt.Errorf("select added: %w", err)
	}
	stale, err := s.selectResources(sq.Eq{"verified_by": fromScanID})
	if err != nil {
		return nil, fmt.Errorf("select stale: %w", err)
	}

	return &ScanDiff{
		FromScanID: fromScanID,
		ToScanID:   toScanID,
		Added:      added,
		Stale:      stale,
	}, nil
}

// selectResources runs a current-version SELECT against `resources`
// with the given WHERE predicate, projecting `root_id AS id` so the
// caller-facing Resource.ID stays the deterministic hash. Stable
// ordering by (provider, type, name) for diff readability.
func (s *Store) selectResources(where sq.Sqlizer) ([]Resource, error) {
	q := applyCurrentVersionPredicate(sq.Select(resourceSelectColumns()...).From("resources")).
		Where(where).
		OrderBy("provider", "type", "name").
		PlaceholderFormat(s.placeholder())
	query, args, err := q.ToSql()
	if err != nil {
		return nil, err
	}
	var rs []Resource
	return rs, s.selectAll(&rs, query, args...)
}
