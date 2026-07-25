package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/icearp/disco-cli/internal/managed"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/volatile"
)

// UpsertResources bulk-upserts resources under the version-chain
// model. Caller-facing signature unchanged. Per-resource logic:
//
//   - First discovery (no row with this natural key has superseded_by
//     IS NULL): INSERT a new row with id = UUIDv7, root_id =
//     Resource.ID (deterministic hash), verified_at = now.
//   - Unchanged rescan (existing current row matches attributes +
//     tags): UPDATE verified_at / verified_by on the existing row;
//     top-level columns (name/region/zone/status/managed) update in
//     place. No new row.
//   - Changed rescan (attributes or tags differ): INSERT a new row
//     with a fresh UUIDv7 PK, inheriting discovered_at / discovered_by
//     from the chain root, linking previous_version_id to the
//     predecessor, and setting verified_at / verified_by to the
//     current scan. Then UPDATE the previous row's superseded_by to
//     point at the new row. The attributes/tags on the previous row
//     stay frozen.
//
// The returned `inserted` counts version splits + first discoveries
// (every INSERT into resources). Unchanged-rescan updates don't count.
//
// Idempotency: calling UpsertResources twice with the same payload
// produces one INSERT on the first call and one verified-only UPDATE
// on the second.
type nativeIDSighting struct {
	scanID string
	typ    string
}

// noteNativeIDType records the type first seen for a resource identity within a
// scan run and warns if a later upsert in the SAME run gives that identity a
// different type. Identity is (provider, account, native_id) — type is excluded
// (see ResourceID), so two distinct types sharing one native_id would merge into
// a single version chain that ping-pongs every scan. The unique index makes a
// second CURRENT row impossible but cannot catch this merge; this surfaces it
// loudly. Keyed on r.ID (= hash(provider|account|native_id)); a legitimate type
// rename never presents both types in one run, so there are no false positives.
func (s *Store) noteNativeIDType(r *Resource) {
	if s.nativeIDSeen == nil {
		return
	}
	if v, ok := s.nativeIDSeen.Load(r.ID); ok {
		if prev := v.(nativeIDSighting); prev.scanID == r.DiscoveredBy && prev.typ != r.Type {
			s.ReportWarning(ScanWarning{
				Provider: r.Provider,
				Service:  r.Type,
				Scope:    r.AccountID,
				Message: fmt.Sprintf("native_id %q maps to both types %q and %q; identity excludes type, so one will supersede the other — give them distinct native_ids",
					r.NativeID, prev.typ, r.Type),
			})
		}
	}
	s.nativeIDSeen.Store(r.ID, nativeIDSighting{scanID: r.DiscoveredBy, typ: r.Type})
}

func (s *Store) UpsertResources(resources []*Resource) (inserted int, err error) {
	now := time.Now().UTC().Format(time.RFC3339)

	for _, r := range resources {
		if r.ID == "" {
			r.ID = ResourceID(r.Provider, r.AccountID, r.NativeID)
		}
		r.AttributesJSON = redact.Apply(r.Type, r.AttributesJSON)
		// Drop volatile fields (e.g. CloudWatch Logs UploadSequenceToken) that AWS
		// rotates every read — left in, they'd version-split an unchanged resource
		// every scan. Runs before the jsonEqual comparison below.
		r.AttributesJSON = volatile.Apply(r.Type, r.AttributesJSON)
		// Unconditionally-managed types stamp ManagedByProvider by type (mirrors
		// redact/volatile). Conditional/per-row managed status stays scanner-set.
		if managed.Is(r.Type) {
			r.ManagedByProvider = true
		}
		s.noteNativeIDType(r)
	}

	// Only the transaction retries. The preprocessing above must not: it is
	// idempotent except for noteNativeIDType, which reports a ScanWarning on a
	// native_id collision and would repeat that warning once per attempt.
	//
	// newC/changedC accumulate per attempt and are published to the shared
	// counters only after a commit succeeds — an attempt that fails midway
	// through the row loop has already counted some rows, so publishing as we
	// go would inflate the scan's "new"/"changed" columns on every retry.
	var newC, changedC int
	err = s.withWriteRetry("upsert resources", func() error {
		var txErr error
		inserted, newC, changedC, txErr = s.upsertResourcesTx(resources, now)
		return txErr
	})
	if err != nil {
		return 0, err
	}
	if s.upsertNew != nil {
		s.upsertNew.Add(int64(newC))
	}
	if s.upsertChanged != nil {
		s.upsertChanged.Add(int64(changedC))
	}
	return inserted, nil
}

// upsertResourcesTx is one transactional attempt of UpsertResources. It counts
// first-discoveries into newC and version splits into changedC rather than
// touching the Store's shared atomics, so a retried attempt starts from zero
// instead of compounding the previous one's partial work.
func (s *Store) upsertResourcesTx(resources []*Resource, now string) (inserted, newC, changedC int, err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return 0, 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, r := range resources {
		// Lookup current version row by natural key. The partial
		// unique index idx_resources_current_by_natural_key makes
		// this O(1).
		const lookupSQL = `
			SELECT id AS version_row_id, root_id, type, attributes, tags,
			       discovered_at, discovered_by
			  FROM resources
			 WHERE provider=$1 AND account_id=$2 AND native_id=$3
			   AND superseded_by IS NULL`

		var existing struct {
			VersionRowID   string  `db:"version_row_id"`
			RootID         string  `db:"root_id"`
			Type           string  `db:"type"`
			AttributesJSON string  `db:"attributes"`
			TagsJSON       *string `db:"tags"`
			DiscoveredAt   string  `db:"discovered_at"`
			DiscoveredBy   string  `db:"discovered_by"`
		}
		lookupErr := tx.Get(&existing, tx.Rebind(lookupSQL),
			r.Provider, r.AccountID, r.NativeID)

		switch {
		case errors.Is(lookupErr, sql.ErrNoRows):
			// First discovery. Fresh root row.
			//
			// Scanners upsert concurrently (errgroup goroutines, each on its own
			// transaction), so a sibling can insert this natural key between our
			// lookup and this insert. ON CONFLICT DO NOTHING on the
			// current-by-natural-key partial index turns that race into a no-op
			// instead of a 23505 that would abort the whole batch tx — the sibling
			// already recorded the row, so we skip (same natural key, same
			// point-in-time scan — no info lost).
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
				ON CONFLICT (provider, account_id, native_id)
				    WHERE superseded_by IS NULL
				DO NOTHING`),
				rowID, r.ID, r.Provider, r.AccountID, r.AccountName, r.Type, r.NativeID,
				r.Name, r.Region, r.Zone, r.Status, r.TagsJSON, r.AttributesJSON,
				r.CreatedAt, r.DiscoveredAt, r.DiscoveredBy,
				now, r.DiscoveredBy, r.ManagedByProvider,
			)
			if err != nil {
				return 0, 0, 0, fmt.Errorf("insert resource %s: %w", r.ID, err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				inserted++
				newC++
			}

		case lookupErr != nil:
			return 0, 0, 0, fmt.Errorf("lookup current version for %s: %w", r.ID, lookupErr)

		case existing.Type == r.Type &&
			jsonEqual(existing.AttributesJSON, r.AttributesJSON) &&
			jsonEqual(derefStr(existing.TagsJSON), derefStr(r.TagsJSON)):
			// Unchanged. Verify-only update; top-level columns still
			// update in place so renames / status flaps propagate.
			// A type change is NOT unchanged — it falls to the split
			// path so the new type takes the current-version slot.
			// deleted_at / deleted_by are cleared unconditionally: if this
			// row was an archival tombstone (see ArchiveResource), re-seeing
			// the resource in a scan resurrects it — the resource still
			// exists, so the tombstone must lift. A live row's columns are
			// already NULL, so the clear is a no-op there.
			if _, err := tx.Exec(tx.Rebind(`
				UPDATE resources
				   SET verified_at         = $1,
				       verified_by         = $2,
				       name                = $3,
				       region              = $4,
				       zone                = $5,
				       status              = $6,
				       account_name        = $7,
				       managed_by_provider = $8,
				       deleted_at          = NULL,
				       deleted_by          = NULL
				 WHERE id = $9`),
				now, r.DiscoveredBy,
				r.Name, r.Region, r.Zone, r.Status, r.AccountName, r.ManagedByProvider,
				existing.VersionRowID,
			); err != nil {
				return 0, 0, 0, fmt.Errorf("verify resource %s: %w", r.ID, err)
			}

		default:
			// Attributes, tags, or type changed → version split. The new row inherits
			// discovered_at / discovered_by from the chain root so "when was this
			// resource first seen" stays stable across splits.
			//
			// Order matters: mark the old row as superseded BEFORE inserting the
			// new row. The partial unique index idx_resources_current_by_natural_key
			// forbids two rows with the same natural key and superseded_by IS NULL
			// simultaneously. Set the old row's superseded_by to the new UUID
			// (computed upfront) first, then INSERT — the new row takes the
			// current-version slot atomically.
			newRowID := uuid.Must(uuid.NewV7()).String()
			if _, err := tx.Exec(tx.Rebind(
				`UPDATE resources SET superseded_by = $1 WHERE id = $2`),
				newRowID, existing.VersionRowID,
			); err != nil {
				return 0, 0, 0, fmt.Errorf("mark superseded for %s: %w", r.ID, err)
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
				return 0, 0, 0, fmt.Errorf("insert new version of %s: %w", r.ID, err)
			}
			inserted++
			changedC++
		}
	}

	return inserted, newC, changedC, tx.Commit()
}

// InsertResourcesIfAbsent inserts each resource only when no current-version
// row already holds its natural key, otherwise leaves the existing row
// untouched. Unlike UpsertResources it never runs the verify or version-split
// paths, so a populated row is never reduced back to a placeholder's
// attributes.
//
// This is the reference-discovery primitive: a resolver that sees an edge into
// an account/subscription/project outside the current scan scope inserts an
// empty-attribute row at that resource's real self-node natural key, giving
// the edge a stable FK target (the deterministic root_id is identical whether
// the row is the empty placeholder or later fully populated). When that target
// is itself scanned — this run or a future one — its own scanner calls
// UpsertResources, finds the placeholder as the current version, and
// version-splits it to the populated form. The placeholder is preserved in
// history; the edge keeps resolving across the split.
//
// Returns the count of rows actually inserted (placeholders whose natural key
// was already occupied don't count).
func (s *Store) InsertResourcesIfAbsent(resources []*Resource) (inserted int, err error) {
	now := time.Now().UTC().Format(time.RFC3339)

	for _, r := range resources {
		if r.ID == "" {
			r.ID = ResourceID(r.Provider, r.AccountID, r.NativeID)
		}
		r.AttributesJSON = redact.Apply(r.Type, r.AttributesJSON)
		r.AttributesJSON = volatile.Apply(r.Type, r.AttributesJSON)
		if managed.Is(r.Type) {
			r.ManagedByProvider = true
		}
	}

	err = s.withWriteRetry("insert resources if absent", func() error {
		var txErr error
		inserted, txErr = s.insertResourcesIfAbsentTx(resources, now)
		return txErr
	})
	if err != nil {
		return 0, err
	}
	// Publish after the commit, not inside the tx: a rolled-back attempt would
	// otherwise leave its rows counted and the retry would count them again.
	if s.upsertNew != nil {
		s.upsertNew.Add(int64(inserted))
	}
	return inserted, nil
}

// insertResourcesIfAbsentTx is one transactional attempt of
// InsertResourcesIfAbsent. Returns its own count so a retried attempt restarts
// from zero rather than compounding the previous attempt's partial work.
func (s *Store) insertResourcesIfAbsentTx(resources []*Resource, now string) (inserted int, err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, r := range resources {
		if r.DiscoveredAt == "" {
			r.DiscoveredAt = now
		}
		rowID := uuid.Must(uuid.NewV7()).String()
		// ON CONFLICT DO NOTHING on the current-by-natural-key partial index: if
		// the row already exists (placeholder racing its own scanner, or a prior
		// run already populated it), this is a no-op — a populated row is never
		// clobbered down to the placeholder's empty attributes.
		res, err := tx.Exec(tx.Rebind(`
			INSERT INTO resources
				(id, root_id, provider, account_id, account_name, type, native_id,
				 name, region, zone, status, tags, attributes,
				 created_at, discovered_at, discovered_by,
				 verified_at, verified_by, managed_by_provider, reference_only)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
			        $14, $15, $16, $17, $18, $19, TRUE)
			ON CONFLICT (provider, account_id, native_id)
			    WHERE superseded_by IS NULL
			DO NOTHING`),
			rowID, r.ID, r.Provider, r.AccountID, r.AccountName, r.Type, r.NativeID,
			r.Name, r.Region, r.Zone, r.Status, r.TagsJSON, r.AttributesJSON,
			r.CreatedAt, r.DiscoveredAt, r.DiscoveredBy,
			now, r.DiscoveredBy, r.ManagedByProvider,
		)
		if err != nil {
			return 0, fmt.Errorf("insert-if-absent resource %s: %w", r.ID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
		}
	}

	return inserted, tx.Commit()
}
