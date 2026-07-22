-- 009_index_write_pressure.sql
--
-- Removes four indexes that cost a write on every row change without giving
-- the planner an access path it did not already have, and gives `resources`
-- in-page room so repeated verification updates can stay heap-only.
--
-- Why an index on a rarely-read column is not free: Postgres never updates a
-- row in place. Every UPDATE writes a new tuple version, and because indexes
-- address physical tuple locations, the default path inserts an entry into
-- EVERY index on the table, including ones whose columns did not change. The
-- heap-only-tuple optimization skips all of those index writes, but only when
-- no indexed column changed AND the new version fits on the same page. So a
-- single index on a column the hot path rewrites taxes every other index too.
--
-- idx_resources_verified_by is exactly that case. UpsertResources rewrites
-- verified_at / verified_by on every unchanged resource on every scan (the
-- verify-only path), so with this index a no-op rescan of an unchanged
-- resource costs a new heap tuple plus an insertion into all of the table's
-- indexes. Its only reader is DiffScans (store/diff.go), reached from the
-- `disco diff` CLI, whose Stale set that comment already documents as
-- approximate -- a scan of the current rows serves it correctly.
--
-- The other three are strict prefixes of a wider index under an identical
-- predicate, so the planner can always use the wider one instead:
--
--   idx_resources_provider (provider)                is a prefix of
--     idx_resources_type    (provider, type)
--     idx_resources_account (provider, account_id)
--     idx_resources_region  (provider, account_id, region)
--   idx_resources_account  (provider, account_id)    is a prefix of
--     idx_resources_region  (provider, account_id, region)
--   idx_scan_checkpoints_scan (scan_id)              is a prefix of
--     the scan_checkpoints primary key (scan_id, provider, service, scope)
--
-- Dropping the index alone does NOT restore heap-only updates: the default
-- fillfactor of 100 leaves no free space on a page, so the new tuple version
-- lands elsewhere and every index is written anyway. Both halves are one fix.
-- fillfactor governs how future page fills are packed, so existing pages keep
-- their current density until they are rewritten -- the effect appears as the
-- table grows, not the moment this migration runs. Verify with the ratio
-- n_tup_hot_upd / n_tup_upd in pg_stat_user_tables: near 0 means every update
-- is still paying the full index tax.
--
-- The autovacuum settings pair with it. resources keeps every superseded
-- version, so the default vacuum_scale_factor of 0.2 waits for a fifth of a
-- large table to go dead, and heap-only updates need that space reclaimed to
-- keep working.

DROP INDEX IF EXISTS idx_resources_verified_by;
DROP INDEX IF EXISTS idx_resources_provider;
DROP INDEX IF EXISTS idx_resources_account;
DROP INDEX IF EXISTS idx_scan_checkpoints_scan;

ALTER TABLE resources SET (
    fillfactor                     = 85,
    autovacuum_vacuum_scale_factor = 0.02,
    autovacuum_vacuum_threshold    = 1000
);
