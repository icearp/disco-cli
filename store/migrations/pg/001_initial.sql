-- Single Postgres migration. Greenfield install applies this one file.
--
-- Schema and column semantics match the SQLite version (../001_initial.sql)
-- one-for-one; only types and a handful of dialect-specific bits differ:
--   * INTEGER PRIMARY KEY AUTOINCREMENT  -> BIGINT GENERATED ALWAYS AS IDENTITY
--   * tags TEXT (JSON string)            -> tags JSONB (jsonb_each_text + ->> work)
--   * managed_by_provider INTEGER 0/1    -> managed_by_provider BOOLEAN
--   * No PRAGMA foreign_keys             -> Postgres enforces FKs by default
--
-- Plus PG-only RLS plumbing: tenant_id columns + per-table policies keyed on
-- the `app.tenant_id` GUC. The GUC is pinned at connection time by
-- OpenPostgres (postgres_paid.go) via AfterConnect, so RLS sees the value
-- on every query without per-statement plumbing. NEW rows pick up the GUC by
-- DEFAULT; INSERTs from app code never need to spell tenant_id explicitly.
--
-- Paid tables (check_runs, findings) live in 002_findings_paid.sql plus
-- their own RLS layer.

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
    tenant_id      UUID NOT NULL DEFAULT current_setting('app.tenant_id')::uuid
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
    verified_at         TEXT,
    verified_by         TEXT,
    tenant_id           UUID NOT NULL DEFAULT current_setting('app.tenant_id')::uuid,
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
CREATE INDEX IF NOT EXISTS idx_resources_managed   ON resources(managed_by_provider);
CREATE INDEX IF NOT EXISTS idx_resources_tenant    ON resources(tenant_id);

CREATE TABLE IF NOT EXISTS relationships (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    from_id       TEXT NOT NULL,
    to_id         TEXT NOT NULL,
    kind          TEXT NOT NULL,
    direction     TEXT NOT NULL DEFAULT 'directed' CHECK (direction IN ('directed','undirected')),
    attributes    TEXT,
    discovered_at TEXT NOT NULL,
    tenant_id     UUID NOT NULL DEFAULT current_setting('app.tenant_id')::uuid,
    FOREIGN KEY (from_id) REFERENCES resources(id),
    FOREIGN KEY (to_id)   REFERENCES resources(id),
    UNIQUE (from_id, to_id, kind)
);

CREATE INDEX IF NOT EXISTS idx_rel_from         ON relationships(from_id, kind);
CREATE INDEX IF NOT EXISTS idx_rel_to           ON relationships(to_id, kind);
CREATE INDEX IF NOT EXISTS idx_relationships_tenant ON relationships(tenant_id);

CREATE TABLE IF NOT EXISTS hierarchy_closure (
    ancestor_id   TEXT NOT NULL,
    descendant_id TEXT NOT NULL,
    depth         INTEGER NOT NULL,
    tenant_id     UUID NOT NULL DEFAULT current_setting('app.tenant_id')::uuid,
    PRIMARY KEY (ancestor_id, descendant_id),
    FOREIGN KEY (ancestor_id)   REFERENCES resources(id),
    FOREIGN KEY (descendant_id) REFERENCES resources(id)
);

CREATE INDEX IF NOT EXISTS idx_closure_descendant       ON hierarchy_closure(descendant_id, depth);
CREATE INDEX IF NOT EXISTS idx_hierarchy_closure_tenant ON hierarchy_closure(tenant_id);

CREATE TABLE IF NOT EXISTS scan_checkpoints (
    scan_id    TEXT NOT NULL,
    provider   TEXT NOT NULL,
    service    TEXT NOT NULL,
    scope      TEXT NOT NULL,
    last_token TEXT,
    updated_at TEXT NOT NULL,
    tenant_id  UUID NOT NULL DEFAULT current_setting('app.tenant_id')::uuid,
    PRIMARY KEY (scan_id, provider, service, scope)
);

CREATE INDEX IF NOT EXISTS idx_scan_checkpoints_scan    ON scan_checkpoints(scan_id);
CREATE INDEX IF NOT EXISTS idx_scans_tenant             ON scans(tenant_id);
CREATE INDEX IF NOT EXISTS idx_scan_checkpoints_tenant  ON scan_checkpoints(tenant_id);

ALTER TABLE resources         ENABLE ROW LEVEL SECURITY;
ALTER TABLE relationships     ENABLE ROW LEVEL SECURITY;
ALTER TABLE hierarchy_closure ENABLE ROW LEVEL SECURITY;
ALTER TABLE scans             ENABLE ROW LEVEL SECURITY;
ALTER TABLE scan_checkpoints  ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON resources
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

CREATE POLICY tenant_isolation ON relationships
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

CREATE POLICY tenant_isolation ON hierarchy_closure
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

CREATE POLICY tenant_isolation ON scans
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

CREATE POLICY tenant_isolation ON scan_checkpoints
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
