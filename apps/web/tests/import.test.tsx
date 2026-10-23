import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { ImportWizard } from '../src/features/records/ImportWizard';
import { mockFetch } from './helpers';

afterEach(() => vi.restoreAllMocks());

const counts = (o: Partial<Record<string, number>> = {}) => ({ total: 3, valid: 0, errors: 0, duplicates: 0, imported: 0, failed: 0, ...o });
const job = (o: object) => ({
  id: 'imp-1', status: 'uploaded', filename: 'students.csv', headers: ['Name', 'Roll No', 'Email'], mapping: {},
  date_format: 'iso', counts: counts(), preview: [], issues: [], issues_truncated: false, ...o,
});

it('walks upload → mapping → review → confirm → error report', async () => {
  const fetchMock = mockFetch({
    'POST /api/v1/students/imports': () => ({ status: 201, body: { import: job({ suggested_mapping: { full_name: 0, student_number: 1, email: 2 } }) } }),
    'PUT /api/v1/students/imports/imp-1/mapping': () => ({
      body: { import: job({ status: 'validated', mapping: { full_name: 0, student_number: 1, email: 2 }, counts: counts({ valid: 2, errors: 1 }),
        issues: [{ row: 3, state: 'error', message: 'email: Enter a valid email address.' }] }) },
    }),
    'POST /api/v1/students/imports/imp-1/confirm': () => ({ body: { import: job({ status: 'completed', counts: counts({ imported: 2, errors: 1 }) }) } }),
    'GET /api/v1/students/imports/imp-1/errors': () => ({ body: 'Row,Result' }),
  });
  const user = userEvent.setup();
  render(<ImportWizard />);

  await user.upload(screen.getByLabelText('CSV file'), new File(['Name,Roll No,Email\nAsha,ST-1,a@x.com\n'], 'students.csv', { type: 'text/csv' }));
  expect(await screen.findByLabelText(/Full name/)).toHaveValue('0'); // suggested mapping pre-selected
  const upload = fetchMock.mock.calls.find(([u]) => String(u).endsWith('/api/v1/students/imports'))!;
  expect(upload[1]!.body).toBeInstanceOf(FormData);
  expect(new Headers(upload[1]!.headers).get('Content-Type')).toBeNull(); // the browser sets the multipart boundary

  await user.click(screen.getByRole('button', { name: 'Check rows' }));
  expect(await screen.findByText(/ready to import/)).toBeInTheDocument();
  expect(screen.getByText('email: Enter a valid email address.')).toBeInTheDocument();
  const put = fetchMock.mock.calls.find(([, i]) => i?.method === 'PUT')!;
  expect(JSON.parse(String(put[1]!.body))).toEqual({ mapping: { full_name: 0, student_number: 1, email: 2 }, date_format: 'iso' });

  await user.click(screen.getByRole('button', { name: 'Import 2 students' }));
  expect(await screen.findByText(/Import finished/)).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Download error report (CSV)' })).toBeInTheDocument();
});

it('shows a server-side file problem next to the upload field', async () => {
  mockFetch({
    'POST /api/v1/students/imports': () => ({
      status: 422,
      body: { error: { code: 'validation_failed', message: 'Excel files are not supported yet.', details: { file: 'Excel files are not supported yet.' } } },
    }),
  });
  const user = userEvent.setup();
  render(<ImportWizard />);
  await user.upload(screen.getByLabelText('CSV file'), new File(['x'], 'students.xlsx'));
  await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Excel files are not supported yet.'));
});

it('disables the import button when nothing is valid', async () => {
  mockFetch({
    'POST /api/v1/students/imports': () => ({ status: 201, body: { import: job({ suggested_mapping: { full_name: 0 } }) } }),
    'PUT /api/v1/students/imports/imp-1/mapping': () => ({ body: { import: job({ status: 'validated', mapping: { full_name: 0 }, counts: counts({ errors: 3 }) }) } }),
  });
  const user = userEvent.setup();
  render(<ImportWizard />);
  await user.upload(screen.getByLabelText('CSV file'), new File(['x'], 's.csv'));
  await user.click(await screen.findByRole('button', { name: 'Check rows' }));
  expect(await screen.findByRole('button', { name: 'Import 0 students' })).toBeDisabled();
});
