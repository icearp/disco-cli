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
