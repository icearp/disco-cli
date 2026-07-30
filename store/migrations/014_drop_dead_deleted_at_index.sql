-- 014_drop_dead_deleted_at_index.sql
--
-- SQLite mirror of pg/014_drop_dead_deleted_at_index.sql. See that file: the
-- index was kept by 013 as the plausible path for an archived-only listing, and
-- the PostgreSQL planner was then observed serving that listing from a different
-- index while this one recorded zero scans against a table that did hold an
-- archived row.
--
-- The query set is shared by both dialects, and here too the only deleted_at
-- predicates lead with root_id.

DROP INDEX IF EXISTS idx_resources_deleted_at;
