# CertificateOS — Project Brain & Execution Charter

**Document ID:** COS-PROJECT-BRAIN  
**Version:** 1.0.0  
**Status:** Baseline / Ready for implementation planning  
**Last updated:** 2026-10-15  
**Product type:** Multi-tenant SaaS for certificate management for offline institutes  
**Primary stack:** Go API + React/TypeScript frontend + PostgreSQL + Docker  
**Purpose:** The single source of truth for product intent, architecture, implementation order, quality gates, and AI-agent execution.

---

## 0. How to use this document

This document is the project’s operating manual. Humans and coding agents must read it before making implementation changes.

### Source-of-truth order

If two instructions conflict, use this precedence:

1. Explicit user decision recorded in `DECISIONS.md`.
2. This document’s approved requirements and architecture.
3. Approved Architecture Decision Records (ADRs).
4. Existing tests and established code behavior, provided they do not violate items 1–3.
5. Agent assumptions.

Never silently resolve a conflict that changes security, tenancy, data retention, public exposure, or certificate validity. Record the conflict and ask for a decision.

### Recommended repository control files

Keep these files at the repository root. This document defines their intended contents; create them during Phase 0.

- `PROJECT_BRAIN.md` — this document, kept in sync with approved decisions.
- `AGENTS.md` — concise instructions for coding agents.
- `ROADMAP.md` — phase and milestone status.
- `DECISIONS.md` — decisions, dates, rationale, and consequences.
- `CHANGELOG.md` — user-visible and operational changes by release.
- `BACKLOG.md` — prioritized, testable work items.
- `SECURITY.md` — security reporting and security operating rules.
- `docs/adr/` — architecture decision records.
- `docs/runbooks/` — operational procedures.
- `docs/api/` — API contract/OpenAPI source.
- `docs/data/` — data dictionary and retention rules.

### How to execute work

Every work item must have: ID, goal, scope, acceptance criteria, dependencies, tests, and status. Agents should implement one bounded work item at a time, run relevant tests, summarize changed files and risks, and update the roadmap/backlog. Do not mark a task complete just because code was written.

---

# 1. Product vision

## 1.1 Product statement

CertificateOS enables offline training institutes, academies, vocational centres, and similar organizations to securely manage students and courses, create and issue certificates in bulk, distribute them through supported channels, and let employers or other recipients verify certificate authenticity online.

It is not merely a PDF generator. It is a multi-tenant workflow and trust system covering institute onboarding, staff permissions, certificate lifecycle, delivery, verification, and auditability.

## 1.2 Primary users

1. **Platform Super Admin** — owns the platform and its configuration.
2. **Platform Operations Staff** — reviews institute applications and manages approved/suspended accounts within granted permissions.
3. **Institute Owner** — owns an institute workspace, manages institute settings and staff, and oversees issuance.
4. **Institute Admin** — manages institute records and day-to-day operations.
5. **Certificate Staff** — manages student/course records and issues certificates according to assigned permissions.
6. **Student / Recipient** — receives, views, and downloads a certificate.
7. **Verifier** — checks a certificate through a public verification page without needing an account.

## 1.3 Product principles

- **Correctness before convenience:** certificates are official records; duplicate or incorrect issuance is unacceptable.
- **Tenant isolation by design:** one institute must not access another institute’s data or files.
- **Traceability:** important actions are attributable to an actor and timestamp.
- **Accessible sharing:** recipients should not need to create an account merely to view a public verification result.
- **Bulk-first operations:** spreadsheet import and batch issuance are core workflows, not afterthoughts.
- **Provider independence:** email/WhatsApp/SMS delivery must be behind provider adapters.
- **Incremental complexity:** deliver a reliable MVP before subscriptions, advanced analytics, or a full template marketplace.
- **Versioned records:** an issued certificate must remain reproducible and historically explainable after templates change.

## 1.4 Explicit non-goals for MVP

Do not build these unless approved through a decision record:

- Blockchain or NFT certificates.
- Native mobile apps.
- Learning management system, attendance, examinations, or student fee management.
- Public directory of all students or certificates.
- Automated accreditation claims or government verification.
- Complex subscription billing and metered invoicing.
- Arbitrary HTML/JavaScript execution inside user-created templates.
- Multiple independent databases per institute.
- AI-generated certificates or automatic changes to student records.

---

# 2. MVP definition

## 2.1 Required MVP capabilities

MVP is complete only when all capabilities below work end-to-end in a Dockerized environment:

1. User registration/login and secure session/token handling.
2. Institute application submission, including required details and supporting documents.
3. Operations review, approve, reject, request correction, and suspend/reactivate workflows.
4. Approved institute workspace.
5. Institute staff invitations/membership and role-based permissions.
6. Institute profile, courses, batches, and student CRUD.
7. CSV/XLSX student import with preview, row-level validation, duplicate handling, and downloadable error report.
8. Reusable certificate templates using a safe, constrained template format.
9. Individual certificate preview and issuance.
10. Bulk certificate generation with progress and partial failure reporting.
11. Unique certificate number, QR code, immutable issuance snapshot, and versioned PDF stored at issuance.
12. Public verification page with valid/revoked/replaced status and limited data disclosure.
13. Authorized PDF download and shareable certificate URL.
14. Email delivery adapter with delivery attempt history and retry behavior.
15. Audit log for sensitive operations.
16. Basic dashboards and filters for institute staff and platform operations.
17. Automated unit, integration, security-boundary, and end-to-end tests.
18. Local setup documented so a new developer can run the app from a clean clone using Docker Compose.

**MVP channel decision:** support email and shareable links/downloads first. WhatsApp integration is a Phase 2/next milestone unless explicitly moved into MVP by a recorded decision. Do not claim WhatsApp delivery is available until provider setup, consent handling, templates, webhook processing, and tests are complete.

## 2.2 MVP success measures

Track these from early internal testing:

- A new institute application can be submitted and reviewed without database edits.
- An approved institute can import a realistic spreadsheet and resolve invalid rows.
- Staff can issue a batch of at least 100 certificates without duplicate issuance or cross-tenant access.
- A generated PDF matches the immutable certificate snapshot.
- A QR verification result is correct for valid, revoked, and replaced certificates.
- Failed email deliveries are visible and can be retried safely.
- All critical permissions have automated tests.
- A clean developer machine can start the stack using the documented instructions.

Performance targets should be measured and then set based on expected load; do not invent benchmarks before establishing the target deployment environment.

---

# 3. Scope and functional requirements

Priority labels: **P0 = release blocker**, **P1 = important**, **P2 = later**.

## 3.1 Authentication and identity

### Requirements
- P0: users can authenticate securely.
- P0: passwords, if used, are stored only as strong password hashes using a maintained library/algorithm.
- P0: protected routes require authentication.
- P0: authorization is checked server-side on every protected operation.
- P0: login and sensitive flows are rate-limited.
- P1: password reset and email verification.
- P1: optional MFA for platform administrators.
- P1: session revocation and device/session listing.

### Acceptance criteria
- Unauthenticated requests cannot access protected endpoints.
- A user cannot grant themselves a higher role by modifying a request.
- Suspended users cannot continue using active sessions after suspension takes effect.
- Secrets and tokens never appear in logs.

## 3.2 Institute onboarding and operations

### Institute application fields
- Legal/display name.
- Requested workspace slug.
- Institute type/category.
- Address and country/region.
- Official email and phone.
- Authorized representative name and role.
- Website, if available.
- Supporting documents and consent/attestation.
- Applicant user reference.
- Submission timestamp and application status.

Collect only information necessary for onboarding and operations. Define document retention and access rules before production.

### Application states
`draft → submitted → under_review → approved | rejected | changes_requested`

An approved institute may later become `suspended` or `closed`. Application state and institute operational state should be distinct concepts.

### Requirements
- P0: operations staff can see a queue of applications.
- P0: review actions require permission and create audit entries.
- P0: approval records reviewer, time, and decision.
- P0: rejection/change request requires a reason.
- P0: an institute cannot issue certificates until approved and active.
- P1: notifications on submission and decision.
- P1: filters, search, and review notes.

### Acceptance criteria
- No endpoint or UI path bypasses approval state.
- Repeated approval requests are idempotent or return a clear conflict.
- A suspended institute cannot issue new certificates or add new staff, subject to explicit read-only access policy.

## 3.3 Institute workspace and staff

### Requirements
- P0: an owner can invite a staff member by email.
- P0: memberships are scoped to an institute.
- P0: permissions are enforced in the API.
- P0: role changes and removals are audited.
- P1: invitations expire and can be resent/revoked.
- P1: owners can update approved institute profile fields; material identity changes may require re-review.

### Roles and default permissions
- **Platform Super Admin:** platform-wide administration.
- **Platform Operations:** review applications, change institute operational status, view permitted operational metadata; no default access to student PII or certificate PDFs unless needed and explicitly granted.
- **Institute Owner:** manage workspace, staff, profile, and all certificate functions.
- **Institute Admin:** manage students, courses, batches, templates, and issuance; cannot change platform-level settings.
- **Certificate Staff:** manage assigned operational records and issue certificates if permission is granted.
- **Student/Verifier:** no workspace access; public verification only.

Use permissions as named capabilities (e.g. `students.read`, `students.write`, `certificates.issue`, `certificates.revoke`, `staff.manage`) rather than scattering role-name checks throughout business logic.

## 3.4 Courses, batches, and students

### Student fields
- Internal UUID.
- Institute ID.
- Institute-local student number (optional, unique within institute when present).
- Full name.
- Email and phone (optional, only when needed).
- Course/batch relationship.
- Enrollment/completion status.
- Relevant completion date and grade/result (optional).
- Created/updated timestamps.
- Archived timestamp, where applicable.

Do not expose contact information on public verification pages by default.

### Requirements
- P0: create, view, update, archive student.
- P0: create/manage courses and batches.
- P0: search and filter records.
- P0: CSV/XLSX import preview and row-level errors.
- P0: configurable duplicate detection with a safe default; never silently overwrite a student.
- P1: export permitted records to CSV.
- P1: bulk archive and merge workflow, only with audit trails.

### Import flow
1. Upload file to a controlled temporary location.
2. Check file size/type and parse without executing embedded content.
3. Let staff map columns to supported fields.
4. Validate rows and display a preview.
5. Identify likely duplicates and conflicts.
6. User confirms import.
7. Process in a transaction or bounded chunks with an import job record.
8. Provide result counts and downloadable row-level error report.
9. Record who imported what and when.

## 3.5 Certificate templates

### MVP approach
Start with preset templates plus configurable layout fields. Build a visual drag-and-drop editor only after issuance correctness is stable, unless an explicit product decision changes this.

### Supported dynamic fields
At minimum: student name, institute name/logo, course name, completion date, issue date, certificate number, grade/result (optional), authorized signatory name/title/image (where authorized), and QR code.

### Requirements
- P0: template belongs to one institute or an explicitly platform-managed template library.
- P0: template version is immutable after use; edits create a new version.
- P0: template preview is available before issuance.
- P0: safe rendering; no arbitrary JavaScript, filesystem access, network requests, or untrusted template code.
- P1: page size/orientation and optional background image.
- P1: multiple signatories and localized text.
- P2: full visual editor and template marketplace.

### Acceptance criteria
- A template cannot read data from another tenant.
- A template version used for an issued certificate can be retrieved for audit.
- Missing required data blocks issuance with a clear validation message.

## 3.6 Certificate issuance and lifecycle

### Certificate record must preserve
- Internal certificate UUID.
- Institute ID.
- Certificate number.
- Student and course references.
- Immutable display snapshot of the fields used at issuance.
- Template ID and template version.
- Issued-at timestamp and issuing actor.
- PDF storage key and checksum/hash.
- Current lifecycle status.
- Optional replacement reference.
- Audit/event history.

### Lifecycle
`draft → queued → issued → revoked`
A corrected certificate should normally be issued as a **replacement** with a link to the prior certificate; do not silently overwrite an issued certificate. Use explicit states for failed jobs where necessary.

### Requirements
- P0: issue one certificate after eligibility and permission checks.
- P0: issue certificates in bulk through a job with progress.
- P0: enforce unique certificate number within the agreed scope.
- P0: ensure retrying a request/job does not create duplicate certificates.
- P0: store the rendered PDF and checksum at issuance.
- P0: revocation requires permission, reason, timestamp, and actor.
- P0: corrections create a replacement or a clearly audited reissue.
- P1: configurable certificate-number format, within safe limits.
- P1: ZIP export of authorized PDFs for a completed bulk job.

### Idempotency
For create/issue operations, accept an idempotency key or use a stable job-item key. A retry of the same operation must return the original result or a clear status, not create a second certificate.

### Bulk job states
`queued → processing → completed | completed_with_errors | failed | cancelled`

A bulk job must show total, succeeded, failed, and remaining counts. One bad row must not silently erase successful work. Failed items must be safely retryable.

## 3.7 PDF storage and downloads

### Decision
Generate and store a versioned PDF at issuance. The PDF is the artifact that was issued; it must not change when the template or student profile changes.

### Requirements
- P0: PDFs are stored privately, not in a public bucket.
- P0: downloads go through authorization or short-lived signed URLs.
- P0: public verification does not automatically grant unrestricted PDF access unless the product explicitly chooses that policy.
- P0: store a cryptographic checksum of the PDF bytes.
- P0: replace/revoke does not delete historical artifacts or audit events.
- P1: retention, deletion, and backup policies documented before production.
- P1: support regenerating a file only as a controlled reissue, never silently as the same issued artifact.

## 3.8 Public verification

### Route
A public route such as `/verify/{public_id}` or a high-entropy verification token. Certificate numbers may be human-readable, but must not be treated as authorization secrets.

### Public result
- Authenticity status.
- Institute display name.
- Student display name, subject to privacy policy.
- Course/program.
- Issue date.
- Certificate number.
- Revoked/replaced state and appropriate notice.
- Optional limited PDF view/download action according to product policy.

### Requirements
- P0: QR links resolve to the canonical verification service.
- P0: verifier sees `valid`, `revoked`, or `replaced` accurately.
- P0: public response contains only approved fields.
- P0: rate limiting and anti-enumeration controls.
- P0: invalid IDs do not reveal whether other sensitive records exist.
- P1: log verification events using privacy-conscious metadata.
- P1: institute can configure allowed public fields within platform limits.

## 3.9 Delivery centre

### MVP
- Shareable URL and authorized download.
- Email provider adapter.
- Delivery status and attempt history.
- Retry for transient errors.

### Later
- WhatsApp Business Platform integration.
- SMS integration.
- Provider webhook processing.
- Approved messaging templates, consent management, opt-outs, and channel-specific compliance.

### Requirements
- P0: certificate issuance is independent from successful delivery.
- P0: delivery failure does not roll back or duplicate a certificate.
- P0: each attempt records channel, provider, status, timestamps, and sanitized error.
- P0: retries are bounded and use backoff for transient errors.
- P0: provider secrets are stored securely and never logged.
- P1: delivery reports and manual retry.
- P1: institute-specific sender configuration after secure validation.

Do not label a message “delivered” unless the provider returns a status that supports that claim. Distinguish queued, accepted, sent, delivered, bounced, failed, and unknown as applicable.

## 3.10 Dashboards and reporting

### Platform operations
- Pending applications.
- Approved/suspended institute counts.
- Recent decisions and operational errors.
- Certificate/job volume at aggregate level.
- Provider delivery health.

### Institute
- Students and active batches.
- Certificates issued in selected periods.
- Bulk job status.
- Delivery success/failure summary.
- Quick search and recent activity.

Reports must be tenant-scoped. Platform reports should avoid exposing student PII by default.

---

# 4. Non-functional requirements

## 4.1 Security
- Use TLS in deployed environments.
- Use maintained cryptographic and authentication libraries.
- Validate all input server-side.
- Use parameterized SQL.
- Enforce authorization on every request.
- Protect against CSRF if cookie-based auth is used; use secure, HttpOnly, SameSite cookies as appropriate.
- Apply CSP and other suitable browser security headers.
- Rate-limit login, password reset, public verification, and sensitive operations.
- Upload validation: allowlisted formats, size limits, safe filenames, malware scanning where available, private storage.
- No secrets in source control, images, frontend bundles, logs, or test fixtures.
- Dependabot/renovation-style dependency updates and vulnerability scanning should be configured.
- Maintain audit trails for role changes, approvals, issuance, revocation, replacement, and export.

## 4.2 Privacy and compliance
- Collect the minimum required personal data.
- Define data retention, deletion, export, and backup retention before production.
- Document privacy notice and lawful basis/consent requirements for the target jurisdictions.
- Avoid unnecessary public exposure of student details.
- Provide institute data export and account closure workflow, subject to legal/audit retention requirements.
- Do not claim compliance with a law or certification until reviewed and evidenced.

## 4.3 Reliability
- Database migrations are versioned and reversible where practical.
- Background jobs have retry limits, dead-letter/failure visibility, and idempotency.
- Critical operations are transactional where appropriate.
- Backups are monitored and restore-tested.
- External provider outages do not corrupt certificate records.
- Health/readiness endpoints distinguish application health from dependency health.

## 4.4 Observability
- Structured logs with request/job correlation IDs.
- Metrics for API errors/latency, job duration, PDF render failures, provider outcomes, and queue depth.
- Error reporting with PII and secrets redacted.
- Operational runbooks for failed migrations, database restore, provider outages, and stuck jobs.

## 4.5 Accessibility and usability
- Responsive desktop-first UI with keyboard-accessible forms.
- Clear loading, empty, error, and success states.
- Bulk workflows must show previews, progress, and actionable row-level errors.
- Destructive actions require confirmation and clear consequences.
- Date/time display uses a documented timezone policy; store timestamps in UTC.

---

# 5. Architecture and implementation constraints

## 5.1 System architecture

- **Web:** React + TypeScript + Vite.
- **API:** Go REST API.
- **Database:** PostgreSQL.
- **Object storage:** S3-compatible storage; MinIO locally.
- **Worker:** Go worker for bulk PDF generation and delivery.
- **Queue:** begin with a simple durable job table or an explicitly selected queue. Add Redis-backed queue only after documenting why it is needed.
- **PDF renderer:** controlled HTML/CSS rendering through a pinned Chromium environment or another approved renderer.
- **Messaging:** provider interfaces/adapters; never couple domain logic to one provider.
- **API contract:** OpenAPI committed to the repository and validated in CI.

Avoid microservices in the MVP. Use a modular monolith with a separate worker process if necessary. This minimizes deployment and debugging complexity while keeping boundaries clear.

## 5.2 Suggested repository structure

```text
certificate-os/
├── AGENTS.md
├── PROJECT_BRAIN.md
├── ROADMAP.md
├── DECISIONS.md
├── BACKLOG.md
├── CHANGELOG.md
├── compose.yaml
├── .env.example
├── Makefile
├── apps/
│   └── web/
│       ├── src/
│       │   ├── app/
│       │   ├── features/
│       │   ├── components/
│       │   ├── lib/
│       │   └── routes/
│       └── tests/
├── services/
│   ├── api/
│   │   ├── cmd/api/
│   │   ├── internal/
│   │   │   ├── auth/
│   │   │   ├── tenancy/
│   │   │   ├── institutes/
│   │   │   ├── students/
│   │   │   ├── courses/
│   │   │   ├── templates/
│   │   │   ├── certificates/
│   │   │   ├── delivery/
│   │   │   ├── verification/
│   │   │   ├── audit/
│   │   │   └── platform/
│   │   └── migrations/
│   └── worker/
├── packages/
│   └── shared-contracts/
├── docs/
│   ├── adr/
│   ├── api/
│   ├── data/
│   └── runbooks/
├── deploy/
├── scripts/
└── tests/
    ├── integration/
    ├── security/
    └── e2e/
```

Structure is a target, not a mandate to create empty folders. Keep modules cohesive and avoid premature abstraction.

## 5.3 Backend boundaries
- HTTP handlers: parse request, authenticate, call service, map errors to HTTP.
- Services: enforce business rules and transactions.
- Repositories/data access: SQL and persistence only.
- Domain types: states, invariants, and value objects.
- Provider adapters: email, storage, PDF renderer, messaging.
- Audit writer: standardized event creation for critical actions.

Do not place business logic in handlers or React components.

## 5.4 Frontend boundaries
- Route/page composition separate from reusable UI.
- Feature modules own feature-specific API calls and schemas.
- Shared API client handles base URL, auth, request IDs, and normalized errors.
- Server remains authoritative for permissions.
- Forms validate for user experience, but backend validation remains mandatory.
- Never embed service credentials in the frontend.

## 5.5 API conventions
- Prefix endpoints with `/api/v1`.
- Use JSON for normal API requests/responses.
- Use consistent pagination, sorting, and filtering.
- Use a consistent error envelope with stable machine-readable error codes.
- Validate request bodies and path/query parameters.
- Use `409 Conflict` for state conflicts, `401` for unauthenticated, `403` for unauthorized, `404` where appropriate, and `422` for validation errors if adopted consistently.
- Generate and validate OpenAPI documentation in CI.
- Avoid exposing database schema details in public errors.

---

# 6. Data model baseline

The exact schema will evolve through migrations and ADRs. Use UUID primary keys and UTC timestamps. Every institute-owned row must be tenant-scoped directly or through an explicitly enforced parent relationship.

## 6.1 Core entities
- `users`
- `platform_roles` / platform permission assignments
- `institutes`
- `institute_applications`
- `institute_memberships`
- `invitations`
- `courses`
- `batches`
- `students`
- `imports`
- `import_rows` or structured import error artifacts
- `certificate_templates`
- `certificate_template_versions`
- `certificates`
- `certificate_events`
- `bulk_jobs`
- `bulk_job_items`
- `delivery_attempts`
- `verification_events`
- `audit_logs`
- `provider_configs` (encrypted secrets or secret-manager references)
- `subscriptions` (later; do not block MVP on billing)

## 6.2 Integrity constraints
- Unique institute slug.
- Unique membership per `(institute_id, user_id)`.
- Unique student number per `(institute_id, student_number)` when present.
- Unique certificate number per `(institute_id, certificate_number)` unless an approved global format is chosen.
- Unique idempotency key within operation/tenant scope.
- Foreign keys for all relationships.
- Do not hard-delete issued certificates or audit records through normal application workflows.
- Use explicit status enums/check constraints where appropriate.
- Add indexes for tenant ID, common search fields, certificate number, status, and timestamps based on query patterns.

## 6.3 Tenant isolation rules
- Derive tenant context from authenticated membership, not a client-supplied tenant ID alone.
- Every repository method for tenant-owned data must accept tenant context or a tenant-scoped transaction.
- Check ownership before returning file metadata or signed download URLs.
- Public verification uses a narrow public service and response schema, not a general certificate detail endpoint.
- Add automated negative tests proving cross-tenant reads and writes fail.
- Consider PostgreSQL row-level security as defense in depth; do not treat it as a substitute for application checks.

---

# 7. API inventory

This is an initial logical inventory; exact payload schemas must be documented in OpenAPI before implementation is considered complete.

## 7.1 Authentication
- `POST /api/v1/auth/register`
- `POST /api/v1/auth/login`
- `POST /api/v1/auth/logout`
- `POST /api/v1/auth/refresh` (only if refresh-token design is selected)
- `POST /api/v1/auth/password/forgot`
- `POST /api/v1/auth/password/reset`
- `GET /api/v1/me`

## 7.2 Institute applications and operations
- `POST /api/v1/institute-applications`
- `GET /api/v1/ops/institute-applications`
- `GET /api/v1/ops/institute-applications/{id}`
- `POST /api/v1/ops/institute-applications/{id}/submit-for-review`
- `POST /api/v1/ops/institute-applications/{id}/approve`
- `POST /api/v1/ops/institute-applications/{id}/reject`
- `POST /api/v1/ops/institute-applications/{id}/request-changes`
- `POST /api/v1/ops/institutes/{id}/suspend`
- `POST /api/v1/ops/institutes/{id}/reactivate`

## 7.3 Workspace and staff
- `GET/PATCH /api/v1/institute`
- `GET/POST /api/v1/institute/members`
- `PATCH /api/v1/institute/members/{id}`
- `DELETE /api/v1/institute/members/{id}`
- `POST /api/v1/institute/invitations`
- `POST /api/v1/institute/invitations/{token}/accept`

## 7.4 Courses, batches, students
- `GET/POST /api/v1/courses`
- `GET/PATCH/DELETE /api/v1/courses/{id}` (delete should normally archive when referenced)
- `GET/POST /api/v1/batches`
- `GET/PATCH /api/v1/batches/{id}`
- `GET/POST /api/v1/students`
- `GET/PATCH /api/v1/students/{id}`
- `POST /api/v1/students/imports`
- `GET /api/v1/students/imports/{id}`
- `GET /api/v1/students/imports/{id}/errors`
- `POST /api/v1/students/imports/{id}/confirm`
- `GET /api/v1/students/export`

## 7.5 Templates and certificates
- `GET/POST /api/v1/certificate-templates`
- `GET/PATCH /api/v1/certificate-templates/{id}`
- `POST /api/v1/certificate-templates/{id}/versions`
- `POST /api/v1/certificate-templates/{id}/preview`
- `POST /api/v1/certificates/preview`
- `POST /api/v1/certificates`
- `POST /api/v1/certificates/bulk`
- `GET /api/v1/bulk-jobs/{id}`
- `POST /api/v1/bulk-jobs/{id}/retry-failed`
- `GET /api/v1/certificates`
- `GET /api/v1/certificates/{id}`
- `POST /api/v1/certificates/{id}/revoke`
- `POST /api/v1/certificates/{id}/replace`
- `POST /api/v1/certificates/{id}/deliveries`
- `GET /api/v1/certificates/{id}/download`

## 7.6 Public verification and provider callbacks
- `GET /verify/{public_token}` (public web route)
- `GET /api/v1/public/certificates/{public_token}` (narrow public response if API route needed)
- `POST /api/v1/webhooks/email/{provider}` (if provider supports callbacks)
- Later: `POST /api/v1/webhooks/whatsapp/{provider}` and SMS equivalent

Do not expose a generic public endpoint that returns the full internal certificate record.

---

# 8. UX requirements and key screens

## 8.1 Public and auth
- Landing page with product explanation and institute signup.
- Login and password reset.
- Institute application form with progress, validation, document upload, and confirmation.
- Public certificate verification page.
- Public certificate not-found/invalid page that does not leak private data.

## 8.2 Platform operations
- Operations overview.
- Application queue with filters and status.
- Application review detail, documents, notes, and decision controls.
- Institute directory and status management.
- Audit/operations activity view.
- Platform settings only when required.

## 8.3 Institute workspace
- Overview dashboard.
- Student list, student detail, add/edit, import wizard.
- Course and batch management.
- Template list, template configuration, preview.
- Certificate issuance flow: select cohort → check eligibility → preview sample → confirm → monitor job.
- Certificate list/detail with status, PDF, delivery attempts, revoke/replace actions.
- Staff/member management.
- Institute profile and approved branding settings.

## 8.4 UX rules
- Show the current institute/workspace prominently.
- Use clear status labels and accessible color-independent indicators.
- Before bulk issuance, show the number of eligible records, skipped records, and reasons.
- Require explicit confirmation before irreversible actions.
- Explain why an operation failed and what the user can do next.
- Avoid showing success until the server confirms the action.
- Never show an issued certificate as successful before its durable record and PDF are committed.

---

# 9. Testing and release quality

## 9.1 Required test layers
1. Unit tests for domain rules and validation.
2. Repository/integration tests against PostgreSQL.
3. API tests for authentication, authorization, status transitions, and error responses.
4. Tenant-isolation tests for every resource family.
5. PDF snapshot/visual tests for representative templates.
6. Job idempotency and retry tests.
7. Provider adapter tests using fakes/mocks; sandbox integration tests before enabling live delivery.
8. Frontend component and form tests.
9. End-to-end tests for institute application through certificate verification.
10. Security checks: dependency scan, secret scan, static analysis, upload validation, rate limits.

## 9.2 Minimum end-to-end journeys
- Applicant submits application → operations approves → owner enters workspace.
- Owner invites staff → staff permission works only within that institute.
- Staff imports students → fixes invalid rows → confirms import.
- Staff creates template → previews → issues a certificate.
- Staff issues a bulk batch → job reports progress and failures.
- Recipient opens QR link → sees valid certificate.
- Institute revokes certificate → same QR now shows revoked.
- Institute replaces certificate → old record shows replaced and new record verifies.
- Email provider fails → attempt is recorded → retry does not duplicate certificate.
- User from another institute tries to access certificate ID or PDF → request is denied.

## 9.3 Definition of Done
A work item is done only when:
- Acceptance criteria pass.
- Tests are added/updated and pass.
- Authorization and tenant isolation are checked where relevant.
- Input validation and error handling are implemented.
- Logs avoid secrets and unnecessary PII.
- Migrations and API docs are updated where needed.
- UI includes loading, empty, error, and success states where applicable.
- Documentation and roadmap/backlog status are updated.
- No unrelated refactors or unapproved dependencies are included.
- The agent reports what changed, commands/tests run, known limitations, and the next task.

---

# 10. Docker, local development, and CI/CD

## 10.1 Local services
- `web`: React dev server with hot reload.
- `api`: Go API with development reload.
- `worker`: background jobs.
- `postgres`: local database with a persistent named volume.
- `minio`: local S3-compatible object storage.
- `mailpit`: local email testing sink.
- Optional `redis`: only if the chosen queue/cache requires it.

## 10.2 Developer experience
A clean clone should be startable with a documented command, ideally:
```bash
cp .env.example .env
docker compose up --build
```
The README must state required Docker/Compose versions, local URLs, seed-user procedure, migration behavior, and troubleshooting steps. Do not commit real credentials. Provide safe local-only seed accounts with conspicuously non-production credentials.

Recommended Make targets:
- `make dev`
- `make test`
- `make lint`
- `make migrate`
- `make seed`
- `make e2e`
- `make down`

## 10.3 CI gates
On each pull request:
- Format Go and frontend files.
- Run Go unit tests and frontend tests.
- Run lint/static analysis.
- Validate migrations and OpenAPI.
- Run dependency and secret scanning.
- Build container images.
- Run integration tests where feasible.
- Block merge on failed critical checks.

Production deployment must use immutable image tags, secret management, health checks, backups, and a documented rollback procedure. Never deploy a database migration without a reviewed migration plan and backup/restore understanding.

---

# 11. Roadmap and milestones

Milestones are capability-based, not calendar promises. Estimate after the repository and deployment constraints are known.

## M0 — Project foundation
**Deliverables:** repo structure, AGENTS.md, environment example, Docker Compose, Go API skeleton, React shell, PostgreSQL migration framework, CI, logging, health endpoints, decision log.

**Exit gate:** clean clone starts; CI passes; migration runs on an empty database.

## M1 — Identity and tenancy
**Deliverables:** authentication, users, institute memberships, permissions, tenant context, cross-tenant security tests.

**Exit gate:** protected API and negative tenancy tests pass.

## M2 — Institute onboarding and operations
**Deliverables:** application form, document upload, operations queue, review decisions, approval state enforcement, audit events.

**Exit gate:** unapproved institute cannot use certificate features.

## M3 — Institute records
**Deliverables:** workspace, staff invitations, courses, batches, students, import wizard, row-level errors.

**Exit gate:** a realistic spreadsheet can be imported safely.

## M4 — Templates and issuance
**Deliverables:** preset templates, versioning, preview, PDF rendering, QR links, unique certificate numbers, stored PDFs, bulk jobs.

**Exit gate:** issue a batch of 100 test certificates without duplicates; artifacts verify against snapshots.

## M5 — Verification and delivery
**Deliverables:** public verification, authorized downloads, email adapter, delivery attempts, retry logic.

**Exit gate:** valid/revoked/replaced flows and delivery failure/retry tests pass.

## M6 — Pilot readiness
**Deliverables:** operational dashboards, audit views, backups/restore test, security review, privacy/retention policy, deployment runbook, monitoring, pilot feedback fixes.

**Exit gate:** pilot institute can complete the workflow without manual database edits.

## M7 — Post-MVP
Possible scope, each requiring prioritization:
- WhatsApp Business Platform integration.
- SMS integration.
- Subscription billing and usage limits.
- Advanced visual template editor.
- Multiple languages.
- Custom domains/branding.
- Advanced analytics and exports.
- SSO/MFA enhancements.
- Integrations with external student information systems.

---

# 12. AI-agent execution protocol

This section is mandatory for all coding agents, whether used in an IDE, terminal, or task orchestrator.

## 12.1 Before coding
1. Read `AGENTS.md`, `PROJECT_BRAIN.md`, `DECISIONS.md`, `ROADMAP.md`, and the relevant backlog item.
2. Inspect the existing repository before assuming files or libraries exist.
3. Identify the exact task, dependencies, acceptance criteria, and affected modules.
4. Check whether an approved ADR covers the design.
5. If a critical decision is missing, present a short decision request instead of silently inventing behavior.
6. Write a brief plan for changes and tests.

## 12.2 While coding
- Implement only the assigned scope.
- Follow existing patterns unless they conflict with this charter or an approved decision.
- Prefer small, reviewable changes.
- Do not rewrite large files or change frameworks without approval.
- Do not add a dependency without explaining why it is needed.
- Do not weaken validation, permissions, tests, or security to make a test pass.
- Never put secrets in code, test fixtures, generated artifacts, or logs.
- Never use production data in local development or tests.
- Do not claim external integrations work without sandbox/live verification appropriate to the provider.
- Update migrations, OpenAPI, and tests alongside code.

## 12.3 Before reporting completion
1. Run relevant tests, lint, and build commands.
2. Inspect the diff for unrelated changes and accidental secrets.
3. Confirm acceptance criteria individually.
4. Update `BACKLOG.md` and `ROADMAP.md` only if the task genuinely meets its exit criteria.
5. Add a changelog entry for user-visible or operational changes.
6. Update the project brain or ADR if an approved design has changed.
7. Report:
   - Summary.
   - Files changed.
   - Tests/commands run and outcomes.
   - Security/tenancy implications.
   - Known limitations.
   - Suggested next task.

## 12.4 Agent stop conditions
Stop and ask for human direction if:
- Requirements conflict or an approval is missing.
- A change could expose tenant data or public student information.
- A migration may destroy or irreversibly alter data.
- Certificate validity, numbering, revocation, or replacement semantics are unclear.
- A provider requires credentials, paid activation, legal acceptance, or a live send.
- A request asks to bypass tests, audit trails, access control, or security checks.
- Scope expands beyond the assigned milestone.

## 12.5 Work item template

Use this template in `BACKLOG.md`:

```markdown
### COS-XXX — Short title
- Status: Proposed | Ready | In Progress | Blocked | Review | Done
- Priority: P0 | P1 | P2
- Goal:
- User value:
- Scope included:
- Scope excluded:
- Dependencies:
- Acceptance criteria:
  - [ ] ...
- Tests required:
- Security/tenant considerations:
- Documentation updates:
- Owner/agent:
- Notes/decision links:
```

## 12.6 Pull request template

```markdown
## Summary
## Work item
## User-visible changes
## Implementation notes
## Tests run
## Security and tenant-isolation review
## Database/API changes
## Screenshots (if UI)
## Known limitations
## Rollback notes
```

---

# 13. Decision log and initial assumptions

The following choices are the current baseline. A change requires an entry in `DECISIONS.md`; do not silently drift.

| ID | Decision | Baseline | Reason |
|---|---|---|---|
| D-001 | Product model | Multi-tenant cloud SaaS | One centrally operated platform for many institutes |
| D-002 | Architecture | Modular monolith + worker | Lower operational complexity than microservices |
| D-003 | Backend | Go REST API | Explicitly selected project stack |
| D-004 | Frontend | React + TypeScript + Vite | Explicitly selected project stack |
| D-005 | Primary database | PostgreSQL | Relational integrity and transaction support |
| D-006 | Local environment | Docker Compose | Repeatable development environment |
| D-007 | Certificate artifact | Store versioned PDF at issuance | Preserve what was actually issued |
| D-008 | MVP template approach | Presets/configurable fields first | Reduce scope and issuance risk |
| D-009 | MVP delivery | Share links/downloads + email | Avoid blocking core issuance on messaging-provider approval |
| D-010 | WhatsApp/SMS | Post-MVP unless explicitly approved | Requires provider setup, consent, templates, and webhook handling |
| D-011 | Tenant model | Shared database, tenant-scoped records | Practical initial SaaS architecture |
| D-012 | Public verification | Narrow public response | Protect student data |
| D-013 | Certificate correction | Replacement/reissue with history | Preserve auditability |
| D-014 | Billing | Post-MVP | Not required to validate core workflow |

These are project defaults inferred from the concept and should be explicitly confirmed by the product owner during M0. Until confirmed, agents must treat them as the working baseline, not as irreversible commitments.

## 13.1 ADR template

```markdown
# ADR-XXX: Title
- Status: Proposed | Accepted | Superseded
- Date:
- Decision owner:

## Context
## Options considered
## Decision
## Consequences
## Security/privacy implications
## Migration/rollback implications
## Related work items
```

---

# 14. Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Cross-tenant data leakage | Critical | Tenant context, repository scoping, RLS defense-in-depth, negative tests |
| Forged or altered certificate | Critical | Stored PDF checksum, authoritative verification, controlled issuance |
| Duplicate bulk issuance | High | Idempotency keys, unique constraints, job-item records |
| Unsafe template/PDF rendering | High | Constrained templates, isolated renderer, resource limits, no arbitrary code |
| Public exposure of student data | High | Narrow response schema, privacy review, rate limits |
| Messaging-provider restrictions | Medium/High | Provider adapter, sandbox testing, phased delivery integration |
| Spreadsheet corruption/duplicates | High | Preview, validation, explicit duplicate policy, error report |
| Lost PDF artifacts | High | Private object storage, backups, checksums, restore tests |
| Unclear revocation/replacement semantics | High | Defined lifecycle, audit events, UX warnings, tests |
| Scope creep | High | MVP boundary, P0/P1/P2, change control |
| Agent design drift | High | This charter, ADRs, decision precedence, stop conditions |
| Vendor lock-in | Medium | Storage/messaging/PDF adapters and documented interfaces |

---

# 15. Release, versioning, and change management

## 15.1 Version rules
Use semantic versioning for product releases:
- **MAJOR:** incompatible API or data behavior requiring migration/coordination.
- **MINOR:** backward-compatible functionality.
- **PATCH:** backward-compatible bug/security fixes.

Use this document’s version separately:
- Increase PATCH for clarifications that do not change scope/architecture.
- Increase MINOR for added requirements or a new phase.
- Increase MAJOR for changes to product model, core lifecycle, tenancy, or architecture.

## 15.2 Change procedure
1. Describe the requested change and user value.
2. Identify impacted requirements, APIs, schema, tests, privacy/security, and milestones.
3. Determine whether it changes an approved decision.
4. If so, write an ADR or update the relevant decision record.
5. Update this project brain, backlog, API contract, and roadmap as needed.
6. Implement through small work items.
7. Add migration and rollback notes for data changes.
8. Verify acceptance criteria and release notes.

Never rewrite history to hide a decision change. Supersede the old decision and explain why.

## 15.3 Release checklist
- [ ] All P0 acceptance criteria pass.
- [ ] CI passes and dependency/security scans reviewed.
- [ ] Migrations reviewed and tested against representative data.
- [ ] Backup exists and restore procedure has been tested for production releases.
- [ ] Secrets and environment settings verified.
- [ ] Tenant-isolation and public verification tests pass.
- [ ] Provider sandbox/live configuration is verified.
- [ ] Monitoring and alerting are active.
- [ ] Runbooks and changelog are updated.
- [ ] Rollback plan is documented.
- [ ] Product owner approves release.

---

# 16. First implementation sprint: exact task order

Use these as the initial backlog seeds. Keep each task small enough for a coding agent to implement and verify.

1. **COS-001 — Repository skeleton and agent instructions**
   - Create root docs, Go API module, React app, formatting/lint commands, `.env.example`.
   - Acceptance: clean clone instructions are accurate; no secrets committed.

2. **COS-002 — Docker Compose local environment**
   - Add web, API, PostgreSQL, MinIO, Mailpit, and health checks.
   - Acceptance: documented command starts services; persistent DB volume works.

3. **COS-003 — CI baseline**
   - Build, test, lint, migration validation, secret/dependency scanning.
   - Acceptance: CI fails on an intentionally failing test and passes when fixed.

4. **COS-004 — PostgreSQL migration framework**
   - Add initial schema migration conventions and test database setup.
   - Acceptance: fresh database migration succeeds; migration status is inspectable.

5. **COS-005 — API foundation**
   - Add configuration, structured logs, request IDs, health/readiness, normalized errors.
   - Acceptance: missing config fails safely; health endpoints work.

6. **COS-006 — Frontend shell**
   - Add routing, layout, shared API client, error/loading/empty states, accessible base components.
   - Acceptance: web app runs in Docker and shows placeholder auth/workspace routes.

7. **COS-007 — Identity and authorization design**
   - Implement authentication and capability-based permissions; document session strategy.
   - Acceptance: auth tests and role-denial tests pass.

8. **COS-008 — Tenant context and isolation**
   - Implement tenant context in middleware/services/repositories.
   - Acceptance: cross-tenant negative tests pass before business modules expand.

9. **COS-009 — Institute application schema and workflow**
   - Implement application state machine and audit events.
   - Acceptance: invalid state transitions are rejected.

10. **COS-010 — Operations review UI/API**
    - Queue, review detail, approve/reject/request-changes.
    - Acceptance: only authorized operations users can make decisions; every decision is audited.

Do not start certificate PDF generation before COS-007 and COS-008 are complete and their quality gates pass.

---

# 17. Product-owner questions to resolve before production

These questions do not block initial repository setup, but must be answered before the affected feature ships:

1. Which legal entity owns and operates the platform?
2. What institute documents are required for approval, and who can view them?
3. What is the certificate numbering format and uniqueness scope?
4. Which fields can be public on verification pages?
5. Can anyone download a PDF from a public verification page, or only via a separate share token?
6. How long are application documents, student records, delivery logs, and audit logs retained?
7. What are the account suspension and institute closure rules?
8. Is institute branding fully customizable or limited to approved fields?
9. Which email provider will be used in production?
10. Which WhatsApp provider/path is selected for Phase 2, and how will opt-in and templates be managed?
11. What are expected peak bulk batch size and acceptable completion time?
12. What deployment region, recovery objectives, and budget constraints apply?
13. Which languages and date formats are required for the first pilot?
14. What is the process for correcting a student name or course after certificate issuance?
15. Is billing required before the first pilot or only before public launch?

Every answer that changes behavior should be captured in `DECISIONS.md`.

---

# 18. Final operating rule

**Build a trustworthy certificate lifecycle first; add commercial breadth second.**

The first successful release is not the one with the most screens. It is the one where an institute can be approved, securely manage its own records, issue a correct certificate, share it, and have a third party verify its status—without manual database edits, duplicate issuance, or cross-tenant data exposure.

When uncertain, preserve data, preserve audit history, fail closed on authorization, and ask for a decision rather than inventing product behavior.
