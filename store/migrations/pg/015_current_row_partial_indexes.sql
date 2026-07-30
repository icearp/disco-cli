-- 015: carry the current-row predicate on the indexes that only ever serve
-- current-state queries, and drop one relationship index a wider unique key
-- already absorbs.
--
-- `resources` is a version-chained table: every historical version of a
-- resource stays forever, by design, because the change history IS the
-- product. So a NON-partial index on it grows with total history, while every
-- query that reads it filters to the current row. Measured on a 101,868-row
-- dev workspace, 1.3% of rows were superseded -- and that fraction only ever
-- climbs, because current rows plateau at the size of the estate while
-- superseded ones accumulate with every scan. Adding `WHERE superseded_by IS
-- NULL` ties each index's size to live resources instead.
--
-- THE INVARIANT THIS RESTS ON, stated here so a future change breaks review
-- rather than production: every read of `resources` that filters on one of
-- these five columns carries the current-row predicate. In this repo that is
-- structural -- applyCurrentVersionPredicate (store/resources_hooks.go) and
-- currentVersionWhereSQL build those reads, and ListResources, DiffScans,
-- GraphWalk, CountManaged and the relationship existence probe all go through
-- them. The SaaS reader of native_id (focusreport.MatchResources) spells
-- `r.superseded_by IS NULL` inline. The one resources SELECT built by neither
-- helper is CountResourcesByScan, and it is not a counterexample: it filters
-- on discovered_by, which is served by idx_resources_scan, the index this
-- migration deliberately leaves non-partial (see below). A FUTURE query that
-- omits the predicate does not get a wrong answer -- it silently loses the
-- index and falls to a sequential scan, which is the kind of regression that
-- shows up as a latency mystery months later. If you add such a query, add the
-- predicate or give it its own index. Do not widen these back.
--
-- Two of the five get a NARROWER predicate than the current-row one, because
-- for them the column's own distribution is the bigger win:
--
--   * status  -> WHERE status IS NOT NULL. 1,602 of 103,238 rows (1.55%) are
--     non-NULL. The planner proves `status = 'X'` implies the predicate, and
--     ResourceFilter.Status is guarded by `f.Status != ""` (store/resources.go)
--     so it never asks for NULL. Every existing query still reaches the index,
--     at ~2% of the entries, and 98.4% of rows stop paying an index write.
--
--   * managed_by_provider -> WHERE NOT managed_by_provider. 1,633 rows (1.58%),
--     and BOTH readers ask only the false side: ListResources emits
--     `managed_by_provider = false` when IncludeManaged is unset and NO
--     predicate at all when it is set, and CountManaged asks the true side.
--     The true side is correctly a sequential scan today and stays one.
--
-- NOT TOUCHED, deliberately: idx_resources_scan (discovered_by). It is the one
-- `resources` index that must stay non-partial. `resources.discovered_by
-- REFERENCES scans(id)` makes Postgres fire a referential-integrity probe per
-- deleted `scans` row, and that probe carries no `superseded_by` predicate, so
-- a partial index cannot serve it -- measured at 14 ms / 3 buffers with the
-- index versus 809 ms / 24,591 buffers without. Same exemption as
-- idx_resources_root_id, which is the history-walk path and must see every
-- version by construction.
--
-- LOCKING: these are DROP + CREATE on what is a 202 MB table in the dev
-- estate, and both runners exec a migration file inside ONE transaction, so
-- CONCURRENTLY is unreachable here (it cannot run in a transaction block).
-- Pre-launch, with no customer traffic, that is a non-event and is why this
-- ships as an ordinary migration. Once real tenants exist, an index change on
-- `resources` or `scans` belongs out of band as CREATE/DROP INDEX
-- CONCURRENTLY instead of in this stream.

DROP INDEX IF EXISTS idx_resources_native_id;
CREATE INDEX IF NOT EXISTS idx_resources_native_id
    ON resources (native_id)
    WHERE superseded_by IS NULL;

DROP INDEX IF EXISTS idx_resources_region;
CREATE INDEX IF NOT EXISTS idx_resources_region
    ON resources (provider, account_id, region)
    WHERE superseded_by IS NULL;

DROP INDEX IF EXISTS idx_resources_type;
CREATE INDEX IF NOT EXISTS idx_resources_type
    ON resources (provider, type)
    WHERE superseded_by IS NULL;

DROP INDEX IF EXISTS idx_resources_status;
CREATE INDEX IF NOT EXISTS idx_resources_status
    ON resources (status)
    WHERE superseded_by IS NULL AND status IS NOT NULL;

DROP INDEX IF EXISTS idx_resources_managed;
CREATE INDEX IF NOT EXISTS idx_resources_managed
    ON resources (managed_by_provider)
    WHERE superseded_by IS NULL AND NOT managed_by_provider;

-- idx_rel_from (from_id, kind) is absorbed by the table's own UNIQUE
-- (from_id, to_id, kind) constraint index: Postgres can use a leading column
-- plus a non-contiguous later one as Index Conds, skipping to_id, so a
-- `from_id = ? AND kind = ?` lookup is served by the wider index without
-- idx_rel_from existing. Measured 13 scans against 11,775 on the unique index,
-- and `relationships` takes real update traffic (2,817 updates at 65% HOT), so
-- the duplicate entry is not free.
--
-- idx_rel_to STAYS: nothing else leads with to_id, so the reverse-edge lookup
-- has no other access path.
DROP INDEX IF EXISTS idx_rel_from;
