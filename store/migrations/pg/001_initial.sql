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

-- ---------------------------------------------------------------------------
-- Table + column documentation. workspace_id is described once here as the
-- nullable single-tenant mirror; disco-saas layers NOT NULL + RLS on top.
-- ---------------------------------------------------------------------------

COMMENT ON TABLE schema_migrations IS 'Applied-migration ledger; the runner skips any version already recorded here, so re-running is a no-op.';
COMMENT ON COLUMN schema_migrations.version    IS 'migration version number, unique and monotonic';
COMMENT ON COLUMN schema_migrations.applied_at IS 'when the runner applied this version';

COMMENT ON TABLE scans IS 'One scan run — a single disco scan invocation over a provider set and scope. Terminal rows drive notify + webhook dispatch downstream.';
COMMENT ON COLUMN scans.id             IS 'scan id; the discovered_by / verified_by target on resources';
COMMENT ON COLUMN scans.started_at     IS 'RFC3339 UTC, scan start (TEXT, not timestamptz, for SQLite parity)';
COMMENT ON COLUMN scans.finished_at    IS 'RFC3339 UTC, terminal transition time; NULL while running';
COMMENT ON COLUMN scans.status         IS 'lifecycle: running|pending|completed|partial|failed; the last three are terminal';
COMMENT ON COLUMN scans.providers      IS 'comma-joined providers this scan covered';
COMMENT ON COLUMN scans.scope          IS 'serialized scan scope (regions/services/accounts)';
COMMENT ON COLUMN scans.error          IS 'legacy prose blob concatenating every per-service failure; superseded by errors JSONB, kept for old rows + external consumers';
COMMENT ON COLUMN scans.resource_count IS 'resources upserted by this scan';
COMMENT ON COLUMN scans.meta           IS 'free-form JSON scan metadata (scanner version, timing)';
COMMENT ON COLUMN scans.workspace_id   IS 'nullable single-tenant mirror; disco-saas sets NOT NULL + a GUC default and enforces RLS';

COMMENT ON TABLE resources IS 'Cloud resource inventory, version-chained: a scan writes a new row only when a resource changes and keeps superseded versions, so the table is the full change history keyed by root_id.';
COMMENT ON COLUMN resources.id                  IS 'per-version row PK (UUID text); reads alias root_id AS id for Resource projections';
COMMENT ON COLUMN resources.provider            IS 'cloud provider (aws|azure|gcp)';
COMMENT ON COLUMN resources.account_id          IS 'cloud account / subscription / project id';
COMMENT ON COLUMN resources.account_name        IS 'human label for account_id; NULL if unknown';
COMMENT ON COLUMN resources.type                IS 'resource type in the provider taxonomy';
COMMENT ON COLUMN resources.native_id           IS 'provider-native id / ARN; unique per (provider, account_id) and the FOCUS cost-match key';
COMMENT ON COLUMN resources.name                IS 'display name; NULL when the provider gives none';
COMMENT ON COLUMN resources.region              IS 'cloud region; NULL for global resources';
COMMENT ON COLUMN resources.zone                IS 'availability zone; NULL when not applicable';
COMMENT ON COLUMN resources.status              IS 'provider lifecycle status; NULL if unreported';
COMMENT ON COLUMN resources.tags                IS 'resource tags as a JSON object';
COMMENT ON COLUMN resources.attributes          IS 'full scanned attribute blob (redact.Apply output); JSONB since migration 007';
COMMENT ON COLUMN resources.managed_by_provider IS 'true when provider-managed rather than user-created';
COMMENT ON COLUMN resources.created_at          IS 'provider-reported resource creation time (RFC3339); NULL if unknown';
COMMENT ON COLUMN resources.discovered_at       IS 'RFC3339 UTC, first time any scan saw this row';
COMMENT ON COLUMN resources.discovered_by       IS 'scans.id that first observed this row';
COMMENT ON COLUMN resources.workspace_id        IS 'nullable single-tenant mirror; disco-saas sets NOT NULL + a GUC default and enforces RLS';

COMMENT ON TABLE relationships IS 'Edges between resources (keyed by root_id, not per-version id), e.g. contains / uses; edge-target validity is an application invariant, not an FK.';
COMMENT ON COLUMN relationships.id            IS 'edge PK';
COMMENT ON COLUMN relationships.from_id       IS 'source resource root_id';
COMMENT ON COLUMN relationships.to_id         IS 'target resource root_id';
COMMENT ON COLUMN relationships.kind          IS 'edge type (e.g. contains, uses)';
COMMENT ON COLUMN relationships.direction     IS 'directed | undirected';
COMMENT ON COLUMN relationships.attributes    IS 'edge attributes as JSON; NULL if none (JSONB since migration 007)';
COMMENT ON COLUMN relationships.discovered_at IS 'RFC3339 UTC, first time this edge was seen';
COMMENT ON COLUMN relationships.workspace_id  IS 'nullable single-tenant mirror; disco-saas sets NOT NULL + a GUC default and enforces RLS';

COMMENT ON TABLE hierarchy_closure IS 'Transitive ancestor->descendant closure over containment edges, one row per reachable pair, so subtree queries are a single indexed lookup.';
COMMENT ON COLUMN hierarchy_closure.ancestor_id   IS 'ancestor resource root_id';
COMMENT ON COLUMN hierarchy_closure.descendant_id IS 'descendant resource root_id';
COMMENT ON COLUMN hierarchy_closure.depth         IS 'edge count between the pair (0 = the resource itself)';
COMMENT ON COLUMN hierarchy_closure.workspace_id  IS 'nullable single-tenant mirror; disco-saas sets NOT NULL + a GUC default and enforces RLS';

COMMENT ON TABLE scan_checkpoints IS 'Per-(scan, provider, service, scope) pagination cursor so an interrupted scan resumes mid-service instead of restarting.';
COMMENT ON COLUMN scan_checkpoints.scan_id      IS 'owning scans.id';
COMMENT ON COLUMN scan_checkpoints.provider     IS 'cloud provider being paginated';
COMMENT ON COLUMN scan_checkpoints.service      IS 'provider service being paginated';
COMMENT ON COLUMN scan_checkpoints.scope        IS 'scope key within the service';
COMMENT ON COLUMN scan_checkpoints.last_token   IS 'opaque provider pagination token; NULL before the first page';
COMMENT ON COLUMN scan_checkpoints.updated_at   IS 'RFC3339 UTC, last checkpoint write';
COMMENT ON COLUMN scan_checkpoints.workspace_id IS 'nullable single-tenant mirror; disco-saas sets NOT NULL + a GUC default and enforces RLS';
