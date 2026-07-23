-- Postgres findings migration. Greenfield install applies this file after 001.
-- check_runs + findings persist `disco check --persist` results so callers can
-- query historical compliance runs. workspace_id is a plain nullable mirror of
-- the SQLite column; the disco-saas control plane layers tenant_id + RLS on top.

CREATE TABLE IF NOT EXISTS check_runs (
    id              TEXT PRIMARY KEY,
    started_at      TEXT NOT NULL,
    finished_at     TEXT,
    rules_paths     TEXT NOT NULL,
    packs           TEXT NOT NULL,
    severity_filter TEXT,
    resource_count  INTEGER,
    finding_count   INTEGER,
    workspace_id    UUID
);

CREATE INDEX IF NOT EXISTS idx_check_runs_started_at ON check_runs(started_at DESC);

CREATE TABLE IF NOT EXISTS findings (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    check_run_id  TEXT NOT NULL REFERENCES check_runs(id) ON DELETE CASCADE,
    finding_id    TEXT NOT NULL,
    resource_id   TEXT NOT NULL,
    severity      TEXT NOT NULL,
    message       TEXT NOT NULL,
    provider      TEXT,
    type          TEXT,
    name          TEXT,
    region        TEXT,
    category      TEXT,
    remediation   TEXT,
    ref_url       TEXT,
    tags          TEXT,
    workspace_id  UUID
);

CREATE INDEX IF NOT EXISTS idx_findings_check_run_id ON findings(check_run_id);
CREATE INDEX IF NOT EXISTS idx_findings_severity     ON findings(severity);
CREATE INDEX IF NOT EXISTS idx_findings_finding_id   ON findings(finding_id);

COMMENT ON TABLE check_runs IS 'One persisted disco check run: a historical compliance evaluation over the inventory, so past runs stay queryable.';
COMMENT ON COLUMN check_runs.id              IS 'check-run id. the findings.check_run_id target';
COMMENT ON COLUMN check_runs.started_at      IS 'RFC3339 UTC, run start';
COMMENT ON COLUMN check_runs.finished_at     IS 'RFC3339 UTC, run end. NULL while running';
COMMENT ON COLUMN check_runs.rules_paths     IS 'rule source paths evaluated';
COMMENT ON COLUMN check_runs.packs           IS 'rule packs applied';
COMMENT ON COLUMN check_runs.severity_filter IS 'minimum severity retained. NULL = no filter';
COMMENT ON COLUMN check_runs.resource_count  IS 'resources evaluated';
COMMENT ON COLUMN check_runs.finding_count   IS 'findings produced';
COMMENT ON COLUMN check_runs.workspace_id    IS 'nullable single-tenant mirror. disco-saas sets NOT NULL + a GUC default and enforces RLS';

COMMENT ON TABLE findings IS 'Individual policy findings from a check_run, one row per (rule, resource) hit. cascades away with its run.';
COMMENT ON COLUMN findings.id           IS 'finding PK';
COMMENT ON COLUMN findings.check_run_id IS 'owning check_runs.id. ON DELETE CASCADE';
COMMENT ON COLUMN findings.finding_id   IS 'rule/finding identifier that fired';
COMMENT ON COLUMN findings.resource_id  IS 'offending resource root_id';
COMMENT ON COLUMN findings.severity     IS 'finding severity';
COMMENT ON COLUMN findings.message      IS 'human-readable finding text';
COMMENT ON COLUMN findings.provider     IS 'denormalized resource provider for filtering. NULL if absent';
COMMENT ON COLUMN findings.type         IS 'denormalized resource type for filtering. NULL if absent';
COMMENT ON COLUMN findings.name         IS 'denormalized resource name for display. NULL if absent';
COMMENT ON COLUMN findings.region       IS 'denormalized resource region for filtering. NULL if absent';
COMMENT ON COLUMN findings.category     IS 'rule category. NULL if unset';
COMMENT ON COLUMN findings.remediation  IS 'remediation guidance. NULL if none';
COMMENT ON COLUMN findings.ref_url      IS 'reference / documentation URL. NULL if none';
COMMENT ON COLUMN findings.tags         IS 'finding tags as JSON text. NULL if none';
COMMENT ON COLUMN findings.workspace_id IS 'nullable single-tenant mirror. disco-saas sets NOT NULL + a GUC default and enforces RLS';
