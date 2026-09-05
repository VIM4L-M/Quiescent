DROP TABLE IF EXISTS audit_log, outbox, leases, attempts, mandate_cycles;

DROP FUNCTION IF EXISTS seed_lease();
DROP FUNCTION IF EXISTS touch_updated_at();
