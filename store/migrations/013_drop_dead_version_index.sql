-- 013_drop_dead_version_index.sql
--
-- SQLite mirror of pg/013_drop_dead_version_index.sql. See that file for the
-- full reasoning: previous_version_id is written and returned but never used as
-- a predicate, because the version chain is read by root_id.
--
-- The measurement quoted there is PostgreSQL-side, but the reason the index is
-- dead is the query set, which is shared by both dialects.

DROP INDEX IF EXISTS idx_resources_previous_version;
