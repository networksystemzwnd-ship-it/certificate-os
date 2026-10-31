import { useCallback, useEffect, useState } from 'react';
import { ApiError } from '../../lib/api';
import { deliveriesApi, type Delivery, type DeliveryStatus } from './api';

// "Accepted" is deliberately not called "delivered": the mail server took the message, but nothing here can know
// that it reached the inbox.
const STATUS_LABEL: Record<DeliveryStatus, string> = {
  queued: 'Queued', sending: 'Sending…', accepted: 'Accepted by the mail server', failed: 'Failed',
};

export const DELIVERY_ERROR: Record<string, string> = {
  connect_failed: 'Could not reach the mail server.',
  timeout: 'The mail server did not answer in time.',
  tls_failed: 'A secure connection to the mail server could not be made.',
  auth_failed: 'The mail server did not accept our login.',
  provider_temporary: 'The mail server asked us to try again later.',
  provider_rejected: 'The mail server rejected the message.',
  recipient_rejected: 'The email address was rejected. Check the student’s email address.',
  invalid_message: 'The message could not be built.',
  certificate_not_issued: 'The certificate was revoked or replaced before it was sent.',
  attempts_exhausted: 'Gave up after several attempts.',
  interrupted: 'The server stopped repeatedly while sending.',
  storage_error: 'The certificate file could not be read.',
  unknown: 'The message could not be sent.',
};

export const deliveryError = (code?: string) => (code ? DELIVERY_ERROR[code] ?? DELIVERY_ERROR.unknown : '');

function message(e: unknown): string {
  return e instanceof ApiError || e instanceof Error ? e.message : 'Something went wrong.';
}

const active = (s: DeliveryStatus) => s === 'queued' || s === 'sending';

/** Recent email deliveries with their honest status and a retry for failed ones. Polls while something is in flight. */
export function DeliveryPanel({ canDeliver, refreshKey = 0, pollMs = 5000 }: { canDeliver: boolean; refreshKey?: number; pollMs?: number }) {
  const [items, setItems] = useState<Delivery[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setItems((await deliveriesApi.list(10)).deliveries);
      setError(null);
    } catch (e) {
      setError(message(e));
    }
  }, []);

  useEffect(() => { void load(); }, [load, refreshKey]);

  const anyActive = items?.some((d) => active(d.status)) ?? false;
  useEffect(() => {
    if (!anyActive) return;
    const t = setInterval(() => { void load(); }, pollMs);
    return () => clearInterval(t);
  }, [anyActive, pollMs, load]);

  const retry = async (d: Delivery) => {
    setBusy(d.id);
    try {
      await deliveriesApi.retry(d.id);
      await load();
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(null);
    }
  };

  return (
    <section aria-labelledby="deliv-h">
      <h4 id="deliv-h">Email deliveries</h4>
      <p><small>“Accepted” means the mail server took the message. It cannot confirm that the email reached the inbox.</small></p>
      {error && <p role="alert">{error}</p>}
      {items && items.length === 0 && <p>No certificates have been emailed yet.</p>}
      {items && items.length > 0 && (
        <table aria-label="Email deliveries">
          <thead><tr><th scope="col">Certificate</th><th scope="col">To</th><th scope="col">Status</th><th scope="col">Details</th><th scope="col">Actions</th></tr></thead>
          <tbody>
            {items.map((d) => (
              <tr key={d.id}>
                <td>{d.certificate_number}</td>
                <td>{d.recipient}</td>
                <td>{STATUS_LABEL[d.status]}</td>
                <td>
                  {d.status === 'queued' && d.last_error ? `Will try again (attempt ${d.attempts} of ${d.max_attempts} failed): ${deliveryError(d.last_error)}` : ''}
                  {d.status === 'failed' ? deliveryError(d.last_error) : ''}
                  {d.status === 'accepted' && d.attempts > 1 ? `After ${d.attempts} attempts.` : ''}
                </td>
                <td>{canDeliver && d.status === 'failed' && <button type="button" disabled={busy === d.id} onClick={() => retry(d)}>Retry {d.certificate_number}</button>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
