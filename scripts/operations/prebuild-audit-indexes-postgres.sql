-- Run with psql -X -v ON_ERROR_STOP=1 -d logdb -f this-file.sql.
-- Do not use --single-transaction, BEGIN, or a GUI transaction wrapper.
-- Review docs/design/online-ddl-prebuild-20261117.md before production use.
\set ON_ERROR_STOP on
\timing on

SELECT current_database(), version();
SET application_name = 'audit-index-prebuild';
SET statement_timeout = '30min';
SET lock_timeout = '500ms';

-- Stop before making changes if an existing name has a different definition
-- or is an INVALID index left by a failed concurrent build.
DO $$
DECLARE
    expected record;
    existing_oid oid;
    matches boolean;
BEGIN
    IF to_regclass('public.audit_logs') IS NULL THEN
        RAISE EXCEPTION 'public.audit_logs does not exist; check the selected log database';
    END IF;
    IF EXISTS (
        SELECT 1 FROM pg_attribute
        WHERE attrelid = 'public.audit_logs'::regclass
          AND attname = 'actor_is_employee' AND NOT attisdropped
          AND (atttypid <> 'boolean'::regtype OR attnotnull OR atthasdef)
    ) THEN
        RAISE EXCEPTION 'actor_is_employee differs from nullable boolean without default; inspect manually';
    END IF;
    FOR expected IN
        SELECT * FROM (VALUES
            ('idx_audit_actor_time', 'actor_role,created_at,event_id'),
            ('idx_audit_employee_time', 'actor_role,actor_is_employee,created_at,event_id'),
            ('idx_audit_auth_time', 'auth_method,created_at,event_id'),
            ('idx_audit_route_time', 'route,created_at,event_id,actor_role'),
            ('idx_audit_ip_time', 'ip,created_at,event_id'),
            ('idx_audit_customer_cursor', 'user_id,created_at,event_id'),
            ('idx_audit_user_route', 'user_id,route,created_at,actor_role')
        ) AS wanted(name, columns)
    LOOP
        existing_oid := to_regclass('public.' || expected.name);
        IF existing_oid IS NOT NULL THEN
            SELECT i.indrelid = 'public.audit_logs'::regclass
                AND i.indisvalid AND i.indisready AND NOT i.indisunique
                AND NOT i.indisprimary AND i.indpred IS NULL
                AND i.indexprs IS NULL AND am.amname = 'btree'
                AND (SELECT string_agg(pg_get_indexdef(i.indexrelid, n, true), ',' ORDER BY n)
                     FROM generate_series(1, i.indnatts) n) = expected.columns
            INTO matches
            FROM pg_index i
            JOIN pg_class c ON c.oid = i.indexrelid
            JOIN pg_am am ON am.oid = c.relam
            WHERE i.indexrelid = existing_oid;
            IF matches IS DISTINCT FROM true THEN
                RAISE EXCEPTION '% exists but is invalid or has a different definition; stop and inspect', expected.name;
            END IF;
        END IF;
    END LOOP;
END $$;

-- No default, NOT NULL constraint, or historical backfill. Skip the ALTER
-- altogether on a second run, so it does not acquire an unnecessary table lock.
SELECT 'ALTER TABLE public.audit_logs ADD COLUMN actor_is_employee boolean;'
WHERE NOT EXISTS (
    SELECT 1 FROM pg_attribute
    WHERE attrelid = 'public.audit_logs'::regclass
      AND attname = 'actor_is_employee' AND NOT attisdropped
)
\gexec

SET lock_timeout = '2s';
-- gexec executes each missing index separately, in autocommit, in this order.
SELECT format('CREATE INDEX CONCURRENTLY %I ON public.audit_logs (%s);', name, columns)
FROM (VALUES
    (1, 'idx_audit_actor_time', 'actor_role,created_at,event_id'),
    (2, 'idx_audit_employee_time', 'actor_role,actor_is_employee,created_at,event_id'),
    (3, 'idx_audit_auth_time', 'auth_method,created_at,event_id'),
    (4, 'idx_audit_route_time', 'route,created_at,event_id,actor_role'),
    (5, 'idx_audit_ip_time', 'ip,created_at,event_id'),
    (6, 'idx_audit_customer_cursor', 'user_id,created_at,event_id'),
    (7, 'idx_audit_user_route', 'user_id,route,created_at,actor_role')
) AS wanted(sequence, name, columns)
WHERE to_regclass('public.' || name) IS NULL
ORDER BY sequence
\gexec

DO $$
BEGIN
    IF (SELECT count(*) FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
        WHERE i.indrelid = 'public.audit_logs'::regclass AND i.indisvalid AND i.indisready
          AND c.relname IN ('idx_audit_actor_time', 'idx_audit_employee_time', 'idx_audit_auth_time',
                           'idx_audit_route_time', 'idx_audit_ip_time', 'idx_audit_customer_cursor',
                           'idx_audit_user_route')) <> 7 THEN
        RAISE EXCEPTION 'Not all seven audit indexes are valid and ready';
    END IF;
END $$;

SELECT c.relname, i.indisvalid, i.indisready, pg_get_indexdef(i.indexrelid)
FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
WHERE i.indrelid = 'public.audit_logs'::regclass AND c.relname IN (
    'idx_audit_actor_time', 'idx_audit_employee_time', 'idx_audit_auth_time',
    'idx_audit_route_time', 'idx_audit_ip_time', 'idx_audit_customer_cursor', 'idx_audit_user_route'
)
ORDER BY c.relname;
