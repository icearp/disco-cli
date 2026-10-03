-- Drop scan_checkpoints. 001 created it for per-page resume cursors, but no
-- scanner ever wrote one: the only reader was the `disco scan --resume` banner
-- count, which was always 0. `--resume` now only reuses a scan id and
-- re-lists every service, which needs no table.
--
-- IF EXISTS because an embedder may already have removed it. Dropping the
-- table drops its index, FK and any policies an embedder layered on it.
DROP TABLE IF EXISTS scan_checkpoints;
