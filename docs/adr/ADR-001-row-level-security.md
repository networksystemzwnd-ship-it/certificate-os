# ADR-001: Row-level security as defense in depth for tenant isolation
- Status: Accepted (2026-10-16, product owner: "take the best option for production")
- Date: 2026-10-15
- Decision owner: Product owner

## Context
Brain §6.3 asks us to "consider PostgreSQL row-level security as defense in depth". The primary
control is application-level: every tenant-owned repository method requires a `tenancy.Scope`
(obtainable only from a membership-resolved `Tenant`) and filters by it (COS-008, D-116).

RLS would add a second barrier if a query forgets the filter. Two facts make enabling it *now*
pointless:
1. There are no tenant-owned business tables yet (`institutes`/`institute_memberships` are read
   before the tenant is known, so they cannot sit behind a tenant policy).
2. The local Postgres user in `compose.yaml` is a superuser, and superusers bypass RLS entirely.
   Policies only protect connections made as a restricted role.

## Options considered
1. **Enable RLS now** on memberships/institutes. Breaks tenant resolution; protects nothing in dev.
2. **Defer, but ship the hook** (`tenantdb.WithTenant` sets `app.institute_id` per transaction) and
   decide with the first tenant-owned table. *(chosen)*
3. **Never use RLS.** Simplest, but removes the safety net the brain doc recommends.

## Decision
Ship `internal/platform/tenantdb.WithTenant` now. When the first tenant-owned table is added (M3:
courses/students), in the same migration:
- create a non-superuser, non-owner application role (e.g. `certos_app`) used by the API and worker;
  migrations keep running as the owner;
- `ALTER TABLE ... ENABLE ROW LEVEL SECURITY` **and** `FORCE ROW LEVEL SECURITY`;
- policy: `USING (institute_id = current_setting('app.institute_id', true)::uuid)` and the same
  `WITH CHECK`, so a missing setting matches no rows (fail closed);
- all tenant-owned repository methods run inside `WithTenant`;
- add integration tests proving a connection scoped to institute A cannot read or write B's rows
  *even with the application filter deliberately removed*.

## Production gate (hard requirement)
Accepted option 2 is the production-best path because RLS needs real tenant-owned tables to protect
and a restricted runtime role to be effective. Therefore, **no tenant-owned business table (students,
courses, batches, templates, certificates, ...) may be merged without RLS**. This is recorded as backlog
item COS-RLS (blocking for M3) and as a release-checklist line. Concretely, the first such migration must:
- create the restricted runtime role and switch API/worker `DATABASE_URL` to it (migrations keep the owner role);
- `ENABLE` + `FORCE` RLS with `USING`/`WITH CHECK` on `institute_id = current_setting('app.institute_id', true)::uuid`;
- run all tenant-owned repository access inside `tenantdb.WithTenant`;
- `REVOKE UPDATE, DELETE, TRUNCATE ON audit_logs` from the runtime role (append-only audit, see D-121);
- include the "filter deliberately removed" cross-tenant integration test.

## Consequences
- Until then, isolation rests on the compile-time `Scope` plus cross-tenant HTTP tests
  (`tenancytest.Fixture`), which mutation testing showed do fail when resolution is broken.
- Compose and CI must switch the API to the restricted role at that point.

## Security/privacy implications
Positive: a forgotten `WHERE institute_id` becomes an empty result instead of a data leak.
Platform-operations queries that legitimately span tenants need an explicit, audited path
(separate role or `SECURITY DEFINER` functions) — to be designed with COS-010.

## Migration/rollback implications
Policies are additive and can be dropped per table. Creating the restricted role requires a
reviewed migration plan (brain §10.3).

## Related work items
COS-008 (this decision's origin), M3 records, COS-010 operations access.
