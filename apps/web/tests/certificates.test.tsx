import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { CertificatesPage } from '../src/features/certificates/Certificates';
import { mockFetch } from './helpers';

afterEach(() => vi.restoreAllMocks());

const cert = (o: object = {}) => ({
  id: 'c-1', certificate_number: 'CERT-2026-000001', status: 'issued', student_id: 's-1', student_name: 'Asha Verma',
  course_name: 'Web Design', batch_name: 'Jan', completion_date: '2026-03-15', grade: 'A', issue_date: '2026-10-24',
  template_name: 'Diploma', template_version: 1, verify_url: 'https://certs.example.com/verify/x', created_at: '2026-10-24T10:00:00Z',
  issued_at: '2026-10-24T10:00:01Z', revoked_at: null, ...o,
});

it('lists certificates and offers only the actions the member may use', async () => {
  mockFetch({ 'GET /api/v1/certificates': () => ({ body: { certificates: [cert(), cert({ id: 'c-2', certificate_number: 'CERT-2026-000002', status: 'failed' })], has_more: false } }) });
  render(<CertificatesPage canIssue={false} canRevoke={false} />);
  expect(await screen.findByRole('table', { name: 'Certificates' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Download CERT-2026-000001' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /Revoke/ })).not.toBeInTheDocument();
  expect(screen.queryByRole('heading', { name: 'Issue a certificate' })).not.toBeInTheDocument();
  // a failed certificate never offers a download
  expect(screen.queryByRole('button', { name: 'Download CERT-2026-000002' })).not.toBeInTheDocument();
  expect(screen.getByText(/not issued/)).toBeInTheDocument();
});

it('issues a certificate for a completed enrollment', async () => {
  let issued = false;
  const fetchMock = mockFetch({
    'GET /api/v1/certificates': () => ({ body: { certificates: issued ? [cert()] : [], has_more: false } }),
    'GET /api/v1/students': () => ({ body: { students: [{ id: 's-1', full_name: 'Asha Verma', student_number: '', email: '', phone: '', archived_at: null }], has_more: false } }),
    'GET /api/v1/students/s-1': () => ({
      body: { student: { id: 's-1', full_name: 'Asha Verma', student_number: '', email: '', phone: '', archived_at: null, enrollments: [
        { id: 'e-1', student_id: 's-1', batch_id: 'b-1', batch_name: 'Jan', course_id: 'c', course_name: 'Web Design', status: 'completed', completion_date: '2026-03-15', grade: 'A' },
        { id: 'e-2', student_id: 's-1', batch_id: 'b-2', batch_name: 'Feb', course_id: 'c2', course_name: 'Not done', status: 'enrolled', completion_date: '', grade: '' },
      ] } },
    }),
    'GET /api/v1/certificate-templates': () => ({ body: { templates: [{ id: 't-1', name: 'Diploma', archived_at: null, current_version: { number: 2, created_at: '2026-10-01T00:00:00Z' } }], has_more: false } }),
    'POST /api/v1/certificates': () => {
      issued = true;
      return { status: 201, body: { certificate: cert() } };
    },
  });
  const user = userEvent.setup();
  render(<CertificatesPage canIssue canRevoke />);
  await user.type(await screen.findByLabelText('Find a student'), 'asha');
  await user.click(screen.getByRole('button', { name: 'Search' }));
  await user.click(await screen.findByRole('button', { name: 'Select Asha Verma' }));
  const enrollment = await screen.findByLabelText('Completed course');
  expect(screen.queryByText(/Not done/)).not.toBeInTheDocument(); // only completed enrollments are offered
  expect(enrollment).toHaveValue('e-1');
  await user.selectOptions(await screen.findByLabelText('Template'), 't-1');
  await user.click(screen.getByRole('button', { name: 'Issue certificate' }));
  expect(await screen.findByText(/Issued CERT-2026-000001 for Asha Verma/)).toBeInTheDocument();
  const post = fetchMock.mock.calls.find(([, i]) => i?.method === 'POST')!;
  expect(JSON.parse(String(post[1]!.body))).toEqual({ enrollment_id: 'e-1', template_id: 't-1', template_version: 0 });
});

it('explains when a student has no completed course', async () => {
  mockFetch({
    'GET /api/v1/certificates': () => ({ body: { certificates: [], has_more: false } }),
    'GET /api/v1/students': () => ({ body: { students: [{ id: 's-1', full_name: 'Asha Verma' }], has_more: false } }),
    'GET /api/v1/students/s-1': () => ({ body: { student: { id: 's-1', full_name: 'Asha Verma', enrollments: [] } } }),
    'GET /api/v1/certificate-templates': () => ({ body: { templates: [], has_more: false } }),
  });
  const user = userEvent.setup();
  render(<CertificatesPage canIssue canRevoke />);
  await user.click(await screen.findByRole('button', { name: 'Search' }));
  await user.click(await screen.findByRole('button', { name: 'Select Asha Verma' }));
  expect(await screen.findByText(/no completed course/)).toBeInTheDocument();
});

it('downloads the PDF through the API client and revokes with a reason', async () => {
  let revoked = false;
  const fetchMock = mockFetch({
    'GET /api/v1/certificates': () => ({ body: { certificates: [cert({ status: revoked ? 'revoked' : 'issued', revoke_reason: revoked ? 'Wrong name' : undefined })], has_more: false } }),
    'GET /api/v1/certificates/c-1/pdf': () => ({ body: 'pdf' }),
    'POST /api/v1/certificates/c-1/revoke': (init) => {
      expect(JSON.parse(String(init!.body))).toEqual({ reason: 'Wrong name' });
      revoked = true;
      return { body: { certificate: cert({ status: 'revoked' }) } };
    },
  });
  const created = vi.fn(() => 'blob:x');
  Object.assign(URL, { createObjectURL: created, revokeObjectURL: vi.fn() });
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
  vi.spyOn(window, 'prompt').mockReturnValue('Wrong name');
  const user = userEvent.setup();
  render(<CertificatesPage canIssue={false} canRevoke />);
  await user.click(await screen.findByRole('button', { name: 'Download CERT-2026-000001' }));
  await waitFor(() => expect(created).toHaveBeenCalled());
  expect(fetchMock.mock.calls.some(([u]) => String(u).endsWith('/api/v1/certificates/c-1/pdf'))).toBe(true);

  await user.click(screen.getByRole('button', { name: 'Revoke CERT-2026-000001' }));
  expect(await screen.findByText(/Revoked — Wrong name/)).toBeInTheDocument();
});
