-- Single OSS migration. Greenfield install applies this one file.
-- Paid tables (check_runs, findings) live in 002_findings_paid.sql.

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS scans (
    id             TEXT PRIMARY KEY,
    started_at     TEXT NOT NULL,
    finished_at    TEXT,
    status         TEXT NOT NULL CHECK (status IN ('running','completed','failed','partial','pending')),
    providers      TEXT NOT NULL, -- JSON array: ["aws","azure","gcp"]
    scope          TEXT NOT NULL, -- JSON: accounts/orgs/subscriptions scanned
    error          TEXT,
    resource_count INTEGER,
    meta           TEXT           -- JSON: scanner version, options
);

CREATE TABLE IF NOT EXISTS resources (
    id                  TEXT PRIMARY KEY,   -- sha256(provider+accountID+resourceType+nativeID)
    provider            TEXT NOT NULL,      -- 'aws' | 'azure' | 'gcp'
    account_id          TEXT NOT NULL,      -- AWS account ID, Azure subscription GUID, GCP project ID
    account_name        TEXT,
    type                TEXT NOT NULL,      -- 'aws:ec2:instance', 'azure:compute:virtual-machine', etc.
    native_id           TEXT NOT NULL,      -- ARN / Azure resource ID / GCP self-link
    name                TEXT,
    region              TEXT,
    zone                TEXT,
    status              TEXT,
    tags                TEXT,               -- JSON: {"key": "value"}
    attributes          TEXT NOT NULL,      -- JSON: full provider-specific response blob
    managed_by_provider INTEGER NOT NULL DEFAULT 0,
    created_at          TEXT,
    discovered_at       TEXT NOT NULL,
    discovered_by       TEXT NOT NULL,
    verified_at         TEXT,
    verified_by         TEXT,
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

CREATE TABLE IF NOT EXISTS relationships (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
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

-- Per-(scan, provider, service, scope) progress markers so a partially-completed
-- scan can resume without re-listing already-visited pages. last_token is opaque
-- to the store -- per-service callers encode whatever cursor shape the upstream
-- SDK exposes (AWS NextToken, Azure pager continuation, GCP pageToken).
CREATE TABLE IF NOT EXISTS scan_checkpoints (
    scan_id    TEXT NOT NULL,
    provider   TEXT NOT NULL,
    service    TEXT NOT NULL,
    scope      TEXT NOT NULL,
    last_token TEXT,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (scan_id, provider, service, scope)
);

CREATE INDEX IF NOT EXISTS idx_scan_checkpoints_scan ON scan_checkpoints(scan_id);
