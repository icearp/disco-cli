-- 015: SQLite mirror of the PG migration of the same number -- carry the
-- current-row predicate on the indexes that only serve current-state queries,
-- and drop the relationship index the wider unique key absorbs.
--
-- This is a REAL mirror, not a version-parity placeholder. SQLite supports
-- partial indexes with the same semantics, and the reason for the change is a
-- property of the SCHEMA rather than of Postgres: `resources` keeps every
-- version of every resource forever, so an index without the predicate grows
-- with total history on either backend while every reader filters to the
-- current row. A CLI user scanning the same estate daily accumulates
-- superseded rows exactly like the hosted one does.
--
-- The invariant, the two narrower predicates (status, managed_by_provider) and
-- the deliberate exemption of idx_resources_scan are all explained in
-- store/migrations/pg/015_current_row_partial_indexes.sql -- read that file
-- rather than duplicating the reasoning here, so the two cannot drift.
--
-- Dialect note: `NOT managed_by_provider` is valid on both. SQLite has no
-- native boolean and stores 0/1, and NOT of a stored 0 is 1, so the predicate
-- selects the same rows the PG one does.

DROP INDEX IF EXISTS idx_resources_native_id;
CREATE INDEX IF NOT EXISTS idx_resources_native_id
    ON resources (native_id)
    WHERE superseded_by IS NULL;

DROP INDEX IF EXISTS idx_resources_region;
CREATE INDEX IF NOT EXISTS idx_resources_region
    ON resources (provider, account_id, region)
    WHERE superseded_by IS NULL;

DROP INDEX IF EXISTS idx_resources_type;
CREATE INDEX IF NOT EXISTS idx_resources_type
    ON resources (provider, type)
    WHERE superseded_by IS NULL;

DROP INDEX IF EXISTS idx_resources_status;
CREATE INDEX IF NOT EXISTS idx_resources_status
    ON resources (status)
    WHERE superseded_by IS NULL AND status IS NOT NULL;

DROP INDEX IF EXISTS idx_resources_managed;
CREATE INDEX IF NOT EXISTS idx_resources_managed
    ON resources (managed_by_provider)
    WHERE superseded_by IS NULL AND NOT managed_by_provider;

DROP INDEX IF EXISTS idx_rel_from;
