-- 008_resource_deleted_at.sql
--
-- Resource archival / deletion tombstone.
--
--   deleted_at  RFC3339 TEXT (matching verified_at / discovered_at). Set on a
--               resource the operator has archived as gone, or that a future
--               scan-coverage reaper detects as absent from a completed scan.
--   deleted_by  Attribution for the tombstone: a caller identifier (user id)
--               for a manual archive, or a scan id for the automated case.
--
-- A tombstone is the CURRENT version row (superseded_by IS NULL) with
-- deleted_at set. UpsertResources clears these columns on the next re-sight
-- (verify-only path) and starts a fresh non-deleted row on a version split,
-- so archival is a soft, reversible state rather than a delete. The
-- current-by-natural-key partial unique index is unchanged: a tombstone is
-- still the single current row for its natural key.

ALTER TABLE resources ADD COLUMN deleted_at TEXT;
ALTER TABLE resources ADD COLUMN deleted_by TEXT;

CREATE INDEX IF NOT EXISTS idx_resources_deleted_at
    ON resources(deleted_at)
    WHERE deleted_at IS NOT NULL;

COMMENT ON COLUMN resources.deleted_at IS 'archival tombstone time (RFC3339 UTC); NULL on a live row. Current row (superseded_by NULL) + deleted_at set = archived; cleared on the next re-sight';
COMMENT ON COLUMN resources.deleted_by IS 'who archived it: caller/user id for a manual archive, or a scan id for the coverage reaper';
