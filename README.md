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

## Tests
`make test` · `make lint`. Migration integration tests run when `TEST_DATABASE_URL` points to a disposable Postgres.

## Troubleshooting
- API exits at start: check `.env` (missing `DATABASE_URL` fails by design).
- Reset local data: `docker compose down -v`.
