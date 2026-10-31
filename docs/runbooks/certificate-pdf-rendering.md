# Runbook: certificate PDF rendering

Certificates are rendered to PDF by a pinned headless Chromium that the API starts for each certificate
(`internal/pdf`). Until it is configured, **issuing returns 503 and writes nothing** (no number is used).

## Enable

1. Install a Chromium/Chrome build in the image that runs the API and pin its version (do not use "latest").
2. Set `PDF_CHROME_PATH` to its absolute path, e.g. `/usr/bin/chromium`.
3. Set `PUBLIC_BASE_URL` to the public https origin of the verification pages (it is printed in the QR code
   of every certificate and can never be changed for certificates already issued).
4. Restart the API. The log line `certificate pdf rendering enabled` confirms it. The startup fails if the
   path does not exist.

Example Dockerfile snippet for a Debian/Alpine based runtime stage (adapt to your base image and pin the package):

```dockerfile
# Alpine example: chromium plus fonts so names in Latin and common Indic scripts render.
RUN apk add --no-cache chromium font-noto font-noto-devanagari tzdata
ENV PDF_CHROME_PATH=/usr/bin/chromium
# Run the API as a non-root user so the Chromium sandbox can stay ON (leave PDF_CHROME_NO_SANDBOX unset).
USER 10001
```

If the API must run as root inside the container, set `PDF_CHROME_NO_SANDBOX=true`. This is weaker: the
HTML given to the browser is already escaped, carries a CSP with `default-src 'none'`, and the browser runs
with DNS disabled and an empty environment, but prefer a non-root user.

## Hardening already in place

- Input is the escaped certificate page built from the immutable snapshot, never live student data.
- Chromium runs with a throw-away profile directory, `HOME` set to it, no inherited environment (no secrets),
  `--host-resolver-rules=MAP * ~NOTFOUND` (no DNS, so no network), a 30 s timeout, at most
  `PDF_MAX_CONCURRENT` renders at once and a 5 MB output cap.
- The output must start with `%PDF-`; anything else is a failed render.

## Troubleshooting

| Symptom | Cause / action |
|---|---|
| Issue returns 503 `render_unavailable` | `PDF_CHROME_PATH` is not set. Set it and restart. |
| Issue returns 502 `render_failed`, certificate listed as `failed` | Browser crashed or timed out. Check the API log for the request id; the number of a failed certificate is never reused. Issue again. |
| Boxes instead of letters in the PDF | The image has no font for that script. Install a Noto font for it. |
| Chromium exits immediately in a container | Running as root without `PDF_CHROME_NO_SANDBOX=true`, or `/dev/shm` too small (the flag `--disable-dev-shm-usage` is already set). |
| Pending certificates that never finish | The process died mid-render. A new attempt after 5 minutes abandons the old one automatically. |

## Verify a deployment

Issue a test certificate for a test enrollment, download it, and check: one A4 page, the name and number
are readable text, and scanning the QR code opens `PUBLIC_BASE_URL/verify/<id>` (the verification page itself
arrives in M5).

## Bulk jobs (M4c)

Bulk issuing runs inside the API process and uses the same renderer, one certificate at a time (so `PDF_MAX_CONCURRENT`
also bounds it). Roughly one to two seconds per certificate with Chromium: a batch of 100 takes a few minutes.

- If the API restarts while a job is running, the job stays `processing`. Two minutes after the last progress, press
  **Resume job** in the Certificates tab (or `POST /api/v1/bulk-jobs/{id}/resume`). Finished certificates are kept, the
  half-done one is redone, and no student gets two certificates.
- A stopped deployment waits up to 15 seconds for the running render to finish before exiting.
- Only one job per institute runs at a time; **Cancel job** frees the slot.
- Failed students are listed with a short reason; fix the cause (for example the renderer) and press **Retry failed**.
