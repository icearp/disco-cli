-- managed_by_provider flags resources owned by the cloud provider rather than
-- the user (Azure built-in policy/role definitions, AWS-owned managed prefix
-- lists, AWS IAM service-linked roles, etc.). Stored as INTEGER 0/1 since
-- SQLite has no native bool. Default 0 means existing rows backfill as
-- not-managed and rescan repopulates the correct value via UpsertResources.

ALTER TABLE resources ADD COLUMN managed_by_provider INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_resources_managed ON resources(managed_by_provider);
