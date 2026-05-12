-- 003_scan_errors.sql: structured per-service errors (SQLite path).
-- Mirrors the PG migration 005_scan_errors_jsonb.sql; SQLite stores it
-- as TEXT but the JSON layout is identical, so AppendScanError's
-- read-modify-write path produces interchangeable rows.

ALTER TABLE scans ADD COLUMN errors TEXT NOT NULL DEFAULT '[]';
