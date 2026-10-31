import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { BulkPanel } from '../src/features/certificates/Bulk';
import { CertificatesPage } from '../src/features/certificates/Certificates';
import { mockFetch } from './helpers';

afterEach(() => vi.restoreAllMocks());

const job = (o: object = {}) => ({
  id: 'J-1234567890', status: 'processing', batch_id: 'b-1', batch_name: 'Jan', template_id: 't-1', template_name: 'Diploma', template_version: 1,
  total: 3, succeeded: 1, failed: 0, remaining: 2, skipped_already_issued: 1, skipped_not_eligible: 2,
  created_at: '2026-10-26T10:00:00Z', started_at: '2026-10-26T10:00:01Z', finished_at: null, ...o,
});
const done = job({ status: 'completed', succeeded: 3, remaining: 0, finished_at: '2026-10-26T10:00:09Z' });
const withErrors = job({ status: 'completed_with_errors', succeeded: 2, failed: 1, remaining: 0, finished_at: '2026-10-26T10:00:09Z' });

const base = {
  'GET /api/v1/batches': () => ({ body: { batches: [{ id: 'b-1', course_id: 'c-1', course_name: 'Web Design', name: 'Jan', start_date: '', end_date: '', archived_at: null }], has_more: false } }),
  'GET /api/v1/certificate-templates': () => ({ body: { templates: [{ id: 't-1', name: 'Diploma', archived_at: null, current_version: { number: 1, created_at: '2026-10-01T00:00:00Z' } }], has_more: false } }),
};

it('loads nothing until the panel is opened, and only people who can issue see it', async () => {
  const fetchMock = mockFetch({ 'GET /api/v1/certificates': () => ({ body: { certificates: [], has_more: false } }), ...base });
  const user = userEvent.setup();
  const { unmount } = render(<CertificatesPage canIssue canRevoke={false} />);
  await screen.findByText('No certificates yet');
  expect(screen.getByRole('heading', { name: 'Issue a whole batch' })).toBeInTheDocument();
  expect(fetchMock.mock.calls.some(([u]) => String(u).includes('/bulk-jobs') || String(u).includes('/batches'))).toBe(false);
  unmount();
  mockFetch({ 'GET /api/v1/certificates': () => ({ body: { certificates: [], has_more: false } }) });
  render(<CertificatesPage canIssue={false} canRevoke={false} />);
  await screen.findByText('No certificates yet');
  expect(screen.queryByRole('heading', { name: 'Issue a whole batch' })).not.toBeInTheDocument();
  void user;
});

it('previews, starts, shows progress and tells the list when the job is finished', async () => {
  let state: 'none' | 'running' | 'done' = 'none';
  const fetchMock = mockFetch({
    ...base,
    'GET /api/v1/bulk-jobs': () => ({ body: { jobs: state === 'none' ? [] : [state === 'done' ? done : job()], has_more: false } }),
    'GET /api/v1/certificates/bulk-preview': () => ({ body: { preview: { batch_name: 'Jan', eligible: 3, already_issued: 1, not_eligible: 2, max_per_job: 500 } } }),
    'POST /api/v1/certificates/bulk': () => {
      state = 'done'; // the next poll sees the finished job
      return { status: 202, body: { job: job() } };
    },
  });
  const changed = vi.fn();
  const user = userEvent.setup();
  render(<BulkPanel canExport={false} onChanged={changed} pollMs={10} />);
  await user.click(screen.getByRole('button', { name: 'Issue for a batch…' }));
  await user.selectOptions(await screen.findByLabelText('Batch'), 'b-1');
  await user.selectOptions(screen.getByLabelText('Template'), 't-1');
  await user.click(screen.getByRole('button', { name: 'Check what will be issued' }));
  const region = await screen.findByRole('region', { name: 'Preview' });
  expect(within(region).getByText(/will be issued for Jan/)).toBeInTheDocument();
  expect(region).toHaveTextContent('1 already has one and 2 are not completed or archived');
  await user.click(within(region).getByRole('button', { name: 'Issue 3 certificates' }));
  const post = fetchMock.mock.calls.find(([, i]) => i?.method === 'POST')!;
  expect(JSON.parse(String(post[1]!.body))).toEqual({ batch_id: 'b-1', template_id: 't-1', template_version: 0 });
  expect(await screen.findByText(/Completed — 3 of 3 issued/)).toBeInTheDocument();
  await waitFor(() => expect(changed).toHaveBeenCalled());
  // finished: no ZIP without the export permission
  expect(screen.queryByRole('button', { name: 'Download all as ZIP' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Cancel job' })).not.toBeInTheDocument();
});

it('shows a running job with cancel and resume, and refuses to start while nothing is eligible', async () => {
  let cancelled = false;
  mockFetch({
    ...base,
    'GET /api/v1/bulk-jobs': () => ({ body: { jobs: [cancelled ? job({ status: 'cancelled', remaining: 2, finished_at: '2026-10-26T10:05:00Z' }) : job()], has_more: false } }),
    'POST /api/v1/bulk-jobs/J-1234567890/cancel': () => {
      cancelled = true;
      return { body: { job: job({ status: 'cancelled' }) } };
    },
  });
  const user = userEvent.setup();
  render(<BulkPanel canExport onChanged={() => {}} pollMs={5000} />);
  await user.click(screen.getByRole('button', { name: 'Issue for a batch…' }));
  expect(await screen.findByText(/Issuing… — 1 of 3 issued, 2 remaining/)).toBeInTheDocument();
  expect(screen.getByRole('progressbar', { name: 'Bulk issuing progress' })).toHaveAttribute('value', '1');
  expect(screen.getByRole('button', { name: 'Resume job' })).toBeInTheDocument();
  expect(screen.queryByLabelText('Batch')).not.toBeInTheDocument(); // one job at a time
  await user.click(screen.getByRole('button', { name: 'Cancel job' }));
  expect(await screen.findByText(/Cancelled — 1 of 3 issued, 2 remaining/)).toBeInTheDocument();
});

it('lists what failed with a reason, retries only those, and downloads the ZIP when allowed', async () => {
  let retried = false;
  const fetchMock = mockFetch({
    ...base,
    'GET /api/v1/bulk-jobs': () => ({ body: { jobs: [retried ? done : withErrors], has_more: false } }),
    'GET /api/v1/bulk-jobs/J-1234567890/items': () => ({ body: { items: [{ enrollment_id: 'e-2', student_name: 'Bela Rao', position: 2, status: 'failed', attempts: 1, error: 'The PDF could not be created. Retry this item.' }], has_more: false } }),
    'POST /api/v1/bulk-jobs/J-1234567890/retry-failed': () => {
      retried = true;
      return { body: { job: job() } };
    },
    'GET /api/v1/bulk-jobs/J-1234567890/export': () => ({ body: 'zip' }),
  });
  const created = vi.fn(() => 'blob:zip');
  Object.assign(URL, { createObjectURL: created, revokeObjectURL: vi.fn() });
  const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
  const user = userEvent.setup();
  render(<BulkPanel canExport onChanged={() => {}} pollMs={5000} />);
  await user.click(screen.getByRole('button', { name: 'Issue for a batch…' }));
  const table = await screen.findByRole('table', { name: 'Failed certificates' });
  expect(within(table).getByText('Bela Rao')).toBeInTheDocument();
  expect(within(table).getByText(/could not be created/)).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Download all as ZIP' }));
  await waitFor(() => expect(created).toHaveBeenCalled());
  expect(click).toHaveBeenCalled();
  expect(fetchMock.mock.calls.some(([u]) => String(u).endsWith('/api/v1/bulk-jobs/J-1234567890/export'))).toBe(true);
  await user.click(screen.getByRole('button', { name: 'Retry failed (1)' }));
  expect(await screen.findByText(/Completed — 3 of 3 issued/)).toBeInTheDocument();
  expect(screen.queryByRole('table', { name: 'Failed certificates' })).not.toBeInTheDocument();
});

it('shows the server message when starting is refused', async () => {
  mockFetch({
    ...base,
    'GET /api/v1/bulk-jobs': () => ({ body: { jobs: [], has_more: false } }),
    'GET /api/v1/certificates/bulk-preview': () => ({ body: { preview: { batch_name: 'Jan', eligible: 0, already_issued: 3, not_eligible: 0, max_per_job: 500 } } }),
  });
  const user = userEvent.setup();
  render(<BulkPanel canExport={false} onChanged={() => {}} />);
  await user.click(screen.getByRole('button', { name: 'Issue for a batch…' }));
  await user.selectOptions(await screen.findByLabelText('Batch'), 'b-1');
  await user.selectOptions(screen.getByLabelText('Template'), 't-1');
  await user.click(screen.getByRole('button', { name: 'Check what will be issued' }));
  const region = await screen.findByRole('region', { name: 'Preview' });
  expect(within(region).getByRole('button', { name: 'Issue 0 certificates' })).toBeDisabled();
});
