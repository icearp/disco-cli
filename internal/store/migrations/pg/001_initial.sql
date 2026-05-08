-- Postgres mirror of migrations/001_initial.sql. Schema and column
-- semantics match the SQLite version one-for-one; only types and a
-- handful of dialect-specific bits differ:
--   * INTEGER PRIMARY KEY AUTOINCREMENT  -> BIGINT GENERATED ALWAYS AS IDENTITY
--   * tags TEXT (JSON string)            -> tags JSONB (so jsonb_each_text + ->> work)
--   * No PRAGMA foreign_keys             -> Postgres enforces FKs by default
-- migration_pg.go drives this file via the same splitStatements runner the
-- SQLite path uses; semicolon-comments are still forbidden.

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS scans (
    id             TEXT PRIMARY KEY,
    started_at     TEXT NOT NULL,
    finished_at    TEXT,
    status         TEXT NOT NULL CHECK (status IN ('running','completed','failed','partial','pending')),
    providers      TEXT NOT NULL,
    scope          TEXT NOT NULL,
    error          TEXT,
    resource_count INTEGER,
    meta           TEXT
);

CREATE TABLE IF NOT EXISTS resources (
    id            TEXT PRIMARY KEY,
    provider      TEXT NOT NULL,
    account_id    TEXT NOT NULL,
    account_name  TEXT,
    type          TEXT NOT NULL,
    native_id     TEXT NOT NULL,
    name          TEXT,
    region        TEXT,
    zone          TEXT,
    status        TEXT,
    tags          JSONB,
    attributes    TEXT NOT NULL,
    created_at    TEXT,
    discovered_at TEXT NOT NULL,
    discovered_by TEXT NOT NULL,
    verified_at   TEXT,
    verified_by   TEXT,
    FOREIGN KEY (discovered_by) REFERENCES scans(id),
    FOREIGN KEY (verified_by)   REFERENCES scans(id)
);

CREATE INDEX IF NOT EXISTS idx_resources_provider  ON resources(provider);
CREATE INDEX IF NOT EXISTS idx_resources_type      ON resources(provider, type);
CREATE INDEX IF NOT EXISTS idx_resources_account   ON resources(provider, account_id);
CREATE INDEX IF NOT EXISTS idx_resources_region    ON resources(provider, account_id, region);
CREATE INDEX IF NOT EXISTS idx_resources_native_id ON resources(native_id);
CREATE INDEX IF NOT EXISTS idx_resources_status    ON resources(status);
CREATE INDEX IF NOT EXISTS idx_resources_scan      ON resources(discovered_by);

CREATE TABLE IF NOT EXISTS relationships (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    from_id       TEXT NOT NULL,
    to_id         TEXT NOT NULL,
    kind          TEXT NOT NULL,
    direction     TEXT NOT NULL DEFAULT 'directed' CHECK (direction IN ('directed','undirected')),
    attributes    TEXT,
    discovered_at TEXT NOT NULL,
    FOREIGN KEY (from_id) REFERENCES resources(id),
    FOREIGN KEY (to_id)   REFERENCES resources(id),
    UNIQUE (from_id, to_id, kind)
);

CREATE INDEX IF NOT EXISTS idx_rel_from ON relationships(from_id, kind);
CREATE INDEX IF NOT EXISTS idx_rel_to   ON relationships(to_id, kind);

CREATE TABLE IF NOT EXISTS hierarchy_closure (
    ancestor_id   TEXT NOT NULL,
    descendant_id TEXT NOT NULL,
    depth         INTEGER NOT NULL,
    PRIMARY KEY (ancestor_id, descendant_id),
    FOREIGN KEY (ancestor_id)   REFERENCES resources(id),
    FOREIGN KEY (descendant_id) REFERENCES resources(id)
);

CREATE INDEX IF NOT EXISTS idx_closure_descendant ON hierarchy_closure(descendant_id, depth);
