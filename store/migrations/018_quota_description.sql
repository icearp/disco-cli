-- SQLite mirror of pg/018_quota_description.sql. Adds the provider's own prose
-- description of a quota, which AWS populates on every row and Azure does not
-- report at all. The PG file carries the full rationale.
--
-- SQLite has no IF NOT EXISTS on ADD COLUMN; migrations apply once, tracked by
-- schema_migrations, so the plain form is what the other ALTER migrations use.

ALTER TABLE quotas ADD COLUMN description TEXT;
