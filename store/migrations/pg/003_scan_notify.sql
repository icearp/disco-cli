-- Replace external polling of scans.finished_at with a per-tenant trigger
-- that pg_notifies on every terminal scan transition. Channel is global per
-- database (NOTIFY is not schema-scoped), so a single LISTEN in the
-- consuming daemon receives events from every tenant schema. Payload carries both
-- tenant_id (the schema selector) and workspace_id (the row-level scope) so
-- the listener routes to the right workspace without a secondary lookup —
-- a single tenant schema now holds many workspaces' scans.
--
-- Channel: disco_scan_status
-- Payload: {"scan_id":"...","status":"...","tenant_id":"...","workspace_id":"..."}

CREATE OR REPLACE FUNCTION notify_scan_status() RETURNS TRIGGER AS $fn$
BEGIN
    IF NEW.status IN ('completed', 'failed', 'partial') THEN
        PERFORM pg_notify(
            'disco_scan_status',
            json_build_object(
                'scan_id',      NEW.id,
                'status',       NEW.status,
                'tenant_id',    NEW.tenant_id,
                'workspace_id', NEW.workspace_id
            )::text
        );
    END IF;
    RETURN NEW;
END;
$fn$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS scans_notify_status ON scans;
CREATE TRIGGER scans_notify_status
    AFTER INSERT OR UPDATE OF status ON scans
    FOR EACH ROW EXECUTE FUNCTION notify_scan_status();
