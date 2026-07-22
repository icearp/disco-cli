-- 009_index_write_pressure.sql
--
-- SQLite mirror of pg/009_index_write_pressure.sql. See that file for the full
-- reasoning. The four index drops apply here too: idx_resources_verified_by
-- serves only DiffScans (an approximate result a table scan answers), and the
-- other three are strict prefixes of a wider index or of the scan_checkpoints
-- composite primary key.
--
-- The storage-parameter half of the PG migration has no SQLite equivalent --
-- SQLite has no fillfactor and no autovacuum thresholds -- so it is
-- deliberately absent rather than approximated. Version parity with the PG set
-- is enforced by scripts/check-migrations.sh.

DROP INDEX IF EXISTS idx_resources_verified_by;
DROP INDEX IF EXISTS idx_resources_provider;
DROP INDEX IF EXISTS idx_resources_account;
DROP INDEX IF EXISTS idx_scan_checkpoints_scan;
