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

### COS-007 — Identity and authorization design — Ready
### COS-008 — Tenant context and isolation — Proposed
### COS-009 — Institute application schema and workflow — Proposed
### COS-010 — Operations review UI/API — Proposed
