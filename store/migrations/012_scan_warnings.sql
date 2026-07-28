-- 012_scan_warnings.sql: structured per-service warnings (SQLite path).
-- Mirrors the PG migration pg/012_scan_warnings.sql. Real, not a no-op:
-- Scan.WarningsJSON is in the shared read projection (scanColumns), so the
-- column must exist in both dialects or every SELECT fails with
-- "missing destination name warnings". SQLite stores it as TEXT but the JSON
-- layout is identical, so AppendScanWarning's read-modify-write path produces
-- rows interchangeable with the PG jsonb ones.

ALTER TABLE scans ADD COLUMN warnings TEXT NOT NULL DEFAULT '[]';
