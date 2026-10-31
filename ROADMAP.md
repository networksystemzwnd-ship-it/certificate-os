# ROADMAP

| Milestone | Status |
|---|---|
| M0 Project foundation | In progress (COS-001..006 built; exit gate NOT met: pgx/npm deps never installed, so migrations, cmd/api, web build/tests and docker build are unverified) |
| M1 Identity and tenancy | In progress (COS-007, COS-008 built and in review; exit gate needs your Postgres/frontend run) |
| M2 Institute onboarding and operations | In progress (COS-009/010 built; needs your Postgres/frontend verification; COS-011 document upload deferred; COS-RLS gate built, see M3) |
| M3 Institute records | In progress (COS-RLS, M3a courses/batches/students/enrollments and M3b CSV import and M3c staff invitations built; needs your verification; XLSX import still to do) |
| M4 Templates and issuance | Built, needs your verification (M4a templates, M4b single issuing, M4c bulk jobs with progress/retry/ZIP; exit gate of 100 certificates without duplicates passes in tests; logo/signature images and the Docker/Chromium image still to do) |
| M5 Verification and delivery | Not started |
| M6 Pilot readiness | Not started |
| M7 Post-MVP | Not started |

Rule: do not start certificate PDF generation before COS-007 and COS-008 pass their quality gates.
