# Security

## Reporting
Report vulnerabilities privately to the project owner (add contact before pilot). Do not open public issues for security problems.

## Operating rules
- No secrets in source control, images, frontend bundles, logs, or fixtures.
- Server-side authorization on every protected operation; fail closed.
- Tenant context derives from authenticated membership.
- Parameterized SQL only; validate all input server-side.
- Public verification exposes only the approved narrow response.
See `PROJECT_BRAIN.md` §4.1 and §6.3.
