# Changelog

## [Unreleased]
### Added
- Certificate templates (M4a): presets (classic, modern, minimal) with fixed configurable fields, immutable versions (database-enforced), archive/restore, safe server-side preview from sample data shown in a sandboxed iframe, REST API, audit events, migration 0010, "Templates" tab (read for staff, edit for admin/owner). No PDF yet and no new dependencies.
- Staff management (M3c): owner invites admin/staff by one-time link (7 days, token hash only, email must match), accept-invite page that survives login/registration, change role/grants, remove member, revoke invitation; migration 0009; Staff tab (owner only); members list now shows emails. No new dependencies. No email is sent yet (D-138).
- Student import (M3b): upload a CSV, map columns, preview row-level problems, confirm; duplicates are skipped and never overwritten; downloadable error report; enrolment with status, completion date and grade; migration 0008; "Import students" tab (admin/owner). No new dependencies (stdlib `encoding/csv`).
- Records (M3a): courses, batches, students and enrollments (migration 0007) with row-level security, archive/restore (no hard delete), search + pagination, enrollment status/completion date/grade, REST API with permission guards and audit events (ids and field names only), and Students/Courses/Batches tabs in the workspace.
- Row-level security (COS-RLS, ADR-001): restricted `certos_runtime` role (migration 0006), `api db provision-runtime-role`, separate owner connection for migrations (`MIGRATE_DATABASE_URL`), production startup guard, table-classification test. No new dependencies.
### Changed
- Go tests run with `-p 1` (CI, `make test`) because Postgres-backed packages share one test database.
- API client sends `FormData` bodies without forcing a JSON content type.
- `.env.example`: `DATABASE_URL` now names the restricted role; new `MIGRATE_DATABASE_URL`, `APP_DB_USER`, `APP_DB_PASSWORD`. Existing local databases: `docker compose down -v` once, or run `api db provision-runtime-role`.
- Institute onboarding (COS-009/010): application workflow (draft → submitted → under review → approved/rejected/changes requested) with atomic approval, ops review API/UI, ops institute suspend/reactivate, super_admin audit view, append-only audit log (migrations 0004, 0005), audited CLI actions, `/apply` and `/ops` pages.
- Tenancy (COS-008): institutes + memberships (migration 0003), tenant resolution from membership (`X-Institute-ID`), compile-time `tenancy.Scope`, suspended-institute read-only rule, per-membership grants, `GET /api/v1/me/institutes`, `/institute`, `/institute/members`, `api institute ...` CLI, two-tenant seed data, `tenancytest` fixture, institute picker in the web app.
- Identity (COS-007): register/login/logout/me, cookie sessions, capability permissions, CSRF + CORS, login rate limits, `api seed` and `api user ...` CLI, migration 0002 (users, sessions), frontend login/register and route guards.
- Project foundation (M0): repo skeleton, Go API (config, structured logs, request IDs, health/readiness, error envelope), migration runner, React/Vite shell, Docker Compose, CI.
