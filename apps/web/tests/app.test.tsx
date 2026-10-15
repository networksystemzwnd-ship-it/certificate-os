import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../src/app/App';
import { anonymous, mockFetch } from './helpers';

afterEach(() => vi.restoreAllMocks());

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  );
}

it('shows loading then ready on the home page', async () => {
  mockFetch({ 'GET /api/v1/me': anonymous, 'GET /readyz': () => ({ body: { status: 'ok' } }) });
  renderAt('/');
  expect(await screen.findByText('API is ready.')).toBeInTheDocument();
});

it('shows an accessible error with retry when the API is down', async () => {
  vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('down'));
  renderAt('/');
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not reach the server');
  expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument();
});

it('renders not-found for unknown paths', async () => {
  mockFetch({ 'GET /api/v1/me': anonymous });
  renderAt('/nope');
  expect(await screen.findByRole('heading', { name: 'Page not found' })).toBeInTheDocument();
});
