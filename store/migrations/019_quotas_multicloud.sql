-- SQLite mirror of pg/019_quotas_multicloud.sql. Adds dimension_key,
-- period_unit, period_value, resource_type, availability_zone and
-- sub_account_type, drops the AWS-only global_quota and applied_level, and
-- widens the natural-key index to carry dimension_key. The PG file holds the
-- FOCUS rationale and the measurements behind each column.
--
-- SQLite has no IF NOT EXISTS on ADD COLUMN or DROP COLUMN; migrations apply
-- once, tracked by schema_migrations, so the plain form is what the other ALTER
-- migrations use. DROP COLUMN is legal here because neither dropped column
-- appears in an index or key.
--
-- No CHECK on period_unit. Adding one to an existing SQLite table needs a full
-- table rebuild, whose copy is DML that TestMigrationsCarryNoDML rejects, and
-- the constraint is enforced where the value is produced -- quotaPeriodUnit
-- returns "" for anything outside the set. check-migrations.sh compares column
-- PRESENCE per dialect, not constraints, so the dialects stay in parity.
--
-- adjustable stays NOT NULL in both dialects for the same rebuild reason; see
-- the PG file. It lands with the GCP scanner.

ALTER TABLE quotas ADD COLUMN dimension_key     TEXT NOT NULL DEFAULT '';
ALTER TABLE quotas ADD COLUMN period_unit       TEXT;
ALTER TABLE quotas ADD COLUMN period_value      INTEGER;
ALTER TABLE quotas ADD COLUMN resource_type     TEXT;
ALTER TABLE quotas ADD COLUMN availability_zone TEXT;
ALTER TABLE quotas ADD COLUMN sub_account_type  TEXT;

DROP INDEX IF EXISTS idx_quotas_current_by_natural_key;

-- The upsert's current-row lookup, and the uniqueness invariant itself.
-- dimension_key joins the key so one quota code can hold a value per dimension
-- set. It only loosens the constraint: every existing row carries the same
-- empty value, so no pair that was distinct becomes equal.
CREATE UNIQUE INDEX IF NOT EXISTS idx_quotas_current_by_natural_key
    ON quotas(provider, account_id, service_code, quota_code, region, dimension_key)
    WHERE superseded_by IS NULL;

ALTER TABLE quotas DROP COLUMN global_quota;
ALTER TABLE quotas DROP COLUMN applied_level;
