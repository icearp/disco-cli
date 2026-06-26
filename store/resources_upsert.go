package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/volatile"
)

// UpsertResources bulk-upserts resources under the paid version-chain
// model. Caller-facing signature unchanged. Per-resource logic:
//
//   - First discovery (no row with this natural key has
//     superseded_by IS NULL): INSERT a new row with id = UUIDv7,
//     root_id = Resource.ID (deterministic hash), verified_at = now.
//   - Unchanged rescan (existing current row matches attributes +
//     tags): UPDATE verified_at / verified_by on the existing row;
//     top-level columns (name/region/zone/status/managed) update in
//     place. No new row.
//   - Changed rescan (attributes or tags differ): INSERT a new row
//     with a fresh UUIDv7 PK, inheriting discovered_at /
//     discovered_by from the chain root, linking
//     previous_version_id to the predecessor, and setting
//     verified_at / verified_by to the current scan. Then UPDATE the
//     previous row's superseded_by to point at the new row. The
//     attributes/tags on the previous row stay frozen.
//
// The returned `inserted` counts version splits + first discoveries
// (every INSERT into resources). Unchanged-rescan updates do not
// count.
//
// Idempotency: calling UpsertResources twice with the same payload
// produces one INSERT on the first call and one verified-only UPDATE
// on the second.
func (s *Store) UpsertResources(resources []*Resource) (inserted int, err error) {
	now := time.Now().UTC().Format(time.RFC3339)

	for _, r := range resources {
		if r.ID == "" {
			r.ID = ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID)
		}
		r.AttributesJSON = redact.Apply(r.Type, r.AttributesJSON)
		// Drop volatile fields (e.g. CloudWatch Logs UploadSequenceToken) that
		// AWS rotates on every read — left in, they version-split an unchanged
		// resource on every scan. Runs before the jsonEqual comparison below.
		r.AttributesJSON = volatile.Apply(r.Type, r.AttributesJSON)
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, r := range resources {
		// Lookup current version row by natural key. The partial
		// unique index idx_resources_current_by_natural_key makes
		// this O(1).
		const lookupSQL = `
			SELECT id AS version_row_id, root_id, attributes, tags,
			       discovered_at, discovered_by
			  FROM resources
			 WHERE provider=$1 AND account_id=$2 AND type=$3 AND native_id=$4
			   AND superseded_by IS NULL`

		var existing struct {
			VersionRowID   string  `db:"version_row_id"`
			RootID         string  `db:"root_id"`
			AttributesJSON string  `db:"attributes"`
			TagsJSON       *string `db:"tags"`
			DiscoveredAt   string  `db:"discovered_at"`
			DiscoveredBy   string  `db:"discovered_by"`
		}
		lookupErr := tx.Get(&existing, tx.Rebind(lookupSQL),
			r.Provider, r.AccountID, r.Type, r.NativeID)

		switch {
		case errors.Is(lookupErr, sql.ErrNoRows):
			// First discovery. Fresh root row.
			//
			// Scanners upsert concurrently (errgroup goroutines, each on its
			// own transaction), so a sibling can insert this natural key
			// between our lookup and this insert. ON CONFLICT DO NOTHING on the
			// current-by-natural-key partial index turns that race into a no-op
			// instead of a 23505 that would abort the whole batch tx — the
			// sibling has already recorded the row, so we skip it (no info lost:
			// same natural key, same point-in-time scan).
			if r.DiscoveredAt == "" {
				r.DiscoveredAt = now
			}
			rowID := uuid.Must(uuid.NewV7()).String()
			res, err := tx.Exec(tx.Rebind(`
				INSERT INTO resources
					(id, root_id, provider, account_id, account_name, type, native_id,
					 name, region, zone, status, tags, attributes,
					 created_at, discovered_at, discovered_by,
					 verified_at, verified_by, managed_by_provider)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
				        $14, $15, $16, $17, $18, $19)
				ON CONFLICT (provider, account_id, type, native_id)
				    WHERE superseded_by IS NULL
				DO NOTHING`),
				rowID, r.ID, r.Provider, r.AccountID, r.AccountName, r.Type, r.NativeID,
				r.Name, r.Region, r.Zone, r.Status, r.TagsJSON, r.AttributesJSON,
				r.CreatedAt, r.DiscoveredAt, r.DiscoveredBy,
				now, r.DiscoveredBy, r.ManagedByProvider,
			)
			if err != nil {
				return 0, fmt.Errorf("insert resource %s: %w", r.ID, err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				inserted++
			}

		case lookupErr != nil:
			return 0, fmt.Errorf("lookup current version for %s: %w", r.ID, lookupErr)

		case jsonEqual(existing.AttributesJSON, r.AttributesJSON) &&
			jsonEqual(derefStr(existing.TagsJSON), derefStr(r.TagsJSON)):
			// Unchanged. Verify-only update; top-level columns still
			// update in place so renames / status flaps propagate.
			if _, err := tx.Exec(tx.Rebind(`
				UPDATE resources
				   SET verified_at         = $1,
				       verified_by         = $2,
				       name                = $3,
				       region              = $4,
				       zone                = $5,
				       status              = $6,
				       account_name        = $7,
				       managed_by_provider = $8
				 WHERE id = $9`),
				now, r.DiscoveredBy,
				r.Name, r.Region, r.Zone, r.Status, r.AccountName, r.ManagedByProvider,
				existing.VersionRowID,
			); err != nil {
				return 0, fmt.Errorf("verify resource %s: %w", r.ID, err)
			}

		default:
			// Attributes or tags changed → version split. The new row
			// inherits discovered_at / discovered_by from the chain
			// root so "when was this resource first seen" stays stable
			// across splits.
			//
			// Order matters: mark the old row as superseded BEFORE
			// inserting the new row. The partial unique index
			// idx_resources_current_by_natural_key forbids two rows
			// with the same natural key and superseded_by IS NULL
			// simultaneously. We give the old row a placeholder
			// superseded_by value first (the new UUID, computed
			// upfront), then INSERT — the new row takes the
			// current-version slot atomically.
			newRowID := uuid.Must(uuid.NewV7()).String()
			if _, err := tx.Exec(tx.Rebind(
				`UPDATE resources SET superseded_by = $1 WHERE id = $2`),
				newRowID, existing.VersionRowID,
			); err != nil {
				return 0, fmt.Errorf("mark superseded for %s: %w", r.ID, err)
			}
			if _, err := tx.Exec(tx.Rebind(`
				INSERT INTO resources
					(id, root_id, previous_version_id, provider, account_id, account_name, type, native_id,
					 name, region, zone, status, tags, attributes,
					 created_at, discovered_at, discovered_by,
					 verified_at, verified_by, managed_by_provider)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
				        $15, $16, $17, $18, $19, $20)`),
				newRowID, existing.RootID, existing.VersionRowID,
				r.Provider, r.AccountID, r.AccountName, r.Type, r.NativeID,
				r.Name, r.Region, r.Zone, r.Status, r.TagsJSON, r.AttributesJSON,
				r.CreatedAt, existing.DiscoveredAt, existing.DiscoveredBy,
				now, r.DiscoveredBy, r.ManagedByProvider,
			); err != nil {
				return 0, fmt.Errorf("insert new version of %s: %w", r.ID, err)
			}
			inserted++
		}
	}

	return inserted, tx.Commit()
}
