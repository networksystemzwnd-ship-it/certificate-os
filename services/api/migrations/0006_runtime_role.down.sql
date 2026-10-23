-- The role itself is cluster-wide (shared by other databases) and is intentionally NOT dropped.
REVOKE ALL ON users, sessions, institutes, institute_memberships, institute_applications, audit_logs FROM certos_runtime;
REVOKE USAGE ON SCHEMA public FROM certos_runtime;
