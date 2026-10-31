import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { StudentsPage } from '../src/features/records/Records';
import { mockFetch } from './helpers';

afterEach(() => vi.restoreAllMocks());

const routes = {
  'GET /api/v1/courses': () => ({ body: { courses: [{ id: 'c-1', name: 'Web Design', code: '', description: '', archived_at: null }], has_more: false } }),
  'GET /api/v1/students': () => ({ body: { students: [{ id: 's-1', student_number: '', full_name: 'Asha Rao', email: 'asha@x.com', phone: '', archived_at: null }], has_more: false } }),
  'GET /api/v1/students/export': () => ({ body: 'csv' }),
};

it('offers the export only to people who may export, and sends the current filters', async () => {
  const fetchMock = mockFetch(routes);
  Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:csv'), revokeObjectURL: vi.fn() });
  const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
  const user = userEvent.setup();
  const { unmount } = render(<StudentsPage canWrite={false} canExport />);
  await screen.findByText('Asha Rao');
  await user.selectOptions(screen.getByLabelText('Status'), 'completed');
  await screen.findByText('Asha Rao');
  await user.click(screen.getByRole('button', { name: 'Export students (CSV)' }));
  await waitFor(() => expect(click).toHaveBeenCalled());
  expect(fetchMock.mock.calls.some(([u]) => String(u).includes('/api/v1/students/export') && String(u).includes('status=completed'))).toBe(true);
  unmount();

  mockFetch(routes);
  render(<StudentsPage canWrite={false} />);
  await screen.findByText('Asha Rao');
  expect(screen.queryByRole('button', { name: 'Export students (CSV)' })).not.toBeInTheDocument();
});

it('shows the server message when the export is refused', async () => {
  mockFetch({ ...routes, 'GET /api/v1/students/export': () => ({ status: 422, body: { error: { code: 'validation_failed', message: 'Too many students to export at once. Narrow the filter.', request_id: 'r1' } } }) });
  const user = userEvent.setup();
  render(<StudentsPage canWrite={false} canExport />);
  await screen.findByText('Asha Rao');
  await user.click(screen.getByRole('button', { name: 'Export students (CSV)' }));
  expect(await screen.findByText(/Too many students/)).toBeInTheDocument();
});
