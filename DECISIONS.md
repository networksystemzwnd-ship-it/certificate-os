# DECISIONS

Decisions override `PROJECT_BRAIN.md` (see its §0 precedence). Never rewrite history: supersede with a new entry.

| ID | Date | Decision | Status |
|---|---|---|---|
| D-001 | 2026-10-15 | Multi-tenant cloud SaaS | Baseline (confirm in M0) |
| D-002 | 2026-10-15 | Modular monolith + worker | Baseline (confirm in M0) |
| D-003 | 2026-10-15 | Go REST API | Baseline |
| D-004 | 2026-10-15 | React + TypeScript + Vite | Baseline |
| D-005 | 2026-10-15 | PostgreSQL | Baseline |
| D-006 | 2026-10-15 | Docker Compose local env | Baseline |
| D-007 | 2026-10-15 | Store versioned PDF at issuance | Baseline |
| D-008 | 2026-10-15 | Preset templates + configurable fields first | Baseline |
| D-009 | 2026-10-15 | MVP delivery: share links/downloads + email | Baseline |
| D-010 | 2026-10-15 | WhatsApp/SMS post-MVP | Baseline |
| D-011 | 2026-10-15 | Shared DB, tenant-scoped rows | Baseline |
| D-012 | 2026-10-15 | Narrow public verification response | Baseline |
| D-013 | 2026-10-15 | Corrections via replacement/reissue | Baseline |
| D-014 | 2026-10-15 | Billing post-MVP | Baseline |

## Implementation decisions made while building

| ID | Date | Decision | Reason |
|---|---|---|---|
| D-101 | 2026-10-15 | API uses Go stdlib `net/http` (1.22+ routing) and `log/slog`; only third-party dependency so far is `pgx/v5` | Minimal dependency surface |
| D-102 | 2026-10-15 | Migrations: embedded plain `.sql` files (`NNNN_name.up.sql` / `.down.sql`) applied by a small in-repo runner tracking `schema_migrations` | No extra tool; inspectable status; reversible |
| D-103 | 2026-10-15 | Job queue starts as a durable Postgres table (per brain §5.1); no Redis | Brain mandate |

Open product-owner questions: see `PROJECT_BRAIN.md` §17.
