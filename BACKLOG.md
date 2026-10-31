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
### COS-RLS — Row-level security + restricted runtime role — Built (needs your Postgres verification)
- Priority: P0
- Delivered: migration 0006 (`certos_runtime` group role + least-privilege grants), `api db provision-runtime-role`, owner vs runtime connections (`MIGRATE_DATABASE_URL` / `DATABASE_URL`), startup guard (production refuses a superuser/BYPASSRLS runtime role), `tenantdb.WithTenant`, table-classification test (`TestEveryTableIsClassified`) so a new table cannot ship without RLS or an explicit global-table entry.
- Acceptance:
  - [x] restricted non-superuser runtime role used by API; owner role only for migrations (code + startup guard)
  - [x] ENABLE + FORCE RLS and fail-closed policies on every tenant-owned table (migration 0007; verified with hand-run SQL on real PostgreSQL 16: unfiltered reads/updates confined, cross-institute insert rejected by policy, cross-tenant FK rejected, no setting => zero rows, DELETE denied)
  - [x] all tenant repositories run inside `tenantdb.WithTenant`
  - [ ] Go integration test "filter deliberately removed" (`TEST_DATABASE_URL` + `TEST_APP_DATABASE_URL`) — written, not run here (pgx unavailable in the build sandbox); run `go test ./...` with both set
  - [x] runtime role cannot UPDATE/DELETE/TRUNCATE `audit_logs` (real PostgreSQL 16)

### M3a — Courses, batches, students, enrollments — Built (needs your verification)
- Priority: P0
- Delivered: migration 0007; `records` domain, service, memory + Postgres stores sharing one contract; REST API under `/api/v1/{courses,batches,students}` with permission guards, audit events and cross-tenant HTTP tests; React Students / Courses / Batches tabs in the institute workspace.
- Acceptance:
  - [x] store contract (CRUD, search, pagination, archive rules, enrollments, isolation) passes on the memory store under `-race`
  - [x] HTTP tests: 401/403, read-only roles, suspended institute, cross-tenant 404s, audit contains no personal data
  - [ ] Postgres store contract run against a real database (`go test ./...` with `TEST_DATABASE_URL`, `TEST_APP_DATABASE_URL`)
  - [ ] frontend `npm test`, `npm run lint`, `npm run typecheck` (needs npm install; never run in the build sandbox)
- Decision awaiting you: D-125 (completion date and grade live on the enrollment).
- Not included: CSV/XLSX import and export, staff invitations (rest of M3).

### M3b — Student import (CSV) — Built (needs your verification)
- Priority: P0
- Delivered: migration 0008 (`student_imports`, RLS); `imports` package (CSV parsing, column mapping, row validation, duplicate rules, confirm with progress/resume, error report); `GET/POST/PUT/DELETE /api/v1/students/imports...`; import wizard in the workspace ("Import students" tab, admin/owner).
- Acceptance:
  - [x] a realistic spreadsheet imports safely: row-level errors, duplicates skipped, explicit date format, enrolment with status/date/grade (service + HTTP tests, `-race`)
  - [x] nothing written before confirm; confirm twice/concurrently refused; interrupted confirm resumes without duplicates
  - [x] cross-tenant: other institutes get 404/403 on every import route; student numbers of one institute never make another's rows duplicates
  - [x] migration 0008 SQL (insert/update/RLS/CHECK) verified on real PostgreSQL 16
  - [ ] Postgres import store contract and `go test -p 1 ./...` with `TEST_DATABASE_URL` + `TEST_APP_DATABASE_URL` (not run here)
  - [ ] frontend tests/lint/typecheck (`npm ci`; never run in the build sandbox)
- Not included: XLSX (D-130), export of students to CSV, scheduled purge of abandoned uploads, merge workflow.

### M3c — Staff invitations and member management — Built (needs your verification)
- Priority: P0
- Delivered: migration 0009 (`institute_invitations`, global table, D-137); `staff` package (invite, list, revoke, accept, change member, remove member) with memory and Postgres stores sharing one contract; owner-only Staff tab; `/accept-invite` page with login/registration detour; audit events `invitation.created/revoked/accepted`, `member.role_changed/removed` (ids, role names and grants only, never emails or tokens).
- Acceptance:
  - [x] token never stored or logged (hash only) and never listed; shown once; link works only for the invited email on an active institute; every refusal identical (HTTP tests)
  - [x] owners/self protected, roles limited, grants validated, pending limit, expiry replacement, reactivation (contract + HTTP tests, `-race`; mutation-checked)
  - [x] cross-tenant: other institutes get 404/403 on invitation and member routes
  - [x] migration 0009 constraints verified on real PostgreSQL 16 (unique pending email, unique hash, role/grant/state CHECKs, runtime-role privileges)
  - [ ] Postgres staff store contract with `TEST_DATABASE_URL` + `TEST_APP_DATABASE_URL` (type-checked against a stub only; never run here)
  - [ ] frontend tests/lint/typecheck (`npm ci`; never run in the build sandbox)
- Not included: sending the invitation by email (needs the notification worker), resending (revoke and create again), transferring ownership, institute profile edits.

### M4a — Certificate templates, versions and preview — Built (needs your verification)
- Priority: P0
- Delivered: migration 0010 (`certificate_templates`, `certificate_template_versions`, RLS, insert-only versions with an immutability trigger); `templates` package (validation, closed placeholder list, escaped renderer with CSP, service, REST API, memory and Postgres stores sharing one contract); audit events `template.created/renamed/version_created/archived/restored` (ids, preset and version number only); Templates tab with create-from-preset, live preview in a sandboxed iframe, new-version editing, version list, archive/restore.
- Acceptance:
  - [x] template text cannot inject markup, script or external resources (renderer tests inspect every emitted tag; mutation-checked)
  - [x] versions never change; edit creates a new version; archived templates take no new versions (contract + HTTP tests)
  - [x] staff read/preview only, admin/owner write, suspended institute read-only, other institutes get 404/403 on every route (HTTP tests, `-race`)
  - [x] migration 0010 verified on real PostgreSQL 16 (immutability trigger, composite FK, RLS fail-closed and cross-tenant, runtime role cannot delete or alter versions)
  - [ ] Postgres template store contract with `TEST_DATABASE_URL` + `TEST_APP_DATABASE_URL` (type-checked against a stub only; never run here)
  - [ ] frontend tests/lint/typecheck (`npm ci`; never run in the build sandbox)
- Not included: logo/signature images (needs COS-011 uploads), PDF rendering, real QR, certificate numbers, issuing (M4b+). Open decisions for M4b: PDF renderer (brain suggests a pinned Chromium) and the certificate number format/scope.

### M4b — Certificate issuing (single), PDF, QR, download, revoke — Built (needs your verification)
- Priority: P0
- Delivered: migration 0011 (`certificate_counters`, `certificates`, `certificate_files`: RLS, immutability triggers, one live certificate per enrollment); `certificates` package (service, REST API, memory and Postgres stores sharing one contract); `pdf` package (pinned-Chromium renderer with hardening, fake for tests); `qr` package (own encoder); config `PUBLIC_BASE_URL`, `PDF_CHROME_*`; Certificates tab (issue from a completed enrollment, list/search, download, revoke); audit events `certificate.issue_started/issued/issue_failed/revoked` (ids, numbers, template versions only).
- Acceptance:
  - [x] a real Chromium run produced one A4 page whose text matches the snapshot (name, course, number, date) and whose QR code decoded to `PUBLIC_BASE_URL/verify/<43-char id>` (checked with pdftotext, pdfinfo and OpenCV)
  - [x] QR encoder decodes correctly in an independent decoder for 55 payloads across versions 1-10
  - [x] never issued without a stored PDF: render failure leaves a `failed` record, no renderer configured returns 503 and writes nothing (HTTP tests)
  - [x] numbers sequential per institute/year, never reused, no gaps from refused attempts; double issue refused; stale pending abandoned (contract tests)
  - [x] staff read/download only, issue needs the issue permission, revoke admin/owner, suspended institute read-only, other institutes get 404/403 on every route (HTTP tests, `-race`; mutation-checked)
  - [x] migration 0011 and every store query verified on real PostgreSQL 16 as the restricted runtime role (immutable snapshot/number/PDF, no deletes, RLS fail-closed, counter upsert)
  - [ ] Postgres certificate store contract with `TEST_DATABASE_URL` + `TEST_APP_DATABASE_URL` (type-checked against a stub only; never run here)
  - [ ] frontend tests/lint/typecheck (`npm ci`; never run in the build sandbox)
  - [ ] Docker image with Chromium (the Dockerfile is yours; see the runbook) and an issue/download check in your environment
- Not included: bulk jobs and ZIP export (M4c), logo/signature images (COS-011), public verification page and replace flow (M5), email delivery (M5). Open: D-151 (who may download).

### M5a — Replace flow and public verification — Built (needs your verification)
- Priority: P0
- Delivered: migration 0013 (replacement links, one-valid-certificate index that exempts only a pending replacement, trigger that lets the link be written once while revoking, narrow public-lookup policy, `verification_events`); `POST /certificates/{id}/replace`; `verification` package with `GET /api/v1/public/certificates/{public_id}` and the public page `/verify/:publicId` (valid / revoked / replaced / not found, no login, no workspace navigation); "Replace" action in the Certificates tab; decisions D-160..D-164; OpenAPI 0.11.0.
- Acceptance:
  - [x] valid / revoked / replaced are reported accurately, the replacement verifies as valid (HTTP tests; mutation-checked)
  - [x] public response has exactly the approved fields; no grade, contact data, internal ids, reasons or PDF; no-store, noindex
  - [x] malformed, unknown, pending and failed ids return the identical 404; the certificate number is not a credential
  - [x] rate limited per address (429 + Retry-After); events recorded without IP or user agent
  - [x] replacement: old certificate stays valid until the new PDF is stored, then revoked and linked in one step; failed render or concurrent revoke leaves exactly one valid certificate; one replacement at a time; stale pending replacement abandoned; other institutes get 404 (shared store contract, memory store, `-race`)
  - [x] migration 0013 verified on real PostgreSQL 16 as the restricted role: one-valid-certificate and one-pending-replacement indexes, trigger refuses re-linking and tampering, public policy exposes only the presented id and is read-only, other tables stay closed, events insert-only, down/up cycle
  - [ ] Postgres stores through real pgx: certificate contract (now with replacement) and `verification/pgstore` test with `TEST_DATABASE_URL` + `TEST_APP_DATABASE_URL` (type-checked against a stub only)
  - [ ] frontend tests/lint/typecheck (never run in the build sandbox; syntax check only)
  - [ ] scan a printed/rendered QR with a phone against your real `PUBLIC_BASE_URL` (the QR payload was decoded independently in M4b)
- Open for you: D-160 (public fields), D-161 (no public PDF download).
- Not included: email delivery with attempts and retry (M5b), institute-configurable public fields (P1), a shared rate limiter for several API replicas (D-108).

### M5b — Email delivery — Built (needs your verification)
- Priority: P0
- Delivered: migration 0014 (`deliveries`, `delivery_attempts`, PII-free `delivery_queue` with a narrow worker-scan policy, guard trigger, no deletes); `delivery` package: provider-independent `mail.Mailer` with a standard-library SMTP adapter (STARTTLS / implicit TLS / none for local use, PLAIN auth, MIME with PDF attachment, header-injection safe) and a fake; store (memory + Postgres, one shared contract); service and background worker (claim with token and lease, backoff, bounded attempts, manual retry, suspended institutes wait); routes `GET /email/status`, `POST/GET /certificates/{id}/deliveries`, `GET /deliveries`, `GET /deliveries/{id}`, `POST /deliveries/{id}/retry`, `POST /bulk-jobs/{id}/deliveries`; config `EMAIL_PROVIDER`, `EMAIL_FROM`, `SMTP_*`; UI: "Email" button per certificate, "Email deliveries" panel with status and Retry, "Email all" for a bulk job; decisions D-165..D-171; OpenAPI 0.12.0.
- Acceptance:
  - [x] issuing is independent of delivery; revoked/replaced certificates are never mailed (service tests, mutation-checked)
  - [x] statuses never claim "delivered"; only `accepted` after the server accepts the message
  - [x] transient failures retry with backoff, permanent ones stop at once, attempts are bounded, manual retry works with the current address, shutdown does not burn an attempt
  - [x] error text and addresses never reach the database, the audit log or API responses (recipients masked)
  - [x] SMTP adapter verified against an in-process SMTP server: plain, STARTTLS with verified certificate, implicit TLS, authentication, STARTTLS-required never falls back to clear text, untrusted certificate refused, 4xx/5xx/timeout/refused classification
  - [x] permission matrix (staff and the issue grant cannot send; admin/owner can), suspended institute read-only, institute isolation, bulk "email all" never mails anyone twice (HTTP tests, mutation-checked)
  - [x] migration 0014 and the store's SQL verified on real PostgreSQL 16 as the restricted role (RLS, worker scan sees ids only, one active delivery per certificate, state-machine trigger, accepted is final, no DELETE, down/up cycle)
  - [ ] Postgres delivery store through real pgx: `delivery/pgstore` contract test with `TEST_DATABASE_URL` + `TEST_APP_DATABASE_URL` (type-checked against a stub only)
  - [ ] frontend tests/lint/typecheck (never run in the build sandbox; syntax check only)
  - [ ] a REAL send through your provider (or Mailpit): not possible in the build sandbox. Follow `docs/runbooks/email-delivery.md`.
- Open for you: provider choice (brain §17 Q9), D-167 (at-least-once sending is acceptable?).
- Not included: bounce / delivery-receipt handling (needs a provider), WhatsApp/SMS (post-MVP), a separate worker process (D-155), email templates per institute.

### M6a — Dashboards — Built (needs your verification)
- Priority: P0
- Delivered: `dashboard` package (`GET /api/v1/dashboard`, `GET /api/v1/ops/dashboard`), Postgres queries, UI: Overview tab (now the first tab of the workspace) and Operations > Overview; decisions D-172, D-173; OpenAPI 0.13.0.
- Acceptance:
  - [x] institute figures are scoped to the caller's institute, sections follow permissions, student names only in the recent list (HTTP tests, mutation-checked)
  - [x] platform overview limited to platform staff and contains no personal data; period validated
  - [x] SQL verified on real PostgreSQL 16 as the restricted role (foreign tenant sees zero, platform queries read only global tables)
  - [ ] `dashboard/pgstore` through real pgx (type-checked against a stub only; there is no store contract because the figures span other packages' tables)
  - [ ] frontend tests/lint/typecheck (never run in the build sandbox)
- Not included: charts/time series, exports, per-provider delivery health (M6b metrics), "operational errors" panel.

### M6b — Operations readiness — Built (needs your verification)
- Priority: P0
- Delivered: `/metrics` (D-174), delivery queue depth, `scripts/restore-check.sh` (D-175), runbooks `backup-restore`, `monitoring`, `deployment`, `docs/security-review.md`, DRAFT `docs/privacy-retention.md` (D-176); OpenAPI 0.14.0; `make restore-check`.
- Acceptance:
  - [x] metrics endpoint disabled without a token, token checked, no personal data in labels (tests)
  - [x] restore rehearsal passes on real PostgreSQL 16 and detects a changed row and a missing FORCE RLS; restore on a fresh server verified (needs the runtime role first)
  - [ ] a restore rehearsal on YOUR server and backup storage
  - [ ] Prometheus scraping and alert rules (suggestions only in `monitoring.md`)
  - [ ] first deployment following `deployment.md` (Docker was never run in the build sandbox)
  - [ ] legal review of `privacy-retention.md`; retention periods decided
  - [ ] dependency scanning, shared rate limiter, external penetration test (see `security-review.md` section 7)
- Not included: pilot-feedback fixes, deletion/closure workflows, automated purge jobs.

### M4c — Bulk certificate issuing — Built (needs your verification)
- Priority: P0 (ZIP export P1)
- Delivered: migration 0012 (`bulk_jobs`, `bulk_job_items`, RLS, one active job per institute, no deletes); `certificates.Jobs` service and `JobStore` (memory + Postgres, one shared contract); runner with token + lease, resume, retry-failed, cancel; routes `GET /certificates/bulk-preview`, `POST /certificates/bulk`, `GET /bulk-jobs`, `GET /bulk-jobs/{id}`, `/items`, `POST /bulk-jobs/{id}/retry-failed|resume|cancel`, `GET /bulk-jobs/{id}/export` (ZIP + manifest); new permission `certificates.export`; "Issue a whole batch" panel in the Certificates tab; decisions D-152..D-159; OpenAPI 0.10.0.
- Acceptance:
  - [x] **M4 exit gate:** a batch of 100 is issued without duplicates: 100 issued certificates, 100 distinct numbers (gapless) and public ids, one live certificate per enrollment, every stored PDF matches its checksum and snapshot; running it again issues nothing (`TestBulkIssuesAHundredWithoutDuplicates`, `-race`)
  - [x] partial failure: failed items keep a short reason, successful work is kept, retry processes only the failed ones and creates no duplicates
  - [x] resumable: shutdown mid-job, takeover only after the lease expires, half-done item requeued, a runner holding an old token is locked out, a certificate stored just before a crash is adopted instead of duplicated, foreign certificates are never adopted
  - [x] cancel keeps what was issued; suspended institute stops/blocks the runner; only one active job per institute
  - [x] preview shows eligible / already issued / not eligible before anything is created; nothing written when no renderer is configured
  - [x] permissions: staff cannot start or export, the issue grant cannot export, admin/owner can; suspended institute is read-only and cannot export; other institutes get 404/403 on every route; export audited with counts only; revoked PDFs excluded; manifest is formula-safe (HTTP tests; mutation-checked)
  - [x] migration 0012 and every job query verified on real PostgreSQL 16 as the restricted runtime role (one-active-job index, state checks, no deletes, RLS fail-closed and cross-tenant, down/up cycle)
  - [ ] Postgres job store contract with `TEST_DATABASE_URL` + `TEST_APP_DATABASE_URL` (type-checked against a stub only; never run here)
  - [ ] frontend tests/lint/typecheck (`npm ci`; never run in the build sandbox; only a syntax check was possible)
  - [ ] a real batch with Chromium in your environment (the 100-certificate gate ran with the fake renderer; the single-certificate Chromium path was verified in M4b)
- Not included: a separate worker process (D-155), logo/signature images (COS-011), public verification page and replace flow (M5), email delivery (M5), XLSX import.

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
