# Runbook: emailing certificates

Certificates are emailed by a background worker inside the API process. Sending is **off** until you configure it
(`EMAIL_PROVIDER=none` is the default; the app answers "Email sending is not set up on this server yet").

## 1. Try it locally with Mailpit

Mailpit is a fake inbox that catches every message (nothing leaves your machine). If your compose file runs it, set in `.env`:

```
EMAIL_PROVIDER=smtp
EMAIL_FROM=no-reply@certificateos.local
SMTP_HOST=mailpit
SMTP_PORT=1025
SMTP_TLS=none
```

Restart the API. Give a student an email address, open **Certificates**, press **Email CERT-…**. Within ~5 seconds the
delivery shows **Accepted by the mail server** and the message, with the PDF attached, appears in the Mailpit inbox.
`SMTP_TLS=none` is refused when `ENV=production`.

## 2. Production

```
EMAIL_PROVIDER=smtp
EMAIL_FROM=Certificates <no-reply@your-domain>   # an address your provider lets you send from
SMTP_HOST=<provider host>
SMTP_PORT=587            # 465 with SMTP_TLS=tls
SMTP_TLS=starttls
SMTP_USERNAME=...        # from your provider
SMTP_PASSWORD=...        # secret: environment/secret store only, never committed, never logged
```

Before sending real certificates:
- Set up SPF, DKIM and DMARC for the sender domain with your provider; otherwise messages land in spam or are rejected.
- Send one to yourself and check the attachment opens and the verification link works.
- Remember `PUBLIC_BASE_URL` is the origin in the message's verification link and in the QR code.

## 3. What the statuses mean

| Status | Meaning |
|---|---|
| Queued | Waiting for the worker (or waiting to retry after a temporary problem). |
| Sending | A worker is sending it right now. |
| Accepted by the mail server | The server took the message. It cannot confirm the inbox received it (no bounce/receipt handling yet). |
| Failed | Gave up. The details say why; **Retry** starts a fresh round using the student's current address. |

Temporary problems (network, timeout, "try later") are retried after 30 s, 2 min, 10 min, 1 h, up to 5 attempts. Permanent ones
(rejected address, bad login, TLS problem) fail immediately.

## 4. Troubleshooting (error codes shown in the panel)

| Code | Usual cause | What to do |
|---|---|---|
| `connect_failed`, `timeout` | Wrong host/port, firewall, provider outage | Check `SMTP_HOST`/`SMTP_PORT`, outbound access from the API container |
| `tls_failed` | Provider does not offer STARTTLS on that port, or the certificate does not match the host | Use the provider's documented port and `SMTP_TLS` mode |
| `auth_failed` | Wrong username/password | Re-check the credentials (never paste them in chat or tickets) |
| `provider_rejected` | Sender not allowed, message blocked | Verify the sender domain/address with the provider |
| `recipient_rejected` | The student's address does not exist | Fix it on the student record, then **Retry** |
| `certificate_not_issued` | Revoked or replaced before sending | Nothing to do; email the replacement instead |
| `interrupted` | The API restarted repeatedly while sending | Check API stability, then **Retry** |

## 5. Known limits

- Sending is **at-least-once**: if the server dies after the mail server accepted a message but before the result was saved, the
  student can receive the email twice.
- A suspended institute's queued emails wait and are sent after reactivation.
- With several API replicas every replica runs a worker; each delivery is owned by exactly one at a time.
