# CertificateOS

Multi-tenant certificate management for offline institutes. Source of truth: [`PROJECT_BRAIN.md`](PROJECT_BRAIN.md).

## Requirements
**Docker 24+ with Compose v2 — nothing else.** Go, Node, Chromium and psql are never needed on your machine: development, tests, linting and verification all run in containers.

## First-time setup
```bash
cp .env.example .env        # includes COMPOSE_FILE so the tooling services below are available
docker compose run --rm tools sh -c "cd services/api && go mod tidy"        # only if services/api/go.sum is missing — commit it
docker compose run --rm tools sh -c "cd apps/web && npm install"            # only if apps/web/package-lock.json is missing — commit it
```
(The generated lock files are written into your working copy by the container and are owned by root on Linux; `sudo chown` them if that matters to you.)

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

Migrations run on API start in development (`MIGRATE_ON_START=true`; refused in production). Manual: `docker compose exec api /api migrate status|up|down`.

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
- **What still needs your verification** (the build sandbox could not run Docker, npm, real email or a phone): `docs/verification-checklist.md`; `docker compose run --rm verify` runs the automated part.
- Behind a reverse proxy set `TRUSTED_PROXIES`; `METRICS_TOKEN` enables `/metrics`; see `.env.example`.

## Tests, lint, verification (all in Docker)
```bash
docker compose run --rm verify          # everything: migrations, Go vet + full suite on a disposable PostgreSQL with RLS and real Chromium, web typecheck/lint/tests/build
docker compose run --rm test-go         # Go only
docker compose run --rm test-web        # web only
docker compose run --rm lint            # gofmt, go vet, eslint, tsc
docker compose run --rm fmt             # gofmt + prettier (writes files)
docker compose run --rm tools bash      # a shell in the toolbox (Go, Node, Chromium, psql)
```
A throw-away `testdb` container (in memory, never your dev database) is started for the Go tests. `make <same names>` are optional shortcuts if you happen to have make.
