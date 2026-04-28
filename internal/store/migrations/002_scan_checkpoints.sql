-- scan_checkpoints persists per-(scan, provider, service, scope) progress
-- markers so a partially-completed scan can resume without re-listing already-
-- visited pages. The schema is intentionally generic — `last_token` is opaque
-- to the store —per-service callers encode whatever cursor shape the upstream
-- SDK exposes (AWS NextToken, Azure pager continuation, GCP pageToken).
--
-- Lifecycle: a scanner saves a row at the END of each successfully-upserted
-- page —on resume, scanners read the latest row for their (scan, service,
-- scope) tuple and pass `last_token` to the SDK. Completion deletes all rows
-- for the scan_id (DeleteScanCheckpoints) so the table stays bounded.
--
-- The OSS schema lands here so the paid incremental-scan feature
-- (Phase 6 / ROADMAP_paid G3) can consume it without a second migration.

CREATE TABLE IF NOT EXISTS scan_checkpoints (
    scan_id    TEXT NOT NULL,           -- FK to scans(id) (no constraint: checkpoints
                                        -- may outlive the scan row during partial runs)
    provider   TEXT NOT NULL,           -- 'aws' | 'azure' | 'gcp'
    service    TEXT NOT NULL,           -- e.g. 'aws:ec2', 'azure:compute', 'gcp:compute'
    scope      TEXT NOT NULL,           -- account/region/sub/project — opaque, per-provider
    last_token TEXT,                    -- opaque continuation token (NULL = first page consumed)
    updated_at TEXT NOT NULL,
    PRIMARY KEY (scan_id, provider, service, scope)
);

CREATE INDEX IF NOT EXISTS idx_scan_checkpoints_scan ON scan_checkpoints(scan_id);
