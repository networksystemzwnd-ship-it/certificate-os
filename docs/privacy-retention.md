# Privacy and data retention (DRAFT)

**Status: draft written by the builder for discussion. It is NOT legal advice and does not claim compliance with any law or standard.** The retention periods below are proposals marked *(decide)*; they must be reviewed by a lawyer and approved by the product owner before production (brain §4.2).

## 1. Data inventory
| Data | Where | Personal data? | Notes |
|---|---|---|---|
| User account: email, password hash, status, platform role | `users`, `sessions` (token hash only) | Yes | Passwords never stored in clear |
| Institute application: contact, institute details, review notes | `institute_applications` | Yes | Documents upload not built (COS-011) |
| Institute, memberships, grants, invitations (email, token hash) | tenant tables | Yes | |
| Students: name, email, phone, enrollments, grade, dates | tenant tables | Yes | Student email is the delivery address |
| Certificates: snapshot (name, course, dates), number, public id | `certificates` | Yes | Snapshots are immutable by design |
| Certificate PDFs and SHA-256 | PostgreSQL (`certificate_files`) | Yes | Private; only members with `certificates.read` |
| Deliveries and attempts: recipient email, status, error code | `deliveries`, `delivery_attempts` | Yes | Recipient masked in API; no provider text |
| Audit log | `audit_logs` | Minimal | Ids, codes, field names; no names, emails, tokens |
| Verification events | `verification_events` | No direct identifiers | Certificate id, result, time; no IP, no user agent |
| Request logs | stdout | Possible IPs | Request id, method, status; no bodies |

## 2. Principles in the code today
- Collect only what onboarding and operations need.
- Public page shows a narrow field list (D-160); no PDF (D-161).
- Metrics and audit metadata carry no personal data.
- Nothing is hard-deleted by the application: records are archived, certificates are revoked or replaced.

## 3. Retention proposals *(decide)*
| Data | Proposal |
|---|---|
| Issued certificates, PDFs, snapshots | Kept while the institute is active and for a period after closure agreed with the institute *(decide: e.g. X years)*, because verifiers rely on them |
| Student records | Kept while the institute is active; archive on request |
| Uploaded import rows (`student_imports`) | Unfinished uploads: rows removed automatically after 7 days (D-178). Finished imports keep rows needing attention for the error report *(decide: purge after N days)* |
| Delivery attempts | Candidate for purge after N months *(decide)* |
| Verification events | Candidate for purge after N months *(decide)* |
| Audit log | Kept for the audit period *(decide)*; append-only, so purge needs a deliberate admin procedure not built yet |
| Sessions | Expire after 12 h; expired rows may be purged (not automated yet) |
| Rejected applications | *(decide)* |
| Backups | Contain everything above; retention of backups bounds true deletion time *(decide)*. See `runbooks/backup-restore.md` |

## 4. Rights and workflows (not built)
- **Export of an institute's data**: only the certificate ZIP with manifest exists (D-158). A student CSV export and a full export are backlog items.
- **Correction**: students are editable; certificates are corrected through replacement (D-164).
- **Deletion / erasure requests**: not built. The database forbids deleting certificates for the runtime role. A manual, audited procedure run by an operator with the owner connection must be designed with legal input, including how it interacts with already-issued certificates and backups.
- **Account closure**: suspension exists (D-114). A closure workflow with an export window and a retention period is not built.

## 5. Processors and transfers *(decide)*
Hosting provider, email provider (open, brain §17 Q9) and backup storage all process this data. Locations, contracts and notices are for the product owner and a lawyer.

## 6. Incident response (draft)
1. **Detect**: alerts in `runbooks/monitoring.md`, audit log, reports to the security contact (SECURITY.md; contact still to add).
2. **Contain**: suspend the affected institute or disable users (CLI); rotate `APP_DB_PASSWORD`, `SMTP_*`, `METRICS_TOKEN`; sessions can be invalidated by deleting rows with the owner connection.
3. **Assess**: use request ids, audit events and delivery/verification tables to find what was accessed. Metrics and audit carry no personal data, so the scope must be derived from tenant tables.
4. **Notify**: whether and whom to notify (institutes, students, authorities) and within what time is a legal question *(decide, with a lawyer)*.
5. **Recover and learn**: restore with `runbooks/backup-restore.md`, write a post-incident note, add a test for the cause.
