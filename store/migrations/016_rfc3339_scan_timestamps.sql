-- 016_rfc3339_scan_timestamps.sql
--
-- SQLite mirror of pg/016_rfc3339_scan_timestamps.sql. Real, not a no-op:
-- nowExpr writes these three columns on both dialects, so both carry rows in
-- the old "YYYY-MM-DD HH:MM:SS" shape and both must be reshaped. See the PG
-- file for why one format per column is load-bearing.
--
-- strftime returns NULL for anything it cannot parse, and updated_at /
-- started_at are NOT NULL, so an unparseable row would fail the migration
-- rather than corrupt the column. The IS NOT NULL guard keeps such a row as
-- it is instead: a value nothing can parse is already unreadable, and losing
-- the original text would remove the only evidence of what it was.
--
-- Guarded on NOT LIKE '%T%' so a re-run is a no-op: an UPDATE takes no
-- IF NOT EXISTS, and both runners apply files unconditionally.

UPDATE scans
   SET started_at = strftime('%Y-%m-%dT%H:%M:%SZ', started_at)
 WHERE started_at NOT LIKE '%T%'
   AND strftime('%Y-%m-%dT%H:%M:%SZ', started_at) IS NOT NULL;

UPDATE scans
   SET finished_at = strftime('%Y-%m-%dT%H:%M:%SZ', finished_at)
 WHERE finished_at IS NOT NULL
   AND finished_at NOT LIKE '%T%'
   AND strftime('%Y-%m-%dT%H:%M:%SZ', finished_at) IS NOT NULL;

UPDATE scan_checkpoints
   SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', updated_at)
 WHERE updated_at NOT LIKE '%T%'
   AND strftime('%Y-%m-%dT%H:%M:%SZ', updated_at) IS NOT NULL;
