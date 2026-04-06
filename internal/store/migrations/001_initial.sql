CREATE TABLE IF NOT EXISTS schema_migrations (
    version  INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS scans (
    id             TEXT PRIMARY KEY,
    started_at     TEXT NOT NULL,
    finished_at    TEXT,
    status         TEXT NOT NULL CHECK (status IN ('running','completed','failed','partial')),
    providers      TEXT NOT NULL, -- JSON array: ["aws","azure","gcp"]
    scope          TEXT NOT NULL, -- JSON: accounts/orgs/subscriptions scanned
    error          TEXT,
    resource_count INTEGER,
    meta           TEXT           -- JSON: scanner version, options
);

CREATE TABLE IF NOT EXISTS resources (
    id            TEXT PRIMARY KEY,   -- sha256(provider+accountID+resourceType+nativeID))
    provider      TEXT NOT NULL,      -- 'aws' | 'azure' | 'gcp'
    account_id    TEXT NOT NULL,      -- AWS account ID, Azure subscription GUID, GCP project ID
    account_name  TEXT,
    type          TEXT NOT NULL,      -- namespaced type: 'aws:ec2:instance', 'azure:compute:virtual-machine'
    native_id     TEXT NOT NULL,      -- ARN / Azure resource ID / GCP self-link
    name          TEXT,
    region        TEXT,
    zone          TEXT,
    status        TEXT,
    tags          TEXT,               -- JSON: {"key": "value"}
    attributes    TEXT NOT NULL,      -- JSON: full provider-specific response blob
    created_at    TEXT,
    discovered_at TEXT NOT NULL,
    discovered_by TEXT NOT NULL,
    verified_at   TEXT,
    verified_by   TEXT,
    FOREIGN KEY (discovered_by)     REFERENCES scans(id),
    FOREIGN KEY (verified_by) REFERENCES scans(id)
);

CREATE INDEX IF NOT EXISTS idx_resources_provider  ON resources(provider);
CREATE INDEX IF NOT EXISTS idx_resources_type      ON resources(provider, type);
CREATE INDEX IF NOT EXISTS idx_resources_account   ON resources(provider, account_id);
CREATE INDEX IF NOT EXISTS idx_resources_region    ON resources(provider, account_id, region);
CREATE INDEX IF NOT EXISTS idx_resources_native_id ON resources(native_id);
CREATE INDEX IF NOT EXISTS idx_resources_status    ON resources(status);
CREATE INDEX IF NOT EXISTS idx_resources_scan      ON resources(discovered_by);

CREATE TABLE IF NOT EXISTS relationships (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    from_id       TEXT NOT NULL,
    to_id         TEXT NOT NULL,
    kind          TEXT NOT NULL,   -- 'contains'|'attached-to'|'uses'|'routes-to'|'peer'|'assumes'
    direction     TEXT NOT NULL DEFAULT 'directed' CHECK (direction IN ('directed','undirected')),
    attributes    TEXT,            -- JSON: relationship-specific metadata
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
    depth         INTEGER NOT NULL, -- 0=self, 1=direct child, etc.
    PRIMARY KEY (ancestor_id, descendant_id),
    FOREIGN KEY (ancestor_id)   REFERENCES resources(id),
    FOREIGN KEY (descendant_id) REFERENCES resources(id)
);

CREATE INDEX IF NOT EXISTS idx_closure_descendant ON hierarchy_closure(descendant_id, depth);
