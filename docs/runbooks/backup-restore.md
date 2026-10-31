# Runbook: backups and restore

Everything that matters lives in **one PostgreSQL database**: records, certificates **and their PDFs** (stored as `bytea`),
deliveries and the audit log. One consistent database backup is therefore a complete backup of the application data.
(Application-document uploads and logo/signature images are not built yet; when they arrive they will need their own line here.)

## What to back up

| Item | How | Notes |
|---|---|---|
| The database | `pg_dump --format=custom` (logical) and, for point-in-time recovery, continuous WAL archiving / managed-provider PITR | Take the dump as the **owner or a superuser** (see "Row-level security and backups") |
| Secrets (`.env`, provider credentials) | Your secret manager | Never inside the database backup, never in git |
| Container images / source | Your registry and git | Rebuildable |

Recommended minimum for the pilot: **daily** logical dump, kept 14 days, stored off the database host and encrypted; plus your
provider's PITR if available. Decide the real retention with `docs/privacy-retention.md` (backups contain personal data).

## Row-level security and backups

Every tenant table has `FORCE ROW LEVEL SECURITY`. `pg_dump` run by an ordinary role would see only the rows that role may see, so:
- take backups with the **owner/superuser** (it bypasses RLS) or a dedicated role with `BYPASSRLS` used only for backups;
- the application's runtime login (`certos_runtime`) must **never** have `BYPASSRLS` and is not used for backups.
`pg_dump` fails loudly ("query would be affected by row-level security policy") instead of silently dumping partial data when
run with an unsuitable role: treat that error as a misconfigured backup job.

## Take a backup

```
pg_dump --format=custom --no-owner --dbname="$DATABASE_OWNER_URL" --file="certos-$(date -u +%Y%m%dT%H%M%SZ).dump"
```

## Rehearse the restore (do this before the pilot, then monthly)

`scripts/restore-check.sh` dumps a database, restores it into a throw-away database on the same server and verifies that the copy is
faithful: every table's row count and **content checksum**, row-level-security flags and policies, triggers, indexes, runtime-role
grants, and that the restricted role still sees **zero** tenant rows without a tenant context.

```
PGHOST=... PGPORT=... PGUSER=<owner> PGPASSWORD=... scripts/restore-check.sh <database_name>
```

Exit code 0 and the line `RESTORE VERIFIED` mean the dump can be restored and is complete. Any difference prints a diff and exits 1.
It was exercised against PostgreSQL 16 during development (it detects a changed row and a disabled RLS flag). A rehearsal on
YOUR server and data is still required: record the date, the dump size and the result in your operations log.

Note: the checksum compares rows as text, which is fine for pilot-sized data; certificate PDFs make the dump large, so run the
rehearsal off-peak.

## Restore for real

1. **Stop writes**: stop the API (`docker compose stop api`) so no deliveries/jobs run against a half-restored database.
2. Make sure the roles exist on the target server. Roles are cluster-level and are **not** part of a database dump, and the dump's grants refer to them: on a NEW server create the group first (`psql -c "CREATE ROLE certos_runtime NOLOGIN"`; the migrations normally create it, but a restore does not run them). The login (`APP_DB_USER`, default `certos_app`) is created afterwards by `api db provision-runtime-role`.
3. Create an empty database and restore:
   ```
   createdb certos
   pg_restore --no-owner --exit-on-error --dbname=certos certos-YYYYMMDDTHHMMSSZ.dump
   ```
4. If the runtime login did not exist yet, run `api db provision-runtime-role` (idempotent) with the owner connection.
5. Run `scripts/restore-check.sh certos` against the restored database (it re-dumps and re-restores: proves the restored copy is itself backup-able),
   then start the API and check `/readyz`.
6. Smoke test: sign in, open an issued certificate's PDF, open its `/verify/<id>` page.
7. After restoring from an older backup, remember: certificates issued after that backup no longer exist, but their QR codes/PDFs may have been
   handed out. Their numbers will be reused by the counter. Record the gap and contact affected institutes (see the incident section of `docs/privacy-retention.md`).

## Failed migration

Migrations are versioned; `api migrate status` shows the state, `api migrate down` reverses the latest (down files are destructive for new tables:
read them first). If `up` fails half way, the failing migration ran in a transaction and left nothing behind: fix the cause and rerun. If in doubt,
restore the last backup into a scratch database and rehearse there first.
