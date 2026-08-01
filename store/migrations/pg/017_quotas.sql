-- Postgres quotas migration. Service quota limits move out of `resources` and
-- into a table shaped for what they are: a per-account scalar limit, not a
-- provisioned thing.
--
-- WHY A SEPARATE TABLE. Quotas were ~89% of every row in `resources` on a real
-- account, so they dominated every index whether or not anyone queried them,
-- and they have zero inbound graph edges. They are also queried differently:
-- by service and by proximity to the limit, never by name-ordered page slice.
-- A typed `value` column makes "which quotas am I near" and "which have I
-- raised" expressible at all, where a JSON attribute bag made them impossible.
--
-- THE VERSION CHAIN IS DELIBERATE and mirrors `resources` exactly: root_id is
-- the deterministic natural-key hash shared by every version, id is a per-
-- version UUIDv7 primary key, superseded_by is NULL on exactly one row per
-- chain. Both providers store limits only, with no usage value, etag or
-- timestamp, so a version bumps only on a real limit change. That is the whole
-- point: a non-adjustable quota changes only when the PROVIDER changes it,
-- silently and without a request, which is the signal most worth watching.
--
-- INDEXES ARE THREE, EACH WITH A NAMED READER. The point of the split is that
-- quotas stop carrying resource-shaped indexes, so nothing here is speculative.
--   idx_quotas_current_by_natural_key -- the upsert's current-row lookup, and
--       the uniqueness invariant itself. service_code precedes region so the
--       same index also serves filtering an account by service, which is what
--       the list surfaces offer.
--   idx_quotas_root_id -- version-chain traversal (QuotaHistory, and the
--       "non-adjustable rows whose value changed" query).
--   idx_quotas_scan -- the per-row RI probe deleting a `scans` row fires
--       through the discovered_by foreign key below. On `resources` the same
--       probe measured 809 ms and 24,591 buffers without its index against
--       14 ms and 3 with it.
--
-- region is NOT NULL, unlike resources.region, because it is part of the
-- natural key: a NULL there would stop the unique index deduplicating at all.
-- Partition-wide quotas use the same 'global' sentinel the scanners already
-- write.

CREATE TABLE IF NOT EXISTS quotas (
    id                  TEXT PRIMARY KEY,
    root_id             TEXT NOT NULL,
    previous_version_id TEXT,
    superseded_by       TEXT,
    provider            TEXT NOT NULL,
    account_id          TEXT NOT NULL,
    account_name        TEXT,
    region              TEXT NOT NULL,
    service_code        TEXT NOT NULL,
    service_name        TEXT,
    quota_code          TEXT NOT NULL,
    name                TEXT NOT NULL,
    unit                TEXT,
    value               NUMERIC,
    default_value       NUMERIC,
    adjustable          BOOLEAN NOT NULL DEFAULT FALSE,
    global_quota        BOOLEAN NOT NULL DEFAULT FALSE,
    applied_level       TEXT,
    attributes          JSONB NOT NULL,
    discovered_at       TEXT NOT NULL,
    discovered_by       TEXT NOT NULL,
    verified_at         TEXT,
    verified_by         TEXT,
    workspace_id        UUID,
    FOREIGN KEY (discovered_by) REFERENCES scans(id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_quotas_current_by_natural_key
    ON quotas(provider, account_id, service_code, quota_code, region)
    WHERE superseded_by IS NULL;

CREATE INDEX IF NOT EXISTS idx_quotas_root_id ON quotas(root_id);
CREATE INDEX IF NOT EXISTS idx_quotas_scan    ON quotas(discovered_by);

COMMENT ON TABLE quotas IS 'Per-account service quota limits, versioned. Separate from resources because a quota is a limit value rather than a provisioned thing, has no graph edges, and is queried by service and limit-proximity.';
COMMENT ON COLUMN quotas.id                  IS 'per-version row id, UUIDv7. one row per version of one quota';
COMMENT ON COLUMN quotas.root_id             IS 'deterministic natural-key hash shared by every row in this quota''s version chain. the stable cross-version identity';
COMMENT ON COLUMN quotas.previous_version_id IS 'immediate predecessor row in the chain. NULL on the root';
COMMENT ON COLUMN quotas.superseded_by       IS 'successor row that replaced this version. NULL on the current row of every chain, so superseded_by IS NULL selects live state';
COMMENT ON COLUMN quotas.provider            IS 'cloud provider that reported the limit';
COMMENT ON COLUMN quotas.account_id          IS 'account, subscription or project the limit applies to';
COMMENT ON COLUMN quotas.account_name        IS 'denormalized account display name. NULL if unknown';
COMMENT ON COLUMN quotas.region              IS 'region the limit applies in, or the global sentinel for partition-wide limits. part of the natural key, so never NULL';
COMMENT ON COLUMN quotas.service_code        IS 'provider service identifier the limit belongs to. AWS ServiceCode, Azure resource-provider namespace';
COMMENT ON COLUMN quotas.service_name        IS 'human-readable service name for display. NULL if the API reports none';
COMMENT ON COLUMN quotas.quota_code          IS 'provider identifier for the limit within its service';
COMMENT ON COLUMN quotas.name                IS 'human-readable limit name';
COMMENT ON COLUMN quotas.unit                IS 'unit the value is expressed in. NULL when the API reports none';
COMMENT ON COLUMN quotas.value               IS 'the applied limit. NUMERIC rather than TEXT so limit-proximity and raised-versus-default are queryable';
COMMENT ON COLUMN quotas.default_value       IS 'the provider default for this limit. NULL when unknown. A divergence from value means the limit was raised on an adjustable quota, or that the provider moved a hard one';
COMMENT ON COLUMN quotas.adjustable          IS 'whether a customer can request a change. FALSE means any value change came from the provider';
COMMENT ON COLUMN quotas.global_quota        IS 'whether the provider reports this limit identically from every region';
COMMENT ON COLUMN quotas.applied_level       IS 'the level the limit is applied at, where the provider distinguishes account from resource. NULL when it does not';
COMMENT ON COLUMN quotas.attributes          IS 'provider-specific remainder, for anything the typed columns above do not carry';
COMMENT ON COLUMN quotas.discovered_at       IS 'RFC3339 UTC, when this chain was first seen';
COMMENT ON COLUMN quotas.discovered_by       IS 'scans.id of the scan that first saw this chain';
COMMENT ON COLUMN quotas.verified_at         IS 'RFC3339 UTC, last scan that re-saw this version unchanged';
COMMENT ON COLUMN quotas.verified_by         IS 'scans.id of that last verifying scan';
COMMENT ON COLUMN quotas.workspace_id        IS 'nullable single-tenant mirror. disco-saas sets NOT NULL + a GUC default and enforces RLS';
