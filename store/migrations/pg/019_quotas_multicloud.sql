-- Reshape `quotas` for more than one cloud, on FOCUS terms.
--
-- 017 designed the table against AWS Service Quotas and Azure was fitted to it
-- afterwards, so two of its columns describe AWS rather than a quota. This
-- replaces them with columns every provider can answer, and adds the facts all
-- of them report that nothing stored.
--
-- WHY FOCUS NAMES. The dimensions of a quota row -- who owns it, where, which
-- service, which resource kind -- are the same dimensions FOCUS 1.4 already
-- standardizes for cost, and the SaaS half of this product speaks FOCUS
-- already. Adopting its Column IDs here means one word means one thing across
-- inventory, quotas and cost, and it means the next provider fills in an
-- existing column rather than needing a new one. FOCUS has no quota or limit
-- dataset at all, so the limit-specific columns (adjustable, the rate window)
-- keep disco's own names; only the dimensions borrow.
--
-- Two of FOCUS's answers are the reason this migration is SMALLER than the
-- first draft of it. FOCUS expresses "not regional" and "not resource-scoped"
-- by NULLABILITY, not by a scope enum: a non-regional charge has a null
-- RegionId, a non-resource charge has a null ResourceId/ResourceType. An
-- earlier draft added a scope enum with values global/account/region/zone/
-- resource, which conflated three independent axes -- geographic extent,
-- container level, and per-resource granularity -- into one column that could
-- not express "an organization-level, regional, per-resource quota". It is
-- gone. `region` already carries the global case through the 'global' sentinel
-- 017 established, and resource-scoping is now "resource_type IS NOT NULL".
--
-- WHAT EACH COLUMN IS FOR, measured against the 153,414 current quota rows on
-- the dev tenant rather than inferred.
--
--   dimension_key, part of the natural key -- one quota identifier can carry
--       different values per dimension set. AWS puts that in
--       QuotaContext.ContextId; GCP's Cloud Quotas returns a value per entry in
--       dimensionsInfos under one quotaId. Without it those collapse onto one
--       row and overwrite each other. No FOCUS analog: FOCUS describes charges,
--       which are already per-dimension by construction.
--
--   period_unit / period_value -- a rate window. AWS ServiceQuota.Period is a
--       {PeriodUnit, PeriodValue} pair; Azure reports properties.quotaPeriod as
--       an ISO 8601 duration. Populated on 70,936 of the 153,414 rows -- SECOND
--       1, HOUR 1, MINUTE 5, MINUTE 1, HOUR 2, HOUR 24, DAY 1, DAY 30 -- and
--       discarded by every typed column so far. Without it "10 per second" and
--       "10 in total" are the same row, which is what made the Service Quotas
--       API pacing work guess. A NULL period_unit IS the count case, so no
--       separate limit-kind column is needed to tell them apart. FOCUS's
--       ChargePeriodStart/End are instants a charge accrued over, not a window
--       a limit resets on, so these keep disco names. The CHECK stops at week
--       because nothing can currently write past it: AWS's PeriodUnit enum ends
--       at WEEK, and the Azure ISO 8601 reader rejects a date-section M, which
--       is months. A month window means widening both together.
--
--   resource_type (FOCUS ResourceType), replacing applied_level -- what kind of
--       thing a resource-scoped limit counts: AWS::APS::Workspace,
--       AWS::Connect::Instance, Azure properties.resourceType. applied_level
--       reads ACCOUNT on all 153,414 rows including the 1,875 whose
--       QuotaContext.ContextScope is RESOURCE, so as stored it carries no
--       information; the resource kind is the fact worth keeping, and its
--       presence is what marks the row resource-scoped.
--
--   availability_zone (FOCUS AvailabilityZone) -- a zone-scoped limit. GCP
--       reports a zone dimension and OCI an availability domain; AWS Service
--       Quotas has none, so it stays NULL there. It is PROMOTED OUT OF
--       dimension_key rather than independent of it -- the zone is the thing
--       distinguishing those rows, so it is already in the key, and this column
--       exists so a zone is filterable without parsing an opaque key.
--
--   sub_account_type (FOCUS SubAccountType) -- what kind of container
--       account_id names: Account, Subscription, Project. FOCUS models exactly
--       two account levels (BillingAccount and SubAccount), so a GCP folder- or
--       organization-level quota still has no FOCUS home; that stays a disco
--       extension when a provider forces it.
--
-- global_quota is dropped outright. It is a two-value AWS-only flag,
-- permanently false everywhere else, and it duplicates what `region` already
-- says: a global quota is recorded once under the 'global' sentinel. Both it
-- and applied_level survive verbatim in the attributes remainder.
--
-- adjustable stays NOT NULL, and that is a deferral rather than a decision. It
-- should be nullable -- a provider that cannot say must not be made to assert
-- false, and Azure's isQuotaApplicable is already a pointer whose nil silently
-- becomes false. SQLite can only relax a column constraint by rebuilding the
-- table, and the copy that needs is DML, which TestMigrationsCarryNoDML rejects
-- for good reason. It lands with the GCP scanner, the first provider that
-- genuinely cannot answer.
--
-- NO BACKFILL, DELIBERATELY, and the two reasons are worth stating because
-- both were nearly missed.
--
--   root_id does not move. QuotaID appends dimension_key to the hash input only
--   when it is non-empty, so the pre-019 five-part hash is what an empty
--   dimension_key still produces. Every existing row keeps its identity and
--   every version chain stays walkable.
--
--   No existing row gets a non-empty dimension_key either. AWS reports
--   ContextId as the literal '*' on all 1,875 rows that carry a QuotaContext --
--   never an ARN -- and '*' means "every resource", which is the absence of a
--   dimension. The scanner maps it to empty. Had it been stored verbatim those
--   1,875 rows would have re-keyed, stranding their predecessors as current
--   rows that no later scan could ever reach.
--
-- The new columns are therefore NULL on every existing row until the next scan
-- fills them, which is the honest state: nothing knows a stored row's period or
-- resource kind until the provider is asked again.
--
-- The unique index gains dimension_key. It only ever loosens: every existing
-- row shares the same empty value, so no pair that was distinct becomes equal.

ALTER TABLE quotas
    ADD COLUMN IF NOT EXISTS dimension_key     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS period_unit       TEXT,
    ADD COLUMN IF NOT EXISTS period_value      INTEGER,
    ADD COLUMN IF NOT EXISTS resource_type     TEXT,
    ADD COLUMN IF NOT EXISTS availability_zone TEXT,
    ADD COLUMN IF NOT EXISTS sub_account_type  TEXT;

ALTER TABLE quotas DROP CONSTRAINT IF EXISTS quotas_period_unit_check;
ALTER TABLE quotas ADD CONSTRAINT quotas_period_unit_check
    CHECK (period_unit IS NULL OR period_unit IN
        ('microsecond', 'millisecond', 'second', 'minute', 'hour', 'day', 'week'));

DROP INDEX IF EXISTS idx_quotas_current_by_natural_key;
CREATE UNIQUE INDEX IF NOT EXISTS idx_quotas_current_by_natural_key
    ON quotas(provider, account_id, service_code, quota_code, region, dimension_key)
    WHERE superseded_by IS NULL;

ALTER TABLE quotas DROP COLUMN IF EXISTS global_quota;
ALTER TABLE quotas DROP COLUMN IF EXISTS applied_level;

COMMENT ON COLUMN quotas.dimension_key     IS 'provider dimension this row''s value belongs to, empty when the limit is undimensioned. part of the natural key, and appended to the identity hash only when non-empty so undimensioned rows keep their pre-019 root_id';
COMMENT ON COLUMN quotas.period_unit       IS 'time unit of the rate window, lowercase singular. NULL means the limit is a count rather than a rate';
COMMENT ON COLUMN quotas.period_value      IS 'how many period_units the window spans. NULL alongside a NULL period_unit';
COMMENT ON COLUMN quotas.resource_type     IS 'FOCUS ResourceType. what a resource-scoped limit counts, e.g. AWS::Connect::Instance. NULL means the limit is not resource-scoped, which is how FOCUS spells it';
COMMENT ON COLUMN quotas.availability_zone IS 'FOCUS AvailabilityZone. the zone a zone-scoped limit applies in, NULL when the limit is not zone-scoped. denormalized out of dimension_key, which already carries the zone as part of the key, so that a zone is filterable without parsing an opaque value';
COMMENT ON COLUMN quotas.sub_account_type  IS 'FOCUS SubAccountType. what kind of container account_id names: Account, Subscription, Project. display-only, so it never splits a version chain';
