import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../src/app/App';
import { setActiveInstitute } from '../src/lib/api';
import { mockFetch } from './helpers';

afterEach(() => {
  vi.restoreAllMocks();
  setActiveInstitute(null);
});

const me = () => ({ body: { user: { id: '1', email: 'a@example.com', permissions: [] } } });
const inst = (id: string, name: string, role = 'owner', status = 'active') => ({ id, slug: name.toLowerCase(), name, status, role });

const renderWorkspace = () =>
  render(
    <MemoryRouter initialEntries={['/app']}>
      <App />
    </MemoryRouter>,
  );

it('auto-selects a single institute and sends X-Institute-ID', async () => {
  const fetchMock = mockFetch({
    'GET /api/v1/me': me,
    'GET /api/v1/me/institutes': () => ({ body: { institutes: [inst('i-1', 'Alpha')] } }),
    'GET /api/v1/institute': () => ({ body: { institute: inst('i-1', 'Alpha') } }),
  });
  renderWorkspace();
  expect(await screen.findByRole('heading', { name: 'Alpha' })).toBeInTheDocument();
  expect(screen.getByText('owner')).toBeInTheDocument();
  const call = fetchMock.mock.calls.find(([u]) => String(u).endsWith('/api/v1/institute'))!;
  expect(new Headers(call[1]!.headers).get('X-Institute-ID')).toBe('i-1');
});

it('asks the user to choose when they belong to several institutes', async () => {
  mockFetch({
    'GET /api/v1/me': me,
    'GET /api/v1/me/institutes': () => ({ body: { institutes: [inst('i-1', 'Alpha', 'staff'), inst('i-2', 'Beta', 'admin')] } }),
    'GET /api/v1/institute': () => ({ body: { institute: inst('i-2', 'Beta', 'admin') } }),
  });
  const u = userEvent.setup();
  renderWorkspace();
  const select = await screen.findByLabelText('Institute');
  expect(screen.queryByRole('heading', { name: 'Beta' })).not.toBeInTheDocument();
  await u.selectOptions(select, 'i-2');
  expect(await screen.findByRole('heading', { name: 'Beta' })).toBeInTheDocument();
});

it('shows an empty state without memberships', async () => {
  mockFetch({ 'GET /api/v1/me': me, 'GET /api/v1/me/institutes': () => ({ body: { institutes: [] } }) });
  renderWorkspace();
  expect(await screen.findByRole('heading', { name: 'No institute access' })).toBeInTheDocument();
});

it('flags a suspended institute as read-only', async () => {
  mockFetch({
    'GET /api/v1/me': me,
    'GET /api/v1/me/institutes': () => ({ body: { institutes: [inst('i-3', 'Gamma', 'owner', 'suspended')] } }),
    'GET /api/v1/institute': () => ({ body: { institute: inst('i-3', 'Gamma', 'owner', 'suspended') } }),
  });
  renderWorkspace();
  expect(await screen.findByText(/Access is read-only/)).toBeInTheDocument();
});

it('shows a clear error when the server denies access', async () => {
  mockFetch({
    'GET /api/v1/me': me,
    'GET /api/v1/me/institutes': () => ({ body: { institutes: [inst('i-1', 'Alpha')] } }),
    'GET /api/v1/institute': () => ({
      status: 403,
      body: { error: { code: 'forbidden', message: 'You do not have access to this institute.', request_id: 'req12345' } },
    }),
  });
  renderWorkspace();
  expect(await screen.findByRole('alert')).toHaveTextContent('You do not have access to this institute.');
});
