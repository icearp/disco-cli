-- 011_resources_reference_only.sql
--
-- SQLite mirror of pg/011_resources_reference_only.sql. Real, not a no-op:
-- Resource.ReferenceOnly is scanned in both dialects (the store test harness
-- runs SQLite), so the column must exist here too or a SELECT of the
-- projection fails. SQLite has no BOOLEAN type; use INTEGER 0/1, matching the
-- managed_by_provider convention from 001_initial.sql.

ALTER TABLE resources ADD COLUMN reference_only INTEGER NOT NULL DEFAULT 0;
