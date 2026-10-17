# DECISIONS

Decisions override `PROJECT_BRAIN.md` (see its §0 precedence). Never rewrite history: supersede with a new entry.

| ID | Date | Decision | Status |
|---|---|---|---|
| D-001 | 2026-10-15 | Multi-tenant cloud SaaS | Baseline (confirm in M0) |
| D-002 | 2026-10-15 | Modular monolith + worker | Baseline (confirm in M0) |
| D-003 | 2026-10-15 | Go REST API | Baseline |
| D-004 | 2026-10-15 | React + TypeScript + Vite | Baseline |
| D-005 | 2026-10-15 | PostgreSQL | Baseline |
| D-006 | 2026-10-15 | Docker Compose local env | Baseline |
| D-007 | 2026-10-15 | Store versioned PDF at issuance | Baseline |
| D-008 | 2026-10-15 | Preset templates + configurable fields first | Baseline |
| D-009 | 2026-10-15 | MVP delivery: share links/downloads + email | Baseline |
| D-010 | 2026-10-15 | WhatsApp/SMS post-MVP | Baseline |
| D-011 | 2026-10-15 | Shared DB, tenant-scoped rows | Baseline |
| D-012 | 2026-10-15 | Narrow public verification response | Baseline |
| D-013 | 2026-10-15 | Corrections via replacement/reissue | Baseline |
| D-014 | 2026-10-15 | Billing post-MVP | Baseline |

## Implementation decisions made while building

| ID | Date | Decision | Reason |
|---|---|---|---|
| D-101 | 2026-10-15 | API uses Go stdlib `net/http` (1.22+ routing) and `log/slog`; only third-party dependency so far is `pgx/v5` | Minimal dependency surface |
| D-102 | 2026-10-15 | Migrations: embedded plain `.sql` files (`NNNN_name.up.sql` / `.down.sql`) applied by a small in-repo runner tracking `schema_migrations` | No extra tool; inspectable status; reversible |
| D-103 | 2026-10-15 | Job queue starts as a durable Postgres table (per brain §5.1); no Redis | Brain mandate |
| D-104 | 2026-10-15 | Registration returns 409 for an existing email (reveals existence) | Usability until email verification (P1) exists; login and verification pages stay non-enumerating |
| D-105 | 2026-10-15 | Certificate Staff default permissions exclude issue/revoke/deliver/export; these are explicit per-membership grants (COS-008) | Brain §3.3: "issue certificates if permission is granted" |
| D-106 | 2026-10-15 | Validation failures use HTTP 422 with `details` map; malformed/unknown-field bodies use 400 | Brain §5.5 consistency |
| D-107 | 2026-10-15 | Sessions: opaque random token in HttpOnly SameSite=Lax cookie (`__Host-` prefix + Secure in production); only SHA-256 of token stored; absolute TTL (default 12h); status/role read live on every request | Instant suspension/revocation; DB leak yields no usable sessions |
| D-108 | 2026-10-15 | Rate limiting is in-memory per process; client IP is the TCP peer (X-Forwarded-For not trusted) | Fine for one API instance; replace with shared limiter + trusted-proxy config before scaling out or deploying behind a proxy |
| D-109 | 2026-10-15 | CSRF: required `X-CSRF-Protection: 1` header + Origin check on non-safe methods, plus strict single-origin CORS | Works with SameSite=Lax cookies and a separate web origin |
| D-110 | 2026-10-15 | Password hashing: PBKDF2-HMAC-SHA256, 600k iterations, Go stdlib (`crypto/pbkdf2`); `NeedsRehash` supports migration to argon2id later | No new dependency; revisit with `golang.org/x/crypto` |
| D-111 | 2026-10-15 | Platform roles are granted only out-of-band (`api user grant-platform-role`), never via the HTTP API | Prevent self-escalation |
| D-112 | 2026-10-15 | User-chosen infra tweaks: MinIO image `cgr.dev/chainguard/minio:latest`; API build image `golang:1.25-alpine` | Applied by product owner after local testing |
| D-113 | 2026-10-16 | Tenant resolution: the client may send `X-Institute-ID`, but it is only matched against the caller's OWN active memberships. Exactly one membership → header optional; several → header required (400 `institute_required`). Non-member and non-existent institutes return the identical 403 | Brain §6.3: tenant from membership, no enumeration |
| D-114 | 2026-10-16 | Suspended institute: members keep only `*.read` permissions (exports excluded, because they bulk-extract PII); closed institute: no member access | Brain §3.2 "explicit read-only access policy". **Confirmed by product owner 2026-10-16** |
| D-115 | 2026-10-16 | Per-membership `grants` extend role defaults but can never include owner-only (`staff.manage`, `institute.manage`) or platform permissions; enforced in code and by a DB CHECK | No escalation by grant; implements D-105 |
| D-116 | 2026-10-16 | Compile-time tenant scoping: tenant-owned repository methods take a `tenancy.Scope`, constructible only from a resolved `Tenant`. `TrustedScope` is for tests/admin tooling only; `AdminStore` is never reachable from handlers | A forgotten tenant check fails to compile rather than leaking |
| D-117 | 2026-10-16 | PostgreSQL RLS is mandatory for every tenant-owned table, enabled together with a restricted runtime role when the first such table is added (hard gate COS-RLS, blocks M3); hook `tenantdb.WithTenant` shipped; ADR-001 **Accepted** | Production-best: superuser dev DB bypasses RLS and there is nothing to protect yet |
| D-118 | 2026-10-16 | Institutes and memberships were created out-of-band (`api institute ...`, `api seed`) until application review existed. **Superseded in part by D-119/D-120**: the CLI remains for seeding, recovery and closing institutes | Onboarding workflow is a later milestone |
| D-119 | 2026-10-17 | Application lifecycle: draft → submitted → under_review → approved \| rejected \| changes_requested (→ submitted). One open application per user; the requested slug is reserved while an application is submitted/under_review/changes_requested/approved and released on rejection. Enforced by the service, the store contract and partial unique indexes | Prevents duplicate/squatted workspaces and parallel applications |
| D-120 | 2026-10-17 | Approval is one transaction: lock row, create institute + owner membership, mark approved, write audit events; any failure (e.g. slug taken, applicant suspended) changes nothing. Operations never see drafts; reviewers cannot decide their own application; a decision reason of 10–2000 characters is required for reject / request-changes and is shown to the applicant; another user's application is 404, never 403 | Integrity, conflict of interest, no enumeration |
| D-121 | 2026-10-17 | Append-only audit log: `audit_logs` has no foreign keys, DB triggers block UPDATE/DELETE, a `seq` identity column gives a stable order, metadata is sanitized (credential-like keys stripped) and holds ids/states/reasons only. API-driven changes write their event in the same transaction; CLI changes are audited as actor `cli` but NOT transactionally (the command reports an error if the audit write fails). Runtime-role REVOKE is part of COS-RLS | Tamper-evidence and traceability |
| D-122 | 2026-10-17 | Operations may suspend (reason required) and reactivate institutes (`ops.institutes.manage`); closing remains CLI-only. The audit view (`GET /api/v1/ops/audit`) is super_admin only (`ops.audit.read`). Suspended behavior is unchanged from D-114 | Least privilege; closure is effectively irreversible |
| D-123 | 2026-10-17 | MVP onboarding has no document upload: the applicant attests accuracy and authority, and operations verify offline. Email/notification of decisions is deferred (P1); applicants see status in the app. Document upload is COS-011 and needs an object-store adapter | Avoids shipping unscanned-upload risk before storage and malware-scan design exist |

Open product-owner questions: see `PROJECT_BRAIN.md` §17.