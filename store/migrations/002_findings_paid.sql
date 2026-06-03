-- check_runs + findings persist `disco check` results so paid builds can
-- query historical compliance runs. Tables are additive — OSS builds apply
-- the migration but never write rows (no --persist flag is registered).
-- CASCADE on the FK so future findings-retention pruning removes children
-- automatically when a check_run is deleted.

CREATE TABLE IF NOT EXISTS check_runs (
    id              TEXT PRIMARY KEY,
    started_at      TEXT NOT NULL,
    finished_at     TEXT,
    rules_paths     TEXT NOT NULL,
    packs           TEXT NOT NULL,
    severity_filter TEXT,
    resource_count  INTEGER,
    finding_count   INTEGER,
    workspace_id    TEXT           -- PG mirror: per-workspace RLS discriminator
);

CREATE INDEX IF NOT EXISTS idx_check_runs_started_at ON check_runs(started_at DESC);

CREATE TABLE IF NOT EXISTS findings (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
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
    workspace_id  TEXT           -- PG mirror: per-workspace RLS discriminator
);

CREATE INDEX IF NOT EXISTS idx_findings_check_run_id ON findings(check_run_id);
CREATE INDEX IF NOT EXISTS idx_findings_severity ON findings(severity);
CREATE INDEX IF NOT EXISTS idx_findings_finding_id ON findings(finding_id);
