-- 005_scan_errors_jsonb.sql: structured per-service scan errors.
--
-- The legacy `error` TEXT column concatenates every per-service /
-- per-region failure into one prose blob, which is unparseable for
-- alerting and renders poorly in the UI. Add a sibling `errors` JSONB
-- column carrying an array of structured entries:
--
--   [{"service":"ec2","region":"us-east-1","code":"AccessDenied",
--     "message":"User: arn:… is not authorized to perform: …"}, …]
--
-- Default empty array so existing readers see [] instead of NULL. The
-- legacy `error` column stays — older scans still populate it and external
-- audit consumers already reference the row shape.

ALTER TABLE scans
    ADD COLUMN IF NOT EXISTS errors JSONB NOT NULL DEFAULT '[]'::jsonb;
