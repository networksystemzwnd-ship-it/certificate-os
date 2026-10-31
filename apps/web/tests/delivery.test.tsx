import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { BulkPanel } from '../src/features/certificates/Bulk';
import { CertificatesPage } from '../src/features/certificates/Certificates';
import { DeliveryPanel, deliveryError } from '../src/features/certificates/Delivery';
import { mockFetch } from './helpers';

afterEach(() => vi.restoreAllMocks());

const cert = (o: object = {}) => ({
  id: 'c-1', certificate_number: 'CERT-2026-000001', status: 'issued', student_id: 's-1', student_name: 'Asha Verma',
  course_name: 'Web Design', batch_name: 'Jan', completion_date: '2026-03-15', grade: 'A', issue_date: '2026-10-24',
  template_name: 'Diploma', template_version: 1, verify_url: 'https://certs.example.com/verify/x', created_at: '2026-10-24T10:00:00Z',
  issued_at: '2026-10-24T10:00:01Z', revoked_at: null, ...o,
});
const dlv = (o: object = {}) => ({
  id: 'd-1', certificate_id: 'c-1', certificate_number: 'CERT-2026-000001', channel: 'email', recipient: 'a***@example.com', status: 'queued',
  attempts: 0, max_attempts: 5, next_attempt_at: '2026-10-24T10:00:00Z', created_at: '2026-10-24T10:00:00Z', finished_at: null, ...o,
});
const certs = { 'GET /api/v1/certificates': () => ({ body: { certificates: [cert()], has_more: false } }) };

it('offers no email button when the server cannot send, and says why to people who could', async () => {
  mockFetch({ ...certs, 'GET /api/v1/email/status': () => ({ body: { enabled: false } }) });
  render(<CertificatesPage canIssue={false} canRevoke={false} canDeliver />);
  await screen.findByRole('table', { name: 'Certificates' });
  expect(await screen.findByText(/not set up on this server yet/)).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /^Email / })).not.toBeInTheDocument();
  expect(screen.queryByRole('table', { name: 'Email deliveries' })).not.toBeInTheDocument();
});

it('hides email for members without the permission even when the server can send', async () => {
  mockFetch({ ...certs, 'GET /api/v1/email/status': () => ({ body: { enabled: true } }), 'GET /api/v1/deliveries': () => ({ body: { deliveries: [dlv({ status: 'accepted' })], has_more: false } }) });
  render(<CertificatesPage canIssue={false} canRevoke={false} canDeliver={false} />);
  await screen.findByRole('table', { name: 'Certificates' });
  await screen.findByRole('table', { name: 'Email deliveries' });
  expect(screen.queryByRole('button', { name: /^Email / })).not.toBeInTheDocument();
  expect(screen.queryByText(/not set up on this server/)).not.toBeInTheDocument();
});

it('hides email silently when the status cannot be read', async () => {
  mockFetch({ ...certs });
  render(<CertificatesPage canIssue={false} canRevoke={false} canDeliver />);
  await screen.findByRole('table', { name: 'Certificates' });
  expect(screen.queryByRole('button', { name: /^Email / })).not.toBeInTheDocument();
});

it('queues an email for an issued certificate and shows honest status', async () => {
  let queued = false;
  const fetchMock = mockFetch({
    ...certs,
    'GET /api/v1/email/status': () => ({ body: { enabled: true } }),
    'GET /api/v1/deliveries': () => ({ body: { deliveries: queued ? [dlv()] : [], has_more: false } }),
    'POST /api/v1/certificates/c-1/deliveries': () => { queued = true; return { status: 202, body: { delivery: dlv() } }; },
  });
  const user = userEvent.setup();
  render(<CertificatesPage canIssue={false} canRevoke={false} canDeliver />);
  await user.click(await screen.findByRole('button', { name: 'Email CERT-2026-000001' }));
  expect(await screen.findByText(/Queued an email for CERT-2026-000001/)).toBeInTheDocument();
  const table = await screen.findByRole('table', { name: 'Email deliveries' });
  expect(within(table).getByText('Queued')).toBeInTheDocument();
  expect(within(table).getByText('a***@example.com')).toBeInTheDocument();
  expect(screen.getByText(/cannot confirm that the email reached the inbox/)).toBeInTheDocument();
  expect(fetchMock.mock.calls.some(([u, i]) => String(u).endsWith('/certificates/c-1/deliveries') && i?.method === 'POST')).toBe(true);
});

it('shows why an email could not be sent (no email address on file)', async () => {
  mockFetch({
    ...certs,
    'GET /api/v1/email/status': () => ({ body: { enabled: true } }),
    'GET /api/v1/deliveries': () => ({ body: { deliveries: [], has_more: false } }),
    'POST /api/v1/certificates/c-1/deliveries': () => ({ status: 422, body: { error: { code: 'validation_failed', message: 'This student has no email address on file. Add one to the student record first.' } } }),
  });
  const user = userEvent.setup();
  render(<CertificatesPage canIssue={false} canRevoke={false} canDeliver />);
  await user.click(await screen.findByRole('button', { name: 'Email CERT-2026-000001' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('no email address on file');
});

it('never offers email for a revoked or failed certificate', async () => {
  mockFetch({
    'GET /api/v1/certificates': () => ({ body: { certificates: [cert({ status: 'revoked' }), cert({ id: 'c-2', certificate_number: 'CERT-2026-000002', status: 'failed' })], has_more: false } }),
    'GET /api/v1/email/status': () => ({ body: { enabled: true } }),
    'GET /api/v1/deliveries': () => ({ body: { deliveries: [], has_more: false } }),
  });
  render(<CertificatesPage canIssue={false} canRevoke={false} canDeliver />);
  await screen.findByRole('table', { name: 'Certificates' });
  expect(screen.queryByRole('button', { name: /^Email / })).not.toBeInTheDocument();
});

it('lists failures with a plain explanation and retries a failed delivery', async () => {
  let failed = true;
  const fetchMock = mockFetch({
    'GET /api/v1/deliveries': () => ({ body: { deliveries: [failed
      ? dlv({ status: 'failed', attempts: 1, next_attempt_at: null, last_error: 'recipient_rejected', finished_at: '2026-10-24T10:01:00Z' })
      : dlv({ status: 'queued', attempts: 1, last_error: 'recipient_rejected' })], has_more: false } }),
    'POST /api/v1/deliveries/d-1/retry': () => { failed = false; return { status: 202, body: { delivery: dlv() } }; },
  });
  const user = userEvent.setup();
  render(<DeliveryPanel canDeliver />);
  const table = await screen.findByRole('table', { name: 'Email deliveries' });
  expect(within(table).getByText('Failed')).toBeInTheDocument();
  expect(within(table).getByText(/email address was rejected/)).toBeInTheDocument();
  await user.click(within(table).getByRole('button', { name: 'Retry CERT-2026-000001' }));
  await waitFor(() => expect(screen.getByText('Queued')).toBeInTheDocument());
  expect(screen.getByText(/Will try again \(attempt 1 of 5 failed\)/)).toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([, i]) => i?.method === 'POST')).toHaveLength(1);
});

it('does not offer retry without the permission, never calls an accepted message delivered, and polls while in flight', async () => {
  let n = 0;
  mockFetch({ 'GET /api/v1/deliveries': () => { n += 1; return { body: { deliveries: [dlv({ status: n > 1 ? 'accepted' : 'sending', attempts: 2 })], has_more: false } }; } });
  render(<DeliveryPanel canDeliver={false} pollMs={10} />);
  expect(await screen.findByText('Sending…')).toBeInTheDocument();
  expect(await screen.findByText('Accepted by the mail server')).toBeInTheDocument();
  expect(screen.queryByText(/delivered/i)).not.toBeInTheDocument();
  expect(screen.getByText(/After 2 attempts/)).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /Retry/ })).not.toBeInTheDocument();
  const calls = n;
  await new Promise((r) => setTimeout(r, 60));
  expect(n).toBe(calls); // polling stops once nothing is in flight
});

it('maps every known error code to a sentence and falls back for unknown ones', () => {
  for (const c of ['connect_failed', 'timeout', 'tls_failed', 'auth_failed', 'provider_temporary', 'provider_rejected', 'recipient_rejected', 'certificate_not_issued', 'attempts_exhausted']) {
    expect(deliveryError(c)).toMatch(/\w+ \w+/);
  }
  expect(deliveryError('something_new')).toBe('The message could not be sent.');
  expect(deliveryError(undefined)).toBe('');
});

const job = (o: object = {}) => ({
  id: 'J-1234567890', status: 'completed', batch_id: 'b-1', batch_name: 'Jan', template_id: 't-1', template_name: 'Diploma', template_version: 1,
  total: 3, succeeded: 3, failed: 0, remaining: 0, skipped_already_issued: 0, skipped_not_eligible: 0,
  created_at: '2026-10-26T10:00:00Z', started_at: '2026-10-26T10:00:01Z', finished_at: '2026-10-26T10:00:09Z', ...o,
});
const bulkBase = {
  'GET /api/v1/batches': () => ({ body: { batches: [], has_more: false } }),
  'GET /api/v1/certificate-templates': () => ({ body: { templates: [], has_more: false } }),
  'GET /api/v1/bulk-jobs': () => ({ body: { jobs: [job()], has_more: false } }),
};

it('emails a whole finished job and reports what was skipped', async () => {
  const fetchMock = mockFetch({
    ...bulkBase,
    'POST /api/v1/bulk-jobs/J-1234567890/deliveries': () => ({ status: 202, body: { queued: 2, skipped_no_email: 1, skipped_already_delivered: 0, skipped_in_progress: 0, skipped_not_deliverable: 0 } }),
  });
  const emailed = vi.fn();
  const user = userEvent.setup();
  render(<BulkPanel canExport={false} canEmail onEmailed={emailed} onChanged={() => {}} pollMs={10} />);
  await user.click(screen.getByRole('button', { name: 'Issue for a batch…' }));
  await user.click(await screen.findByRole('button', { name: 'Email all 3 certificates' }));
  expect(await screen.findByText(/Queued 2 emails\. Skipped: 1 without an email address\./)).toBeInTheDocument();
  expect(emailed).toHaveBeenCalled();
  expect(fetchMock.mock.calls.filter(([, i]) => i?.method === 'POST')).toHaveLength(1);
});

it('offers "Email all" only to people who can email, and only for a finished job', async () => {
  mockFetch(bulkBase);
  const user = userEvent.setup();
  const { unmount } = render(<BulkPanel canExport={false} onChanged={() => {}} pollMs={10} />);
  await user.click(screen.getByRole('button', { name: 'Issue for a batch…' }));
  await screen.findByLabelText('Bulk job');
  expect(screen.queryByRole('button', { name: /Email all/ })).not.toBeInTheDocument();
  unmount();
  mockFetch({ ...bulkBase, 'GET /api/v1/bulk-jobs': () => ({ body: { jobs: [job({ status: 'processing', succeeded: 1, remaining: 2, finished_at: null })], has_more: false } }) });
  render(<BulkPanel canExport={false} canEmail onChanged={() => {}} pollMs={1000} />);
  await user.click(screen.getByRole('button', { name: 'Issue for a batch…' }));
  await screen.findByLabelText('Bulk job');
  expect(screen.queryByRole('button', { name: /Email all/ })).not.toBeInTheDocument();
});
