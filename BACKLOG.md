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
- Decisions to confirm: D-114 (suspended = read-only), ADR-001 (RLS timing).
- Not included: invitations, role changes, institute profile edits (M3); audit log (COS-009); RLS (ADR-001).
### COS-009 — Institute application schema and workflow — Proposed
### COS-010 — Operations review UI/API — Proposed
