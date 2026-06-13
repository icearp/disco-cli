-- 004_scan_metadata.sql: widen tenant `scans` with auditor-required
-- attribution. An external control plane plumbs these values forward and
-- persists them on the pre-claimed scan row alongside the existing columns.
--
--   scanner_version — binary build id (so an auditor can correlate a
--                     scan id back to a specific scanner release)
--   principal_arn   — the AssumeRole'd ARN at trigger time
--   account_id      — the connected_accounts.cloud_account_id snapshot
--   regions         — comma-joined effective regions after server-side
--                     allowlist narrowing
--   services        — comma-joined effective services (likewise)
--   triggered_by    — public.users.id of the human who hit POST /scans
--                     (the api-key creator under bearer auth)
--
-- NOT NULL not enforced — legacy rows pre-migration won't carry the
-- data and back-filling would change row digests downstream.

ALTER TABLE scans
    ADD COLUMN IF NOT EXISTS scanner_version TEXT,
    ADD COLUMN IF NOT EXISTS principal_arn   TEXT,
    ADD COLUMN IF NOT EXISTS account_id      TEXT,
    ADD COLUMN IF NOT EXISTS regions         TEXT,
    ADD COLUMN IF NOT EXISTS services        TEXT,
    ADD COLUMN IF NOT EXISTS triggered_by    UUID;

CREATE INDEX IF NOT EXISTS scans_account_id_idx   ON scans (account_id);
CREATE INDEX IF NOT EXISTS scans_triggered_by_idx ON scans (triggered_by);
