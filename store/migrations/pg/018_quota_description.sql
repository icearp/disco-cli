-- Add the provider's own prose description of a quota.
--
-- 017 omitted this column on the reasoning that neither provider supplies one.
-- That is wrong for AWS: sqtypes.ServiceQuota carries Description, and a survey
-- of 91,126 stored quota rows found it populated on every single one ("The
-- maximum number of plans for each AWS account."). It is the one field that
-- says what a limit actually governs, which no combination of service code,
-- quota code and unit conveys.
--
-- Azure's armquota reports no equivalent, so this stays NULL there.
--
-- It is display-only prose and must NOT participate in change detection: a
-- provider rewording a description would otherwise split every chain in the
-- catalogue at once. UpsertQuotas updates it in place alongside name and
-- service_name for exactly that reason.
--
-- 017 is already tagged and published, so this arrives as its own version
-- rather than as an edit -- a recorded version is skipped by the runner, which
-- would leave any schema that already applied 017 silently without the column.

ALTER TABLE quotas ADD COLUMN IF NOT EXISTS description TEXT;

COMMENT ON COLUMN quotas.description IS 'the provider''s prose description of what this limit governs. NULL where the provider reports none, as Azure does. display-only, so it never splits a version chain';
