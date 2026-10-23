# AGENTS.md — Instructions for coding agents

Read `PROJECT_BRAIN.md` (especially §12) before changing anything. Summary:

## Before coding
1. Read `PROJECT_BRAIN.md`, `DECISIONS.md`, `ROADMAP.md`, and the relevant `BACKLOG.md` item.
2. Inspect the repo; don't assume files/libraries exist.
3. If a critical decision is missing, write a short decision request — don't invent behavior.

## While coding
- One bounded work item at a time; follow existing patterns.
- Handlers parse/authenticate/map errors only. Business rules live in services. SQL lives in repositories.
- Tenant context comes from authenticated membership, never from a client-supplied tenant ID alone.
- Parameterized SQL only. Validate all input server-side. Fail closed on authorization.
- No secrets in code, fixtures, logs, or frontend bundles. No production data locally.
- No new dependency without a one-line justification in the PR/changelog.
- Never weaken tests, validation, or permissions to get green.
- Update migrations, OpenAPI (`docs/api/openapi.yaml`), and tests alongside code.

## Tenancy rules (COS-008)
- Tenant routes use `tenancyHandler.Guard(permission, handler)`; never read an institute ID from a path, body or header yourself.
- Every tenant-owned repository method takes `tenancy.Scope` and filters EVERY query by `scope.InstituteID()`.
- `tenancy.TrustedScope` and `tenancy.AdminStore` are for tests and CLI only. Using them in handler/service code needs explicit review.
- Every new tenant-owned resource family MUST add cross-tenant negative tests using `tenancytest.NewFixture` (users of A must not read or write B, including by ID guessing and by header).
- Check permissions with `Tenant.Can(perm)` (it already applies suspended-institute read-only rules), not role names.

## Database access rules (COS-RLS, ADR-001)
- The API/worker connect as the restricted runtime role (`DATABASE_URL`); only migrations use the owner (`MIGRATE_DATABASE_URL`). Production refuses to start if the runtime role is a superuser or bypasses RLS.
- A new table needs an explicit `GRANT ... TO certos_runtime` in its migration (no default privileges), granting only what the app uses.
- A tenant-owned table (has `institute_id UUID NOT NULL`) MUST `ENABLE` + `FORCE ROW LEVEL SECURITY` with a policy on `institute_id = current_setting('app.institute_id', true)::uuid` (USING and WITH CHECK). `TestEveryTableIsClassified` fails otherwise; adding a table to its `globalTables` list is a security-review decision.
- Tenant-owned repository code runs inside `tenantdb.WithTenant(ctx, pool, scope, fn)` and still filters by `scope.InstituteID()`.
- Every tenant-owned table needs a "filter deliberately removed" cross-tenant test.

## Before reporting done
- Run `make lint test` (and `make e2e` when relevant).
- Review the diff for secrets and unrelated changes.
- Update `BACKLOG.md` / `ROADMAP.md` only if exit criteria are truly met.
- Report: summary, files changed, commands run, security/tenancy implications, limitations, next task.

## Stop and ask when
Requirements conflict; tenant/public data exposure is possible; a migration is destructive; certificate numbering/revocation/replacement semantics are unclear; a provider needs credentials/live send; someone asks to bypass tests/audit/access control; scope exceeds the milestone.

## Commands
`make dev` · `make test` · `make lint` · `make migrate` · `make seed` · `make e2e` · `make down`
