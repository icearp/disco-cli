-- 006_resource_versioning_paid.sql (paid-only — skipped in OSS builds)
--
-- Adds resource versioning to the paid build. Schema becomes:
--   id                   per-version UUIDv7 (TEXT), the row PK.
--   root_id              deterministic ResourceID hash, shared across the
--                        chain of versions for a single resource. Reads
--                        addressed by the natural key resolve through this.
--   previous_version_id  link to the immediately prior version row in the
--                        chain (NULL for the root).
--   superseded_by        link to the row that replaced this version
--                        (NULL on the current row of every chain).
--   verified_at,
--   verified_by          set on first insert and on every unchanged-
--                        attributes rescan. Untouched when attributes or
--                        tags change — that case inserts a new row.
--
-- Migration policy: DB is recreated when switching builds (no backfill).
-- The defensive UPDATE below covers fresh-greenfield paid installs where
-- the migration runs against an empty resources table; it's a no-op then.

ALTER TABLE resources ADD COLUMN root_id             TEXT;
ALTER TABLE resources ADD COLUMN previous_version_id TEXT;
ALTER TABLE resources ADD COLUMN superseded_by       TEXT;
ALTER TABLE resources ADD COLUMN verified_at         TEXT;
ALTER TABLE resources ADD COLUMN verified_by         TEXT;

UPDATE resources SET root_id = id WHERE root_id IS NULL;

-- Hot ingest path index: "find current row for this natural key".
CREATE UNIQUE INDEX IF NOT EXISTS idx_resources_current_by_natural_key
    ON resources(provider, account_id, type, native_id)
    WHERE superseded_by IS NULL;

CREATE INDEX IF NOT EXISTS idx_resources_root_id          ON resources(root_id);
CREATE INDEX IF NOT EXISTS idx_resources_previous_version ON resources(previous_version_id);
CREATE INDEX IF NOT EXISTS idx_resources_verified_by      ON resources(verified_by);

-- Edges reference root_id (the deterministic hash), not the per-version
-- row PK. The 001_initial.sql FKs to resources(id) become meaningless
-- under paid versioning. SQLite can't ALTER TABLE DROP FOREIGN KEY, so
-- we recreate `relationships` and `hierarchy_closure` without the FKs.
-- Per the plan's "no backfill" policy these tables are empty at
-- migration time.
DROP TABLE IF EXISTS relationships;
CREATE TABLE relationships (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    from_id       TEXT NOT NULL,
    to_id         TEXT NOT NULL,
    kind          TEXT NOT NULL,
    direction     TEXT NOT NULL DEFAULT 'directed' CHECK (direction IN ('directed','undirected')),
    attributes    TEXT,
    discovered_at TEXT NOT NULL,
    workspace_id  TEXT,
    UNIQUE (from_id, to_id, kind)
);
CREATE INDEX IF NOT EXISTS idx_rel_from ON relationships(from_id, kind);
CREATE INDEX IF NOT EXISTS idx_rel_to   ON relationships(to_id, kind);

DROP TABLE IF EXISTS hierarchy_closure;
CREATE TABLE hierarchy_closure (
    ancestor_id   TEXT NOT NULL,
    descendant_id TEXT NOT NULL,
    depth         INTEGER NOT NULL,
    workspace_id  TEXT,
    PRIMARY KEY (ancestor_id, descendant_id)
);
CREATE INDEX IF NOT EXISTS idx_closure_descendant ON hierarchy_closure(descendant_id, depth);
