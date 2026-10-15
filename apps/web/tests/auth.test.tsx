import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../src/app/App';
import { anonymous, mockFetch } from './helpers';

afterEach(() => vi.restoreAllMocks());

const renderAt = (path: string) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  );

const user = (permissions: string[] = []) => ({ user: { id: '1', email: 'a@example.com', permissions } });

it('redirects anonymous visitors from the workspace to login', async () => {
  mockFetch({ 'GET /api/v1/me': anonymous });
  renderAt('/app');
  expect(await screen.findByRole('heading', { name: 'Log in' })).toBeInTheDocument();
});

it('logs in, sends the CSRF header, and lands in the workspace', async () => {
  let loggedIn = false;
  const fetchMock = mockFetch({
    'GET /api/v1/me': () => (loggedIn ? { body: user() } : anonymous(undefined)),
    'POST /api/v1/auth/login': () => {
      loggedIn = true;
      return { body: user() };
    },
  });
  const u = userEvent.setup();
  renderAt('/login');
  await u.type(await screen.findByLabelText('Email'), 'a@example.com');
  await u.type(screen.getByLabelText('Password'), 'correct horse battery');
  await u.click(screen.getByRole('button', { name: 'Log in' }));
  expect(await screen.findByRole('heading', { name: 'Institute workspace' })).toBeInTheDocument();

  const post = fetchMock.mock.calls.find(([, init]) => init?.method === 'POST')!;
  expect(new Headers(post[1]!.headers).get('X-CSRF-Protection')).toBe('1');
  expect(screen.getByText('a@example.com')).toBeInTheDocument();
});

it('shows field errors from a 422 response', async () => {
  mockFetch({
    'GET /api/v1/me': anonymous,
    'POST /api/v1/auth/register': () => ({
      status: 422,
      body: { error: { code: 'validation_failed', message: 'Fix', details: { password: 'Password must be at least 12 characters.' } } },
    }),
  });
  const u = userEvent.setup();
  renderAt('/register');
  await u.type(await screen.findByLabelText('Email'), 'a@example.com');
  await u.type(screen.getByLabelText('Password'), 'short');
  await u.click(screen.getByRole('button', { name: 'Create account' }));
  expect(await screen.findByText(/Password must be at least 12 characters\./)).toBeInTheDocument();
});

it('shows a generic error on bad credentials', async () => {
  mockFetch({
    'GET /api/v1/me': anonymous,
    'POST /api/v1/auth/login': () => ({
      status: 401,
      body: { error: { code: 'unauthenticated', message: 'Incorrect email or password.' } },
    }),
  });
  const u = userEvent.setup();
  renderAt('/login');
  await u.type(await screen.findByLabelText('Email'), 'a@example.com');
  await u.type(screen.getByLabelText('Password'), 'wrong password here');
  await u.click(screen.getByRole('button', { name: 'Log in' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Incorrect email or password.');
});

it('hides the operations area from users without the permission', async () => {
  mockFetch({ 'GET /api/v1/me': () => ({ body: user([]) }) });
  renderAt('/ops');
  expect(await screen.findByRole('heading', { name: 'Not permitted' })).toBeInTheDocument();
});

it('shows operations to users with the permission, and logout clears the session', async () => {
  mockFetch({
    'GET /api/v1/me': () => ({ body: user(['ops.applications.read']) }),
    'POST /api/v1/auth/logout': () => ({ status: 204 }),
  });
  const u = userEvent.setup();
  renderAt('/ops');
  expect(await screen.findByRole('heading', { name: 'Operations' })).toBeInTheDocument();
  await u.click(screen.getByRole('button', { name: 'Log out' }));
  await waitFor(() => expect(screen.getByRole('heading', { name: 'Log in' })).toBeInTheDocument());
});
