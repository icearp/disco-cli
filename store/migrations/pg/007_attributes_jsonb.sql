-- Promote the attributes columns from TEXT (the SQLite-parity lowest common
-- denominator) to native JSONB on Postgres, matching what tags and
-- scans.errors already do. The Go field stays a string; pgx inserts/scans
-- JSONB transparently. SQLite keeps attributes as TEXT (no SQLite migration) --
-- the two backends already diverge in type for tags/errors.
--
-- resources.attributes is NOT NULL and always a full JSON blob from the
-- scanner (redact.Apply output), so a plain cast is safe.
-- relationships.attributes is nullable and rarely set; NULLIF guards any
-- empty-string rows so the cast cannot fail.

ALTER TABLE resources ALTER COLUMN attributes TYPE JSONB USING attributes::jsonb;

ALTER TABLE relationships ALTER COLUMN attributes TYPE JSONB USING NULLIF(attributes, '')::jsonb;
