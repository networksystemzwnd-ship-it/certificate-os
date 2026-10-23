import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { App } from '../src/app/App';
import { StaffPage } from '../src/features/staff/Staff';
import { mockFetch } from './helpers';

beforeEach(() => sessionStorage.clear());
afterEach(() => {
  vi.restoreAllMocks();
  window.history.replaceState(null, '', '/');
});

const member = (o: object) => ({ id: 'm-1', user_id: 'u-1', email: 'owner@x.com', role: 'owner', grants: [], status: 'active', ...o });
const invitation = (o: object) => ({
  id: 'i-1', email: 'new@x.com', role: 'staff', grants: [], status: 'pending',
  created_at: '2026-10-01T00:00:00Z', expires_at: '2026-10-08T00:00:00Z', ...o,
});

it('creates an invitation and shows the one-time link', async () => {
  const fetchMock = mockFetch({
    'GET /api/v1/institute/members': () => ({ body: { members: [member({})] } }),
    'GET /api/v1/institute/invitations': () => ({ body: { invitations: [] } }),
    'POST /api/v1/institute/invitations': () => ({
      status: 201,
      body: { invitation: invitation({}), token: 'inv_abc', accept_path: '/accept-invite#token=inv_abc' },
    }),
  });
  const user = userEvent.setup();
  render(<StaffPage selfEmail="owner@x.com" />);
  await screen.findByRole('table', { name: 'Members' });

  await user.type(screen.getByLabelText('Their email address'), 'new@x.com');
  await user.click(screen.getByLabelText('Issue certificates'));
  await user.click(screen.getByRole('button', { name: 'Create invitation link' }));

  const link = await screen.findByLabelText('Invitation link', { selector: 'input' });
  expect(link).toHaveValue(`${window.location.origin}/accept-invite#token=inv_abc`);
  expect(screen.getByText(/shown only once/)).toBeInTheDocument();
  const post = fetchMock.mock.calls.find(([, i]) => i?.method === 'POST')!;
  expect(JSON.parse(String(post[1]!.body))).toEqual({ email: 'new@x.com', role: 'staff', grants: ['certificates.issue'] });
});

it('protects owners and the own row, and removes a member after confirmation', async () => {
  let removed = false;
  const fetchMock = mockFetch({
    'GET /api/v1/institute/members': () => ({
      body: { members: [member({}), member({ id: 'm-2', user_id: 'u-2', email: 'staff@x.com', role: 'staff' })] },
    }),
    'GET /api/v1/institute/invitations': () => ({ body: { invitations: [invitation({})] } }),
    'DELETE /api/v1/institute/members/m-2': () => {
      removed = true;
      return { body: { removed: true } };
    },
    'DELETE /api/v1/institute/invitations/i-1': () => ({ body: { invitation: invitation({ status: 'revoked' }) } }),
  });
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  const user = userEvent.setup();
  render(<StaffPage selfEmail="owner@x.com" />);
  const table = await screen.findByRole('table', { name: 'Members' });
  const rows = within(table).getAllByRole('row');
  expect(within(rows[1]!).queryByRole('button')).not.toBeInTheDocument(); // owner row: no actions
  await user.click(within(rows[2]!).getByRole('button', { name: 'Remove' }));
  await waitFor(() => expect(removed).toBe(true));

  await user.click(await screen.findByRole('button', { name: 'Revoke' }));
  await waitFor(() => expect(fetchMock.mock.calls.some(([u, i]) => String(u).endsWith('/invitations/i-1') && i?.method === 'DELETE')).toBe(true));
});

const renderInvite = (hash: string) => {
  window.history.replaceState(null, '', `/accept-invite${hash}`);
  return render(
    <MemoryRouter initialEntries={[`/accept-invite${hash}`]}>
      <App />
    </MemoryRouter>,
  );
};

it('keeps the token for the login detour when signed out and does not send it anywhere', async () => {
  const fetchMock = mockFetch({
    'GET /api/v1/me': () => ({ status: 401, body: { error: { code: 'unauthenticated', message: 'Authentication required.' } } }),
  });
  renderInvite('#token=inv_abc');
  expect(await screen.findByText(/You have been invited/)).toBeInTheDocument();
  expect(sessionStorage.getItem('pending_invite')).toBe('inv_abc');
  expect(window.location.hash).toBe(''); // fragment removed from the address bar
  expect(fetchMock.mock.calls.every(([u]) => !String(u).includes('inv_abc'))).toBe(true);
});

it('accepts the invitation when signed in and clears the stored token', async () => {
  sessionStorage.setItem('pending_invite', 'inv_abc');
  const fetchMock = mockFetch({
    'GET /api/v1/me': () => ({ body: { user: { id: '1', email: 'new@x.com', permissions: [] } } }),
    'POST /api/v1/invitations/accept': () => ({ body: { institute: { id: 'i-1', slug: 'alpha', name: 'Alpha Institute' }, role: 'staff' } }),
  });
  renderInvite('');
  expect(await screen.findByText(/Alpha Institute/)).toBeInTheDocument();
  const post = fetchMock.mock.calls.find(([, i]) => i?.method === 'POST')!;
  expect(JSON.parse(String(post[1]!.body))).toEqual({ token: 'inv_abc' });
  expect(sessionStorage.getItem('pending_invite')).toBeNull();
});

it('shows one generic message for every refusal', async () => {
  mockFetch({
    'GET /api/v1/me': () => ({ body: { user: { id: '1', email: 'other@x.com', permissions: [] } } }),
    'POST /api/v1/invitations/accept': () => ({ status: 404, body: { error: { code: 'not_found', message: 'Not found.' } } }),
  });
  renderInvite('#token=inv_bad');
  expect(await screen.findByRole('alert')).toHaveTextContent(/invalid or has expired/);
});
