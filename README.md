# CertificateOS

Multi-tenant certificate management for offline institutes. Source of truth: [`PROJECT_BRAIN.md`](PROJECT_BRAIN.md).

## Requirements
Docker 24+ with Compose v2; Go 1.24+ and Node 22+ for running tests outside Docker.

## First-time setup (needs network access to the Go and npm registries)
```bash
cd services/api && go get github.com/jackc/pgx/v5 && go mod tidy   # creates go.sum — commit it
cd ../../apps/web && npm install                                    # creates package-lock.json — commit it
```

## Run
```bash
cp .env.example .env
docker compose up --build
```
| Service | URL |
|---|---|
| Web | http://localhost:5173 |
| API health / ready | http://localhost:8080/healthz · /readyz |
| Mailpit | http://localhost:8025 |
| MinIO console | http://localhost:9001 |

Migrations run on API start in development (`MIGRATE_ON_START=true`; refused in production). Manual: `make migrate`, or `go run ./cmd/api migrate status|up|down` in `services/api`.

## Local accounts
`make seed` (or `docker compose exec api /api seed`) creates `superadmin@local.test`, `ops@local.test`, `owner@local.test`
with the conspicuously non-production password printed by the command. Refused when `APP_ENV=production`.
Seed tenants: **Demo Institute** (`owner@`, `admin@`, `staff@`, `multi@` as staff) and **Other Institute**
(`owner2@`, `multi@` as admin) — log in as `multi@local.test` to try the institute picker, or compare what
`owner@` and `owner2@` can see. Manage institutes out-of-band with `docker compose exec api /api institute ...`.
Grant roles out-of-band: `docker compose exec api /api user grant-platform-role EMAIL ops`.

## Database roles (row-level security)
The API connects as the restricted login role `certos_app` (member of `certos_runtime`; no DELETE, append-only audit log, row-level security on every tenant table). Migrations and role provisioning use the owner connection `MIGRATE_DATABASE_URL`. In development `MIGRATE_ON_START=true` does both automatically; elsewhere run `api migrate up` then `api db provision-runtime-role` with the owner URL. **If you already have a local database from an earlier version, run `docker compose down -v` once** so the new roles are created cleanly. In production the API refuses to start if its role is a superuser or bypasses RLS (ADR-001).

## What it does today
Institute onboarding and operations review; institute workspace with students, courses, batches, CSV/XLSX import, CSV export, certificate templates, single and bulk certificate issuing (PDF with QR code, ZIP download), replacement and revocation, public verification page (`/verify/<id>`), email delivery with retry, staff invitations, dashboards, audit log. Decisions are in `DECISIONS.md`, work items in `BACKLOG.md`, the OpenAPI description in `docs/api/openapi.yaml`.

## Operating it
- Deployment, backup/restore, monitoring, email and PDF runbooks: `docs/runbooks/`.
- Security self-review: `docs/security-review.md`. Privacy and retention (DRAFT, not legal advice): `docs/privacy-retention.md`.
- **What still needs your verification** (the build sandbox could not run Docker, npm, real email or a phone): `docs/verification-checklist.md`; `make verify` runs the automated part.
- Behind a reverse proxy set `TRUSTED_PROXIES`; `METRICS_TOKEN` enables `/metrics`; see `.env.example`.

## Tests
`make test` · `make lint`. Postgres integration tests run when `TEST_DATABASE_URL` (owner) points to a disposable Postgres; set `TEST_APP_DATABASE_URL` (the `certos_app` login) as well so the store contracts run under row-level security.

## Troubleshooting
- API exits at start: check `.env` (missing `DATABASE_URL` fails by design).
- Reset local data: `docker compose down -v`.
