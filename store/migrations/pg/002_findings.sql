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
