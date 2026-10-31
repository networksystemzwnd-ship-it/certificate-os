import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../src/app/App';
import { setActiveInstitute } from '../src/lib/api';
import { Overview } from '../src/features/institute/Overview';
import { OpsDashboard } from '../src/features/ops/OpsDashboard';
import { mockFetch } from './helpers';

afterEach(() => {
  vi.restoreAllMocks();
  setActiveInstitute(null);
});

const full = {
  days: 30, since: '2026-09-30T00:00:00Z',
  records: { active_students: 12, archived_students: 2, active_batches: 3 },
  certificates: { issued_in_period: 5, issued: 9, revoked: 1, failed: 2 },
  last_job: { id: 'j-1', status: 'completed_with_errors', batch_name: 'Jan', total: 10, succeeded: 9, failed: 1, created_at: '2026-10-20T10:00:00Z' },
  deliveries: { queued: 1, sending: 0, accepted: 6, failed: 2 },
  recent: [{ id: 'c-1', certificate_number: 'CERT-2026-000001', student_name: 'Asha Verma', course_name: 'Web Design', status: 'issued', at: '2026-10-24T10:00:01Z' }],
};

it('shows the institute figures and reloads for another period', async () => {
  const fetchMock = mockFetch({ 'GET /api/v1/dashboard': () => ({ body: { dashboard: full } }) });
  const user = userEvent.setup();
  render(<Overview />);
  const records = await screen.findByLabelText('Records');
  expect(records).toHaveTextContent('12 (2 archived)');
  expect(screen.getByLabelText('Certificates')).toHaveTextContent('Issued in the last 30 days5');
  expect(screen.getByText(/Jan — Completed with errors, 9 of 10 issued, 1 failed/)).toBeInTheDocument();
  expect(screen.getByLabelText('Email deliveries')).toHaveTextContent('9 — 6 accepted by the mail server, 2 failed, 1 waiting');
  expect(within(screen.getByRole('table', { name: 'Recent certificates' })).getByText('Asha Verma')).toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText('Period'), '90');
  await waitFor(() => expect(fetchMock.mock.calls.some(([u]) => String(u).endsWith('/api/v1/dashboard?days=90'))).toBe(true));
});

it('omits sections the server left out and explains an empty institute', async () => {
  mockFetch({ 'GET /api/v1/dashboard': () => ({ body: { dashboard: { days: 30, since: '2026-09-30T00:00:00Z', records: { active_students: 0, archived_students: 0, active_batches: 0 } } } }) });
  render(<Overview />);
  await screen.findByLabelText('Records');
  expect(screen.queryByLabelText('Certificates')).not.toBeInTheDocument();
  expect(screen.queryByRole('table', { name: 'Recent certificates' })).not.toBeInTheDocument();
  expect(screen.queryByText(/Latest bulk job/)).not.toBeInTheDocument();
});

it('shows "none yet" and an empty list, and a retry when loading fails', async () => {
  let fail = true;
  mockFetch({ 'GET /api/v1/dashboard': () => fail
    ? { status: 500, body: { error: { code: 'internal', message: 'An unexpected error occurred.' } } }
    : { body: { dashboard: { ...full, last_job: null, recent: [], deliveries: { queued: 0, sending: 0, accepted: 0, failed: 0 } } } } });
  const user = userEvent.setup();
  render(<Overview />);
  expect(await screen.findByText(/could not be loaded/)).toBeInTheDocument();
  fail = false;
  await user.click(screen.getByRole('button', { name: 'Try again' }));
  expect(await screen.findByText(/Latest bulk job:/)).toHaveTextContent('none yet');
  expect(screen.getByText('No certificates yet.')).toBeInTheDocument();
});

it('is the first tab of the workspace', async () => {
  const me = () => ({ body: { user: { id: '1', email: 'a@example.com', permissions: [] } } });
  const inst = { id: 'i-1', slug: 'alpha', name: 'Alpha', status: 'active', role: 'owner' };
  mockFetch({
    'GET /api/v1/me': me,
    'GET /api/v1/me/institutes': () => ({ body: { institutes: [inst] } }),
    'GET /api/v1/institute': () => ({ body: { institute: inst } }),
    'GET /api/v1/dashboard': () => ({ body: { dashboard: full } }),
  });
  render(<MemoryRouter initialEntries={['/app']}><App /></MemoryRouter>);
  expect(await screen.findByRole('button', { name: 'Overview', pressed: true })).toBeInTheDocument();
  expect(await screen.findByLabelText('Records')).toBeInTheDocument();
});

it('shows platform aggregates without any personal data', async () => {
  mockFetch({ 'GET /api/v1/ops/dashboard': () => ({ body: { dashboard: {
    days: 30, since: '2026-09-30T00:00:00Z', applications: { submitted: 2, under_review: 1, approved: 7 }, institutes: { active: 5, suspended: 1 },
    volume: { certificates_issued: 40, certificates_revoked: 1, bulk_jobs_started: 2, emails_accepted: 35, emails_failed: 3 },
    recent_decisions: [{ action: 'application.approved', at: '2026-10-01T00:00:00Z' }],
  } } }) });
  render(<OpsDashboard />);
  expect(await screen.findByLabelText('Institutes and applications')).toHaveTextContent('Applications waiting for review or changes3');
  expect(screen.getByLabelText('Institutes and applications')).toHaveTextContent('Suspended institutes1');
  expect(screen.getByLabelText('Volume')).toHaveTextContent('Emails that failed3');
  expect(screen.getByText(/Application approved/)).toBeInTheDocument();
});
