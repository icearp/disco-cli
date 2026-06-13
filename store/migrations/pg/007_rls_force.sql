-- Force row-level security on every tenant-scoped table. ENABLE ROW LEVEL
-- SECURITY (migrations 001 + 002) applies the tenant_isolation policy to
-- ordinary roles, but the table OWNER keeps an implicit bypass unless RLS is
-- FORCEd. A multi-tenant deployment that ever connects to a tenant schema as
-- the owning role would otherwise read across tenants. FORCE closes that gap;
-- it is defense-in-depth on top of the app.tenant_id / app.workspace_id GUC +
-- USING/WITH CHECK policies, which are unchanged. (Superusers and roles with
-- the BYPASSRLS attribute still bypass — that is a deliberate Postgres
-- guarantee and is out of scope for RLS.)
ALTER TABLE resources         FORCE ROW LEVEL SECURITY;
ALTER TABLE relationships     FORCE ROW LEVEL SECURITY;
ALTER TABLE hierarchy_closure FORCE ROW LEVEL SECURITY;
ALTER TABLE scans             FORCE ROW LEVEL SECURITY;
ALTER TABLE scan_checkpoints  FORCE ROW LEVEL SECURITY;
ALTER TABLE check_runs        FORCE ROW LEVEL SECURITY;
ALTER TABLE findings          FORCE ROW LEVEL SECURITY;
