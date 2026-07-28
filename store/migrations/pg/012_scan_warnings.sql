-- 012_scan_warnings.sql: structured per-service scan warnings.
--
-- Warnings were render-only: store.ReportWarning fired an in-memory callback,
-- the CLI printed a block, and the process exited. Nothing persisted them, so
-- a scan orchestrated remotely (disco-saas) had no way to show that a run
-- finished while silently skipping a service or a region -- the very failures
-- that are otherwise only visible by pulling logs for a task that has exited.
--
-- Sibling of `errors` (005), same array-of-objects layout:
--
--   [{"service":"aws:bedrockagentcore","region":"us-west-1",
--     "scope":"228886154857/us-west-1","message":"operation error ..."}, ...]
--
-- `code` is deliberately absent. The errors path scrapes an AWS-style code out
-- of the message; a warning's message is usually a full SDK error string where
-- that heuristic yields noise rather than a facet. `scope` is kept verbatim in
-- addition to the parsed `region` -- it is what makes a warning traceable back
-- to a scanner log line, and the errors path discarding it has been a nuisance.
--
-- Default empty array so existing readers see [] instead of NULL. A warning
-- never affects scan status: a `completed` scan carrying warnings is the
-- normal, healthy case.

ALTER TABLE scans
    ADD COLUMN IF NOT EXISTS warnings JSONB NOT NULL DEFAULT '[]'::jsonb;

COMMENT ON COLUMN scans.warnings IS 'structured per-service warnings: array of {service, region, scope, message}. [] not NULL, so readers never branch on missing. Never affects status';
