-- 0006_runtime_role (COS-RLS, ADR-001): least-privilege role for the API and worker.
--
-- `certos_runtime` is a NOLOGIN group role that carries every privilege the application needs.
-- A login role (e.g. certos_app) is created out-of-band (`api db provision-runtime-role`) and made a
-- member, so the password never appears in a migration. Migrations keep running as the owner.
--
-- Rules for later migrations (checked by TestEveryTableIsClassified):
--  * every new table must GRANT only what the application uses to certos_runtime (no default privileges:
--    a forgotten grant fails loudly with "permission denied" instead of silently over-granting);
--  * every tenant-owned table (has institute_id) must ENABLE + FORCE ROW LEVEL SECURITY with a policy.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'certos_runtime') THEN
        CREATE ROLE certos_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
    END IF;
END
$$;

REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO certos_runtime;

-- Global (non-tenant-owned) identity and onboarding tables. No DELETE: nothing in the app deletes rows.
GRANT SELECT, INSERT, UPDATE ON users, sessions, institutes, institute_memberships, institute_applications TO certos_runtime;
-- Append-only audit trail: the runtime role can add and read, never change or remove (D-121).
-- schema_migrations is owner-only by default: no grant is made, so the runtime role cannot touch it.
GRANT SELECT, INSERT ON audit_logs TO certos_runtime;
REVOKE UPDATE, DELETE, TRUNCATE ON audit_logs FROM certos_runtime;
