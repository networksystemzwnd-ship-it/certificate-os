# Verification checklist (what only you can run)

The code was built in a sandbox that could not reach the Go and npm registries, run Docker, send real email or use a phone. Everything below was therefore verified only by type-checking against a stub, by hand-run SQL on a real PostgreSQL 16, or not at all. Work through this list once before a pilot and tick it off. Each item says what a failure would mean.

## 1. One command for the automated parts
```bash
docker compose run --rm verify     # Docker only; uses a disposable PostgreSQL container, never your data
```
It runs migrations (0001–0017), provisions the restricted role, then `gofmt`, `go vet`, the whole Go suite with `-race -p 1` against real PostgreSQL **as the restricted role** (so row-level security is part of what is proven), and for the web app `npm ci`, typecheck, lint, tests and build.

If it fails, send me the first failing output. Likely causes, in order: a Postgres store query that only a real pgx run can catch (all Postgres stores were run only against a stub), a frontend test that was syntax-checked but never executed, a lint rule.

## 2. Things a script cannot check
| # | Check | How | If it fails |
|---|---|---|---|
| 1 | First deployment | `docs/runbooks/deployment.md` (note: Docker was never run in the sandbox; the Dockerfile and compose are yours) | Image or compose issue, not application logic |
| 2 | PDF rendering in your image | Chromium in the API image, `PDF_CHROME_PATH`; `docs/runbooks/certificate-pdf-rendering.md`; issue one certificate and download it | Issuing returns 503 until Chromium is configured |
| 3 | A real email | `docs/runbooks/email-delivery.md` with Mailpit, then your provider. Delivery status `accepted` means the server took it, not that it arrived | SMTP settings, TLS mode |
| 4 | QR code | Print or show a certificate and scan it with a phone against your real `PUBLIC_BASE_URL` | Wrong `PUBLIC_BASE_URL`, proxy path |
| 5 | Real spreadsheets | Import one real `.xlsx` (Excel, LibreOffice, Google Sheets export) and one CSV from a real institute | Parser gap: send me the file shape (not the data) |
| 6 | Reverse proxy | Set `TRUSTED_PROXIES` to your proxy and confirm two different clients get separate login limits (D-180) | All users share one limit |
| 6b | Several replicas | Set `RATE_LIMIT_STORE=postgres`, run two API instances, fail logins against one then the other: the limit must hold across both (D-182) | Limits multiply per replica |
| 7 | Backup and restore | `scripts/restore-check.sh` against a real backup on your server (`docs/runbooks/backup-restore.md`) | Backups cannot be trusted until this passes |
| 8 | Monitoring | Scrape `/metrics` with `METRICS_TOKEN`; add the alerts from `docs/runbooks/monitoring.md` | No visibility |
| 9 | Dependency scanning | Add `govulncheck ./...` and `npm audit` to CI | Known-vulnerable dependency goes unnoticed |
| 10 | Security and privacy review | `docs/security-review.md` section 7; lawyer review of `docs/privacy-retention.md` | Not production-ready for real student data |

## 3. Decisions waiting for you
- D-148, D-151 (who may download PDFs), D-155, D-160 (public page fields), D-161 (no PDF on the public page), D-167 (are at-least-once emails acceptable?), the email provider (brain §17 Q9), and the retention periods in `docs/privacy-retention.md`.

## 4. After it passes
Tell me which items passed or failed. Pilot-feedback fixes, bounce handling (needs the provider), a shared rate limiter (needs more than one replica) and the remaining purges (need retention periods) are the next work.
