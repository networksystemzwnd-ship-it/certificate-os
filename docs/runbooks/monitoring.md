# Runbook: monitoring and alerts

## Health
- `GET /healthz`: the process is up (liveness). Use for restarts.
- `GET /readyz`: the database answers (readiness). Use for load balancers. Never leaks driver errors.

## Metrics
`GET /metrics` (Prometheus text format) is **off** unless `METRICS_TOKEN` (16+ characters) is set; the scraper sends
`Authorization: Bearer <token>`. Put it behind your private network or reverse proxy: do not expose it publicly.
Labels are fixed by the code (method, status class, our own error codes): no paths, users, institutes or addresses.

| Metric | Meaning | Suggested alert |
|---|---|---|
| `cos_http_requests_total{method,class}` | Requests by status class | 5xx share > 2% for 10 min |
| `cos_http_request_duration_seconds` (histogram) | Latency | p95 > 2 s for 10 min |
| `cos_certificate_failures_total{reason}` | Certificates that failed to render/store | any increase over 15 min (PDF rendering broken?) |
| `cos_delivery_attempts_total{outcome,code}` | Email attempts: accepted / retry / failed with our error code | `failed` rate rising, or `code="auth_failed"` / `tls_failed` (provider misconfigured) |
| `cos_delivery_queue{state="active"|"overdue"|"oldest_overdue_seconds"}` | Unfinished deliveries; how many are due; age of the oldest due one | `overdue > 0` and `oldest_overdue_seconds > 600` for 15 min (worker stuck or mail disabled) |

Counters reset when the process restarts (normal for Prometheus). With several API replicas each exposes its own counters: scrape all and sum.
The delivery queue gauge reads the database, so it is the same on every replica.

## Dashboards in the app
- Institute **Overview** tab (per institute) and **Operations > Overview** (platform aggregates). They show business figures, not system health.

## Logs
Structured JSON via `slog` with a `request_id` on every request (also returned to the client in error responses and the `X-Request-ID` header).
Passwords, tokens, SMTP credentials and recipient addresses are never logged. Ship stdout to your log store and keep it per `docs/privacy-retention.md`.

## Backups
Alert when the last successful `restore-check` or dump is older than 26 h (this is a job on your side: the app cannot see your backups).

## Stuck things and what to do
| Symptom | Likely cause | Action |
|---|---|---|
| A bulk job stays "Issuing…" with no progress | API restarted mid-job; lease not yet expired (2 min) | Wait 2 min, then **Resume job** in the Certificates tab |
| Emails stay "Queued" | `EMAIL_PROVIDER=none`, worker not running, or the institute is suspended (rechecked every 15 min) | Check API start log line "email delivery enabled"; `cos_delivery_queue`; see `email-delivery.md` |
| Emails "Failed" with `auth_failed`/`tls_failed` | Provider credentials/port | Fix `SMTP_*`, restart, press **Retry** |
| Issuing returns 503 `render_unavailable` | `PDF_CHROME_PATH` unset or Chromium missing from the image | See `certificate-pdf-rendering.md` |
| `/readyz` 503 | Database down/unreachable | Check the database; the API recovers by itself |
| 429 on `/verify` or login | Per-address rate limits (in-memory, per replica) | Expected under abuse; if legitimate users share one address (NAT), raise limits in code after review |

## Provider outages
Email: deliveries retry automatically (30 s, 2 min, 10 min, 1 h; 5 attempts) and never affect certificates. After an outage, failed ones can be retried
from the UI. Issuing and verification do not depend on the mail provider.

## Abandoned import uploads
An hourly job in the API process removes the rows of student-import uploads left unfinished for 7 days (D-178). It logs `abandoned imports purged count=N`; a repeating `import purge failed` error means the database role lacks migration 0015 (run `api migrate up`).
