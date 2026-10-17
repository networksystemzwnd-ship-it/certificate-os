# BACKLOG

Template: see `PROJECT_BRAIN.md` §12.5.

### COS-001 — Repository skeleton and agent instructions
- Status: Review
- Priority: P0
- Acceptance: [x] root docs, Go module, React app, lint/format commands, `.env.example`; [x] no secrets committed

### COS-002 — Docker Compose local environment
- Status: Review (needs real `docker compose up` verification on a Docker host)
- Priority: P0

### COS-003 — CI baseline
- Status: Review (workflow written; needs first run on GitHub)
- Priority: P0

### COS-004 — PostgreSQL migration framework
- Status: Blocked (code written; not compiled — pgx could not be downloaded in the build sandbox; run `go get` + `go test` with TEST_DATABASE_URL)
- Priority: P0

### COS-005 — API foundation
- Status: Review (config/httpx/health tests pass; cmd/api and migrate need pgx to compile)
- Priority: P0

### COS-006 — Frontend shell
- Status: Blocked (source and tests written; never installed/built — npm unreachable in sandbox)
- Priority: P0

### COS-007 — Identity and authorization design
- Status: Review
- Priority: P0
- Goal: authentication + capability-based permissions; session strategy documented (D-107..D-111).
- Acceptance:
  - [x] unauthenticated requests cannot reach protected endpoints (tested)
  - [x] a user cannot grant themselves a role (unknown-field rejection + no role endpoint; tested)
  - [x] suspended users lose live sessions immediately (tested at service and HTTP level)
  - [x] auth/role-denial tests (platform ops have no student/certificate permissions; unknown roles fail closed)
  - [x] login rate limiting; generic login errors; CSRF; strict CORS
  - [ ] pgstore + migration 0002 + CLI verified against real Postgres (needs pgx; run `go test ./...` with TEST_DATABASE_URL)
  - [ ] frontend tests run (needs npm install)
- Not included (P1): password reset, email verification, MFA, session listing.

### COS-008 — Tenant context and isolation
- Status: Review
- Priority: P0
- Goal: tenant context from authenticated membership; cross-tenant negative tests pass before business modules expand.
- Acceptance:
  - [x] tenant derived from membership, never from a client ID alone (D-113; tested)
  - [x] cross-tenant reads denied for institute, member list and by header/ID guessing; foreign vs non-existent indistinguishable (tested)
  - [x] role boundaries inside a tenant (owner/admin/staff/grant); platform ops, removed and closed members denied (tested)
  - [x] suspended institute is read-only, effective on the next request; removal effective immediately (tested)
  - [x] tests proven to fail when resolution or suspension rules are broken (mutation check)
  - [x] reusable `tenancytest.Fixture` for every future resource family
  - [ ] pg store + migration 0003 + CLI verified on real Postgres (type-checked only; run `go test ./...` with TEST_DATABASE_URL)
  - [ ] frontend tests run (needs npm install)
- Decisions: D-114 confirmed by product owner (suspended = read-only, exports blocked); ADR-001 Accepted.
- Not included: invitations, role changes, institute profile edits (M3); RLS (ADR-001).
### COS-RLS — Row-level security + restricted runtime role — Proposed (BLOCKS M3)
- Priority: P0
- Goal: implement ADR-001's production gate with the first tenant-owned table; no tenant-owned table merges without it.
- Acceptance:
  - [ ] restricted non-superuser runtime role used by API/worker; owner role only for migrations
  - [ ] ENABLE + FORCE RLS and fail-closed policies on every tenant-owned table
  - [ ] all tenant repositories run inside `tenantdb.WithTenant`
  - [ ] integration test: scope A cannot read/write B even with the application filter removed
  - [ ] runtime role cannot UPDATE/DELETE/TRUNCATE `audit_logs`

### COS-009 — Institute application schema, state machine and audit log — Built (needs your verification)
- Priority: P0
- Delivered: migrations 0004 (audit_logs, append-only) and 0005 (institute_applications); `applications` domain + service; memory and Postgres stores sharing one contract; applicant API (`/api/v1/institute-applications`); audit events for every change; CLI actions audited.
- Acceptance:
  - [x] state machine rejects illegal transitions; one open application per user; slug reservation (store contract, memory store, `-race`)
  - [x] approval is atomic (institute + owner membership + audit); failed approval changes nothing (contract)
  - [x] migrations 0001–0005 apply, revert and re-apply on real PostgreSQL 16; constraints, unique indexes and append-only triggers exercised with hand-run SQL equivalent to the store queries
  - [ ] Go pg stores run via `TEST_DATABASE_URL=... go test ./...` (pgx could not be installed in the build sandbox; the code type-checks against a stub only)
  - [ ] frontend type-check/lint/tests (npm unavailable in the build sandbox)
### COS-010 — Operations review API and UI — Built (needs your verification)
- Priority: P0
- Delivered: ops queue/detail/history, start-review, approve, reject, request-changes (`/api/v1/ops/institute-applications`); institute list, suspend, reactivate (`/api/v1/ops/institutes`); audit view (`/api/v1/ops/audit`); React pages under `/apply` and `/ops`.
- Acceptance:
  - [x] only ops/super_admin reach ops routes; plain users get 403, anonymous 401; audit view is super_admin only
  - [x] ops never see drafts; self-review blocked; reasons validated; double approval is 409
  - [ ] same unverified items as COS-009 (pg tests, frontend build)
### COS-011 — Applicant document upload — Proposed (deferred, see D-123)
- Needs: object-store adapter, size/type limits, malware-scan decision, retention policy.
