-- Postgres uses BOOLEAN for managed_by_provider rather than INTEGER 0/1.
-- The Go column tag is `bool` so sqlx unmarshals identically across both
-- backends.

ALTER TABLE resources ADD COLUMN managed_by_provider BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_resources_managed ON resources(managed_by_provider);
