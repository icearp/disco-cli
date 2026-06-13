-- Postgres findings migration. Greenfield install applies this file after 001.
-- check_runs + findings persist `disco check --persist` results so callers can
-- query historical compliance runs. Tables include tenant_id + workspace_id
-- + RLS to keep parity with the two-level isolation policy set in 001.

CREATE TABLE IF NOT EXISTS check_runs (
    id              TEXT PRIMARY KEY,
    started_at      TEXT NOT NULL,
    finished_at     TEXT,
    rules_paths     TEXT NOT NULL,
    packs           TEXT NOT NULL,
    severity_filter TEXT,
    resource_count  INTEGER,
    finding_count   INTEGER,
    tenant_id       UUID NOT NULL DEFAULT current_setting('app.tenant_id')::uuid,
    workspace_id    UUID NOT NULL DEFAULT current_setting('app.workspace_id')::uuid
);

CREATE INDEX IF NOT EXISTS idx_check_runs_started_at ON check_runs(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_check_runs_tenant     ON check_runs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_check_runs_workspace  ON check_runs(workspace_id);

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
    tenant_id     UUID NOT NULL DEFAULT current_setting('app.tenant_id')::uuid,
    workspace_id  UUID NOT NULL DEFAULT current_setting('app.workspace_id')::uuid
);

CREATE INDEX IF NOT EXISTS idx_findings_check_run_id ON findings(check_run_id);
CREATE INDEX IF NOT EXISTS idx_findings_severity     ON findings(severity);
CREATE INDEX IF NOT EXISTS idx_findings_finding_id   ON findings(finding_id);
CREATE INDEX IF NOT EXISTS idx_findings_tenant       ON findings(tenant_id);
CREATE INDEX IF NOT EXISTS idx_findings_workspace    ON findings(workspace_id);

ALTER TABLE check_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE findings   ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON check_runs
    USING (tenant_id = current_setting('app.tenant_id')::uuid
        AND workspace_id = current_setting('app.workspace_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid
        AND workspace_id = current_setting('app.workspace_id')::uuid);

CREATE POLICY tenant_isolation ON findings
    USING (tenant_id = current_setting('app.tenant_id')::uuid
        AND workspace_id = current_setting('app.workspace_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid
        AND workspace_id = current_setting('app.workspace_id')::uuid);
