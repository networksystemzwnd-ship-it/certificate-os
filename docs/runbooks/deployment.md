# Runbook: deployment (pilot)

This describes what a pilot deployment needs. It is a checklist, not a claim that a particular hosting product has been tested:
**the build sandbox cannot run Docker, so the container images and compose file have only been exercised by you locally.**

## Components
- `api` (Go, one container) with Chromium available for PDF rendering (see `certificate-pdf-rendering.md`; the stock API image does not include Chromium yet).
- `web` (static build served by any web server/CDN) on the **same site** as the API or behind the same reverse proxy (cookies are `SameSite=Lax`, CORS allows one origin).
- PostgreSQL 16 (managed service recommended: automated backups, PITR, patching).
- Reverse proxy / load balancer terminating **TLS** (required: the session cookie is `Secure` and `__Host-` prefixed in production).
- Optional: mail provider (SMTP), Prometheus scraper.

## Settings that must be set in production (`ENV=production`)
| Setting | Why |
|---|---|
| `DATABASE_URL` (the restricted runtime login, default role `certos_app`, **not** the owner) and `MIGRATE_DATABASE_URL` (owner; production refuses to start if both are the same) | RLS only protects if the app connects as the restricted role |
| `APP_DB_USER` / `APP_DB_PASSWORD` | Login created by `api db provision-runtime-role`; strong, unique password from a secret store |
| `WEB_ORIGIN` | Exact https origin of the web app (CORS + CSRF origin check) |
| `PUBLIC_BASE_URL` | https origin printed in QR codes and emails; **changing it later breaks already-printed QR codes** unless the old address keeps working |
| `PDF_CHROME_PATH` (+ sandbox settings) | Issuing is disabled without it |
| `EMAIL_PROVIDER=smtp`, `EMAIL_FROM`, `SMTP_*` | Only when you want to email certificates (`email-delivery.md`) |
| `METRICS_TOKEN` | Only if you scrape metrics |
Never use the `.env.example` development passwords. `SMTP_TLS=none` is refused in production.

## First deployment
1. Provision the database; create the owner login and set `MIGRATE_DATABASE_URL`.
2. Run, as one-off jobs with the owner connection: `api migrate up`, then `api db provision-runtime-role` (creates/updates the `APP_DB_USER` login inside the `certos_runtime` group). `MIGRATE_ON_START` is a development convenience and is refused in production.
3. Create the first platform account out-of-band: register a user, then `api user grant-platform-role EMAIL super_admin` (platform roles are never granted over HTTP).
4. Check `/readyz`, open the app over https, sign in, confirm the cookie is `Secure` and `HttpOnly`.
5. Run `scripts/restore-check.sh` once (see `backup-restore.md`) and schedule backups **before** inviting the pilot institute.
6. Issue one test certificate, scan its QR code with a phone, check the verification page, send one test email.

## Upgrades
1. Take a backup (and note the current migration version: `api migrate status`).
2. Run `api migrate up` as a job with the new image (before starting the new instances). Migrations are forward-compatible with the previous
   application version where practical; destructive down files are for development.
3. Watch `/readyz`, error rate and `cos_delivery_queue` for 15 minutes.
4. Roll back by redeploying the previous image. If a migration must be undone, restore the backup (rehearsed) rather than running a `down` file against production data.

## Scaling notes (known limits)
- Rate limits are in-memory per replica (D-108): with N replicas the effective limit is N times higher. Acceptable for a pilot; a shared limiter is on the backlog.
- Bulk jobs run in the API process (D-155); the email worker runs in every replica and is safe (one owner per delivery).
- PDFs are stored in PostgreSQL: expect roughly 50-150 KB per certificate; size the database and backups accordingly.

## Security checklist before the pilot
See `docs/security-review.md` (open items there are launch blockers or accepted risks, decided by the product owner).
