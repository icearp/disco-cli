-- 016_rfc3339_scan_timestamps.sql
--
-- Normalize every timestamp nowExpr writes to RFC3339, matching what
-- dialect.go now emits. These columns are TEXT and the schema's own comments
-- in 001_initial.sql have always called them "RFC3339 UTC"; nowExpr was the
-- thing writing "YYYY-MM-DD HH:MM:SS" instead, so this makes the data agree
-- with the documented contract rather than changing it.
--
-- Leaving the old rows alone was not an option. started_at is compared and
-- ordered as TEXT (ORDER BY started_at DESC here, and a keyset cursor plus an
-- evidence-bundle range filter in disco-saas), and a space sorts before "T",
-- so a column holding both shapes orders its own rows into two interleaved
-- blocks. One format per column is what makes those comparisons mean anything.
--
-- The cast is ::timestamp, deliberately, NOT ::timestamptz. The stored text
-- carries no offset, so ::timestamptz would resolve it against the session
-- TimeZone and to_char would render it back in that same zone -- making the
-- rewritten value depend on who ran the migration. ::timestamp keeps the
-- wall-clock digits untouched and this stays a pure textual reshape.
--
-- Guarded on NOT LIKE '%T%' so a re-run is a no-op: an UPDATE takes no
-- IF NOT EXISTS, and both runners apply files unconditionally.

UPDATE scans
   SET started_at = to_char(started_at::timestamp, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
 WHERE started_at NOT LIKE '%T%';

UPDATE scans
   SET finished_at = to_char(finished_at::timestamp, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
 WHERE finished_at IS NOT NULL
   AND finished_at NOT LIKE '%T%';

UPDATE scan_checkpoints
   SET updated_at = to_char(updated_at::timestamp, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
 WHERE updated_at NOT LIKE '%T%';
