-- SQLite mirror of pg/017_quotas.sql. Service quota limits move out of
-- `resources` into a table shaped for what they are: a per-account scalar
-- limit, not a provisioned thing. The PG file carries the full rationale.
--
-- Column names match the Postgres file exactly -- scripts/check-migrations.sh
-- diffs (table, column) pairs per dialect, so this is a real mirror and not a
-- placeholder. Types differ where PG has the richer native one: JSONB becomes
-- TEXT (as resources.attributes already does) and NUMERIC becomes REAL.
-- BOOLEAN becomes INTEGER, matching resources.managed_by_provider.
--
-- The version chain mirrors `resources`: root_id is the deterministic
-- natural-key hash shared by every version, id is a per-version UUIDv7 primary
-- key, superseded_by is NULL on exactly one row per chain.
--
-- region is NOT NULL, unlike resources.region, because it is part of the
-- natural key -- a NULL there would stop the unique index deduplicating at all.

CREATE TABLE IF NOT EXISTS quotas (
    id                  TEXT PRIMARY KEY,   -- per-version UUIDv7
    root_id             TEXT NOT NULL,      -- deterministic natural-key hash, stable across the chain
    previous_version_id TEXT,
    superseded_by       TEXT,               -- NULL on the current row of every chain
    provider            TEXT NOT NULL,
    account_id          TEXT NOT NULL,
    account_name        TEXT,
    region              TEXT NOT NULL,      -- 'global' sentinel for partition-wide limits
    service_code        TEXT NOT NULL,      -- AWS ServiceCode, Azure resource-provider namespace
    service_name        TEXT,
    quota_code          TEXT NOT NULL,
    name                TEXT NOT NULL,
    unit                TEXT,
    value               REAL,               -- PG mirror: NUMERIC
    default_value       REAL,               -- PG mirror: NUMERIC
    adjustable          INTEGER NOT NULL DEFAULT 0,
    global_quota        INTEGER NOT NULL DEFAULT 0,
    applied_level       TEXT,
    attributes          TEXT NOT NULL,      -- JSON: provider-specific remainder
    discovered_at       TEXT NOT NULL,
    discovered_by       TEXT NOT NULL,
    verified_at         TEXT,
    verified_by         TEXT,
    workspace_id        TEXT,               -- PG mirror: per-workspace RLS discriminator
    FOREIGN KEY (discovered_by) REFERENCES scans(id)
);

-- The upsert's current-row lookup, and the uniqueness invariant itself.
-- service_code precedes region so the same index serves filtering an account
-- by service, which is what the list surfaces offer.
CREATE UNIQUE INDEX IF NOT EXISTS idx_quotas_current_by_natural_key
    ON quotas(provider, account_id, service_code, quota_code, region)
    WHERE superseded_by IS NULL;

-- Version-chain traversal: QuotaHistory, and non-adjustable rows whose value changed.
CREATE INDEX IF NOT EXISTS idx_quotas_root_id ON quotas(root_id);

-- The per-row RI probe that deleting a `scans` row fires through discovered_by.
CREATE INDEX IF NOT EXISTS idx_quotas_scan ON quotas(discovered_by);
