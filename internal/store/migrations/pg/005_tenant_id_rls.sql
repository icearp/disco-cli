-- Tenant scoping. Every user-data row carries a tenant_id; per-table RLS
-- policies filter on `current_setting('app.tenant_id')::uuid`. The GUC is
-- pinned at connection time by OpenPostgres (postgres_paid.go) via
-- AfterConnect, so RLS sees the value on every query without per-statement
-- plumbing. NEW rows pick up the GUC by DEFAULT; INSERTs from app code
-- never need to spell tenant_id explicitly.
--
-- Rationale: makes cross-tenant leakage impossible at the storage layer,
-- not just by convention. SaaS reads via its own pool with the same
-- AfterConnect hook; disco serve sets the same GUC for scan writes.
--
-- Keep in lockstep with SQLite migrations: a new column on a SQLite table
-- needs the matching change in this file's table set, and `make
-- check-migrations` enforces parity (with tenant_id on the ignore list).

ALTER TABLE resources         ADD COLUMN tenant_id UUID NOT NULL DEFAULT current_setting('app.tenant_id')::uuid;
ALTER TABLE relationships     ADD COLUMN tenant_id UUID NOT NULL DEFAULT current_setting('app.tenant_id')::uuid;
ALTER TABLE hierarchy_closure ADD COLUMN tenant_id UUID NOT NULL DEFAULT current_setting('app.tenant_id')::uuid;
ALTER TABLE scans             ADD COLUMN tenant_id UUID NOT NULL DEFAULT current_setting('app.tenant_id')::uuid;
ALTER TABLE scan_checkpoints  ADD COLUMN tenant_id UUID NOT NULL DEFAULT current_setting('app.tenant_id')::uuid;
ALTER TABLE check_runs        ADD COLUMN tenant_id UUID NOT NULL DEFAULT current_setting('app.tenant_id')::uuid;
ALTER TABLE findings          ADD COLUMN tenant_id UUID NOT NULL DEFAULT current_setting('app.tenant_id')::uuid;

CREATE INDEX IF NOT EXISTS idx_resources_tenant         ON resources(tenant_id);
CREATE INDEX IF NOT EXISTS idx_relationships_tenant     ON relationships(tenant_id);
CREATE INDEX IF NOT EXISTS idx_hierarchy_closure_tenant ON hierarchy_closure(tenant_id);
CREATE INDEX IF NOT EXISTS idx_scans_tenant             ON scans(tenant_id);
CREATE INDEX IF NOT EXISTS idx_scan_checkpoints_tenant  ON scan_checkpoints(tenant_id);
CREATE INDEX IF NOT EXISTS idx_check_runs_tenant        ON check_runs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_findings_tenant          ON findings(tenant_id);

ALTER TABLE resources         ENABLE ROW LEVEL SECURITY;
ALTER TABLE relationships     ENABLE ROW LEVEL SECURITY;
ALTER TABLE hierarchy_closure ENABLE ROW LEVEL SECURITY;
ALTER TABLE scans             ENABLE ROW LEVEL SECURITY;
ALTER TABLE scan_checkpoints  ENABLE ROW LEVEL SECURITY;
ALTER TABLE check_runs        ENABLE ROW LEVEL SECURITY;
ALTER TABLE findings          ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON resources
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

CREATE POLICY tenant_isolation ON relationships
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

CREATE POLICY tenant_isolation ON hierarchy_closure
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

CREATE POLICY tenant_isolation ON scans
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

CREATE POLICY tenant_isolation ON scan_checkpoints
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

CREATE POLICY tenant_isolation ON check_runs
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

CREATE POLICY tenant_isolation ON findings
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
