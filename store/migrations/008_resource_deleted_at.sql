-- 008_resource_deleted_at.sql
--
-- SQLite mirror of pg/008_resource_deleted_at.sql. See that file for full
-- semantics. Adds the resource archival / deletion tombstone columns and the
-- partial index over live tombstones. Column parity with the PG set is
-- enforced by scripts/check-migrations.sh.

ALTER TABLE resources ADD COLUMN deleted_at TEXT;
ALTER TABLE resources ADD COLUMN deleted_by TEXT;

CREATE INDEX IF NOT EXISTS idx_resources_deleted_at
    ON resources(deleted_at)
    WHERE deleted_at IS NOT NULL;
