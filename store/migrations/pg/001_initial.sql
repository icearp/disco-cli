-- Single Postgres migration. Greenfield install applies this one file.
--
-- Schema and column semantics match the SQLite version (../001_initial.sql)
-- one-for-one. Only types and a handful of dialect-specific bits differ:
--   * INTEGER PRIMARY KEY AUTOINCREMENT  -> BIGINT GENERATED ALWAYS AS IDENTITY
--   * tags TEXT (JSON string)            -> tags JSONB (jsonb_each_text + ->> work)
--   * attributes TEXT (JSON string)      -> attributes JSONB since pg/007 (->> works)
--   * managed_by_provider INTEGER 0/1    -> managed_by_provider BOOLEAN
--   * No PRAGMA foreign_keys             -> Postgres enforces FKs by default
--
-- workspace_id is a plain nullable mirror of the SQLite column. The OSS
-- schema is single-tenant; the multi-tenant control plane (disco-saas)
-- layers tenant_id, row-level security, and the workspace_id NOT-NULL/GUC
-- default on top via its own migration set, against these same tables.

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
    meta           TEXT,
    workspace_id   UUID
);

CREATE TABLE IF NOT EXISTS resources (
    id                  TEXT PRIMARY KEY,
    provider            TEXT NOT NULL,
    account_id          TEXT NOT NULL,
    account_name        TEXT,
    type                TEXT NOT NULL,
    native_id           TEXT NOT NULL,
    name                TEXT,
    region              TEXT,
    zone                TEXT,
    status              TEXT,
    tags                JSONB,
    attributes          TEXT NOT NULL,
    managed_by_provider BOOLEAN NOT NULL DEFAULT FALSE,
    created_at          TEXT,
    discovered_at       TEXT NOT NULL,
    discovered_by       TEXT NOT NULL,
    workspace_id        UUID,
    FOREIGN KEY (discovered_by) REFERENCES scans(id)
);

CREATE INDEX IF NOT EXISTS idx_resources_provider  ON resources(provider);
CREATE INDEX IF NOT EXISTS idx_resources_type      ON resources(provider, type);
CREATE INDEX IF NOT EXISTS idx_resources_account   ON resources(provider, account_id);
CREATE INDEX IF NOT EXISTS idx_resources_region    ON resources(provider, account_id, region);
CREATE INDEX IF NOT EXISTS idx_resources_native_id ON resources(native_id);
CREATE INDEX IF NOT EXISTS idx_resources_status    ON resources(status);
CREATE INDEX IF NOT EXISTS idx_resources_scan      ON resources(discovered_by);
CREATE INDEX IF NOT EXISTS idx_resources_managed   ON resources(managed_by_provider);

CREATE TABLE IF NOT EXISTS relationships (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    from_id       TEXT NOT NULL,
    to_id         TEXT NOT NULL,
    kind          TEXT NOT NULL,
    direction     TEXT NOT NULL DEFAULT 'directed' CHECK (direction IN ('directed','undirected')),
    attributes    TEXT,
    discovered_at TEXT NOT NULL,
    workspace_id  UUID,
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
    workspace_id  UUID,
    PRIMARY KEY (ancestor_id, descendant_id),
    FOREIGN KEY (ancestor_id)   REFERENCES resources(id),
    FOREIGN KEY (descendant_id) REFERENCES resources(id)
);

CREATE INDEX IF NOT EXISTS idx_closure_descendant ON hierarchy_closure(descendant_id, depth);

CREATE TABLE IF NOT EXISTS scan_checkpoints (
    scan_id      TEXT NOT NULL,
    provider     TEXT NOT NULL,
    service      TEXT NOT NULL,
    scope        TEXT NOT NULL,
    last_token   TEXT,
    updated_at   TEXT NOT NULL,
    workspace_id UUID,
    PRIMARY KEY (scan_id, provider, service, scope)
);

CREATE INDEX IF NOT EXISTS idx_scan_checkpoints_scan ON scan_checkpoints(scan_id);
