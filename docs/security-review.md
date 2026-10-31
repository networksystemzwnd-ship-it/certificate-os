# Security review (self-review, M6b)

Status: **self-review by the builder, not an independent assessment.** It lists what is implemented, where the evidence is, and what is still open. It makes no claim of compliance with any standard or law. An external penetration test has not been done.

Evidence legend: **T** = automated test, **S** = checked by hand on real PostgreSQL 16 in the build sandbox, **R** = reviewed in code only, **U** = needs your verification (could not be run in the build sandbox).

## 1. Authentication and sessions
| Control | Evidence |
|---|---|
| Passwords hashed with PBKDF2-HMAC-SHA256, 600k iterations, rehash supported (D-110) | T |
| Session token random, only its SHA-256 stored, 12 h absolute TTL, status/role read live every request (D-107) | T |
| Cookie HttpOnly, SameSite=Lax, `__Host-` prefix and Secure in production (D-107) | T / R |
| Login and register rate limits (D-108) | T |
| Platform roles can only be granted by CLI, never through HTTP (D-111) | T |

## 2. Request integrity
| Control | Evidence |
|---|---|
| CSRF: `X-CSRF-Protection: 1` header + Origin check on non-safe methods, single-origin CORS (D-109) | T |
| Security headers and CSP set in `httpx/middleware.go` | T / R |
| Parameterised SQL only; server-side validation of all input | R |
| Uniform error envelope, no stack traces or provider text to clients (D-169) | T |

## 3. Authorisation and tenancy
| Control | Evidence |
|---|---|
| Every protected route goes through `tenancy.Guard(permission, ...)`; tenant derived from membership, never from the request body | T |
| Compile-time `tenancy.Scope`; `TrustedScope` used only by the delivery worker (D-171) | R |
| PostgreSQL row-level security, `FORCE`, restricted runtime role without DELETE or table ownership (ADR-001) | T (table-classification test), S |
| Narrow extra policies: public lookup (D-162) and worker scan on a PII-free queue (D-171) | S |
| Suspended institute is read-only; exports blocked (D-114) | T |
| Cross-institute purge of abandoned imports uses two narrow policies, only while `app.worker = 'purge'` (D-178) | S |
| Foreign-tenant access returns nothing (dashboards, records, certificates, deliveries) | T, S |

## 4. Certificates and public exposure
| Control | Evidence |
|---|---|
| Public verification returns only the approved fields; same 404 for unknown/pending/failed; id shape checked first; 60 req/min per peer; never cached (D-160, D-162) | T |
| No PDF download from the public page (D-161) | T |
| Immutable snapshots and template versions enforced in the database | T, S |
| Revocation needs a reason; replacement never overwrites (D-164) | T |
| Verification events store no IP or user agent (D-163) | R |

## 5. Email
| Control | Evidence |
|---|---|
| STARTTLS mandatory, certificate verification, plain text refused in production, header-injection sanitising (D-170) | T |
| Secrets from the environment only; errors reduced to codes; recipients masked (D-169) | T |
| A real send through a provider | **U** (see `docs/runbooks/email-delivery.md`) |

## 6. Audit, logs, metrics, backups
| Control | Evidence |
|---|---|
| Audit log append-only (trigger + no UPDATE/DELETE grant), metadata ids/codes only, keys containing "token"/"password" stripped | T, S |
| `/metrics` disabled unless `METRICS_TOKEN` (≥16 chars) is set; constant-time bearer check; labels are method and status class only, never paths, ids or emails (D-174) | T |
| Restore rehearsal compares row counts, content checksums, RLS flags, policies, triggers, grants and checks the runtime role sees no data without a tenant (D-175) | S |
| Production startup refuses a table-owner runtime connection | T |

## 7. Open items (not done)
1. **Dependency scanning** (Go modules, npm) is not set up; the build sandbox could not reach the registries. Add `govulncheck` and `npm audit` to CI.
2. **Rate limiters are per process** (D-108). Behind a proxy or with several replicas they are weaker; a shared limiter and trusted-proxy configuration are needed before scaling out.
3. **Uploads** (COS-011) are not built, so there is no upload scanning to review yet.
4. **No real-provider email test**, no bounce handling.
5. **No external penetration test** and no independent review of the RLS policies.
6. **Postgres stores through real pgx**, the frontend (tests, lint, typecheck) and the Docker image were not run in the build sandbox (**U**).
7. **TLS termination, HTTP-to-HTTPS redirect, HSTS** depend on the reverse proxy you deploy (see `docs/runbooks/deployment.md`).
8. Account lockout/MFA are not implemented; login is only rate limited.
9. Chromium for PDF rendering runs on content built from the institute's own data; `PDF_CHROME_NO_SANDBOX` must be understood before use (`certificate-pdf-rendering.md`).
