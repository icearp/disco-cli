-- 006_resource_versioning_paid.sql (paid-only — skipped in OSS builds)
--
-- Postgres mirror of the SQLite paid versioning migration. See
-- ../006_resource_versioning_paid.sql for full semantics.

ALTER TABLE resources ADD COLUMN root_id             TEXT;
ALTER TABLE resources ADD COLUMN previous_version_id TEXT;
ALTER TABLE resources ADD COLUMN superseded_by       TEXT;
ALTER TABLE resources ADD COLUMN verified_at         TEXT;
ALTER TABLE resources ADD COLUMN verified_by         TEXT;

UPDATE resources SET root_id = id WHERE root_id IS NULL;

ALTER TABLE resources ALTER COLUMN root_id SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_resources_current_by_natural_key
    ON resources(provider, account_id, type, native_id)
    WHERE superseded_by IS NULL;

CREATE INDEX IF NOT EXISTS idx_resources_root_id          ON resources(root_id);
CREATE INDEX IF NOT EXISTS idx_resources_previous_version ON resources(previous_version_id);
CREATE INDEX IF NOT EXISTS idx_resources_verified_by      ON resources(verified_by);

-- Edges reference root_id (the deterministic hash), not the per-version
-- row PK. Drop the FKs to resources(id) — id is now the per-version UUID
-- and root_id is non-unique by design. Rely on application-level
-- invariant for edge target validity (matches OSS tolerance for
-- dangling refs surfaced by Floci-incompat scans).
ALTER TABLE relationships     DROP CONSTRAINT IF EXISTS relationships_from_id_fkey;
ALTER TABLE relationships     DROP CONSTRAINT IF EXISTS relationships_to_id_fkey;
ALTER TABLE hierarchy_closure DROP CONSTRAINT IF EXISTS hierarchy_closure_ancestor_id_fkey;
ALTER TABLE hierarchy_closure DROP CONSTRAINT IF EXISTS hierarchy_closure_descendant_id_fkey;
