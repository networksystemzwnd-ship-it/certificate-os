# Changelog

## [Unreleased]
### Added
- Institute onboarding (COS-009/010): application workflow (draft → submitted → under review → approved/rejected/changes requested) with atomic approval, ops review API/UI, ops institute suspend/reactivate, super_admin audit view, append-only audit log (migrations 0004, 0005), audited CLI actions, `/apply` and `/ops` pages.
- Tenancy (COS-008): institutes + memberships (migration 0003), tenant resolution from membership (`X-Institute-ID`), compile-time `tenancy.Scope`, suspended-institute read-only rule, per-membership grants, `GET /api/v1/me/institutes`, `/institute`, `/institute/members`, `api institute ...` CLI, two-tenant seed data, `tenancytest` fixture, institute picker in the web app.
- Identity (COS-007): register/login/logout/me, cookie sessions, capability permissions, CSRF + CORS, login rate limits, `api seed` and `api user ...` CLI, migration 0002 (users, sessions), frontend login/register and route guards.
- Project foundation (M0): repo skeleton, Go API (config, structured logs, request IDs, health/readiness, error envelope), migration runner, React/Vite shell, Docker Compose, CI.
