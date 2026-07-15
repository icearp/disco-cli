package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ArchiveResource marks the current version of the resource chain identified
// by rootID as an archival tombstone, and reports whether a row was archived.
//
// It appends a new current version row that carries deleted_at (now) and
// deleted_by, copying the prior row's payload and superseding it
// (previous_version_id -> old row, old row superseded_by -> the tombstone), so
// the pre-archive state stays in the version chain and [Store.GetResourceVersions]
// still returns it. verified_at / verified_by are carried forward unchanged:
// they record the last scan that actually saw the resource, which archiving
// does not alter.
//
// deletedBy attributes the tombstone — a caller identifier (user id) for a
// manual archive, or a scan id for an automated coverage reaper.
//
// The archive is soft and reversible. A later scan that re-sees the resource
// resurrects it automatically (UpsertResources clears deleted_at on its
// verify-only path and starts a fresh non-deleted row on a version split);
// [Store.RestoreResource] lifts the tombstone directly.
//
// It reports false with a nil error when rootID has no live current row —
// the chain does not exist, or its current row is already a tombstone. The
// operation is therefore idempotent: a second call archives nothing.
func (s *Store) ArchiveResource(rootID, deletedBy string) (bool, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := s.db.Beginx()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	// Load the live current row of the chain, skipping an already-archived
	// tombstone (deleted_at IS NOT NULL).
	const lookupSQL = `
		SELECT id AS version_row_id, root_id, provider, account_id, account_name,
		       type, native_id, name, region, zone, status, tags, attributes,
		       created_at, discovered_at, discovered_by, verified_at, verified_by,
		       managed_by_provider
		  FROM resources
		 WHERE root_id = $1 AND superseded_by IS NULL AND deleted_at IS NULL`

	var cur struct {
		VersionRowID      string  `db:"version_row_id"`
		RootID            string  `db:"root_id"`
		Provider          string  `db:"provider"`
		AccountID         string  `db:"account_id"`
		AccountName       *string `db:"account_name"`
		Type              string  `db:"type"`
		NativeID          string  `db:"native_id"`
		Name              *string `db:"name"`
		Region            *string `db:"region"`
		Zone              *string `db:"zone"`
		Status            *string `db:"status"`
		TagsJSON          *string `db:"tags"`
		AttributesJSON    string  `db:"attributes"`
		CreatedAt         *string `db:"created_at"`
		DiscoveredAt      string  `db:"discovered_at"`
		DiscoveredBy      string  `db:"discovered_by"`
		VerifiedAt        *string `db:"verified_at"`
		VerifiedBy        *string `db:"verified_by"`
		ManagedByProvider bool    `db:"managed_by_provider"`
	}
	switch lookupErr := tx.Get(&cur, tx.Rebind(lookupSQL), rootID); {
	case errors.Is(lookupErr, sql.ErrNoRows):
		return false, nil
	case lookupErr != nil:
		return false, fmt.Errorf("lookup current version for %s: %w", rootID, lookupErr)
	}

	// Version split into a tombstone. Mark the old row superseded BEFORE the
	// insert so the current-by-natural-key partial unique index never sees two
	// live rows for the natural key (mirrors UpsertResources' split ordering).
	newRowID := uuid.Must(uuid.NewV7()).String()
	if _, err := tx.Exec(tx.Rebind(
		`UPDATE resources SET superseded_by = $1 WHERE id = $2`),
		newRowID, cur.VersionRowID,
	); err != nil {
		return false, fmt.Errorf("mark superseded for %s: %w", rootID, err)
	}
	if _, err := tx.Exec(tx.Rebind(`
		INSERT INTO resources
			(id, root_id, previous_version_id, provider, account_id, account_name, type, native_id,
			 name, region, zone, status, tags, attributes,
			 created_at, discovered_at, discovered_by,
			 verified_at, verified_by, managed_by_provider, deleted_at, deleted_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
		        $15, $16, $17, $18, $19, $20, $21, $22)`),
		newRowID, cur.RootID, cur.VersionRowID,
		cur.Provider, cur.AccountID, cur.AccountName, cur.Type, cur.NativeID,
		cur.Name, cur.Region, cur.Zone, cur.Status, cur.TagsJSON, cur.AttributesJSON,
		cur.CreatedAt, cur.DiscoveredAt, cur.DiscoveredBy,
		cur.VerifiedAt, cur.VerifiedBy, cur.ManagedByProvider, now, deletedBy,
	); err != nil {
		return false, fmt.Errorf("insert tombstone for %s: %w", rootID, err)
	}
	return true, tx.Commit()
}

// RestoreResource lifts the archival tombstone on the current version of the
// resource chain identified by rootID, clearing deleted_at / deleted_by in
// place, and reports whether a tombstone was lifted.
//
// The clear is done in place on the current row (matching how a scan re-sight
// resurrects via UpsertResources' verify-only path) rather than as another
// version split, so restoring leaves no extra chain entry.
//
// It reports false with a nil error when rootID has no archived current row —
// the chain does not exist, or its current row is already live. The operation
// is therefore idempotent.
func (s *Store) RestoreResource(rootID string) (bool, error) {
	res, err := s.db.Exec(s.db.Rebind(`
		UPDATE resources
		   SET deleted_at = NULL, deleted_by = NULL
		 WHERE root_id = $1 AND superseded_by IS NULL AND deleted_at IS NOT NULL`),
		rootID)
	if err != nil {
		return false, fmt.Errorf("restore resource %s: %w", rootID, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
