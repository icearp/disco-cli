-- 011_resources_reference_only.sql
--
-- Adds reference_only: a resource row that exists only as the endpoint of a
-- reference (a cross-account IAM trust principal, a graph-edge target), not as
-- a resource a scanner enumerated in its own right. InsertResourcesIfAbsent
-- writes these empty-attribute placeholders so an edge always has something to
-- resolve to; UpsertResources (the real-scan path) never sets the column, so a
-- genuinely scanned resource keeps the FALSE default, and when a placeholder's
-- target is later scanned the version-split lands a populated, non-reference
-- current row over it.
--
-- Non-volatile default -> metadata-only on PG 11+, so this is fast even on a
-- large resources table; no CREATE INDEX, so no lock concern.

ALTER TABLE resources ADD COLUMN reference_only BOOLEAN NOT NULL DEFAULT FALSE;
