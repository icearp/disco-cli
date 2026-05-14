//go:build !paid

package store

import (
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"codeberg.org/icearp/disco/internal/redact"
)

// UpsertResources bulk-upserts resources in a single transaction.
// OSS variant: no verification, no version chain. Conflict on the
// deterministic ResourceID hash updates the mutable fields in place.
// Returns the number of newly inserted rows.
//
// Paid variant lives in resources_upsert_paid.go and implements the
// version-split logic.
func (s *Store) UpsertResources(resources []*Resource) (inserted int, err error) {
	now := time.Now().UTC().Format(time.RFC3339)

	for _, r := range resources {
		if r.ID == "" {
			r.ID = ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID)
		}
		if r.DiscoveredAt == "" {
			r.DiscoveredAt = now
		}
		r.AttributesJSON = redact.Apply(r.Type, r.AttributesJSON)
	}

	// Count how many of these IDs already exist so the inserted return
	// is precise. Two passes keep the SQL simple.
	ids := make([]string, len(resources))
	for i, r := range resources {
		ids[i] = r.ID
	}
	q, args, _ := sq.Select("COUNT(*)").From("resources").
		Where(sq.Eq{"id": ids}).PlaceholderFormat(s.placeholder()).ToSql()
	var existing int
	if err := s.get(&existing, q, args...); err != nil {
		return 0, fmt.Errorf("count existing resources: %w", err)
	}
	inserted = len(resources) - existing

	tx, err := s.db.Beginx()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range resources {
		if _, err := tx.NamedExec(`
			INSERT INTO resources
				(id, provider, account_id, account_name, type, native_id, name,
				 region, zone, status, tags, attributes, created_at,
				 discovered_at, discovered_by, managed_by_provider)
			VALUES
				(:id, :provider, :account_id, :account_name, :type, :native_id, :name,
				 :region, :zone, :status, :tags, :attributes, :created_at,
				 :discovered_at, :discovered_by, :managed_by_provider)
			ON CONFLICT(id) DO UPDATE SET
				name                = excluded.name,
				region              = excluded.region,
				zone                = excluded.zone,
				status              = excluded.status,
				tags                = excluded.tags,
				attributes          = excluded.attributes,
				managed_by_provider = excluded.managed_by_provider`, r); err != nil {
			return 0, fmt.Errorf("upsert resource %s: %w", r.ID, err)
		}
	}
	return inserted, tx.Commit()
}
