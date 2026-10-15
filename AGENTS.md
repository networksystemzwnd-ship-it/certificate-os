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

## Before reporting done
- Run `make lint test` (and `make e2e` when relevant).
- Review the diff for secrets and unrelated changes.
- Update `BACKLOG.md` / `ROADMAP.md` only if exit criteria are truly met.
- Report: summary, files changed, commands run, security/tenancy implications, limitations, next task.

## Stop and ask when
Requirements conflict; tenant/public data exposure is possible; a migration is destructive; certificate numbering/revocation/replacement semantics are unclear; a provider needs credentials/live send; someone asks to bypass tests/audit/access control; scope exceeds the milestone.

## Commands
`make dev` · `make test` · `make lint` · `make migrate` · `make seed` · `make e2e` · `make down`
