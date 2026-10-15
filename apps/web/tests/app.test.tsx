import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../src/app/App';

afterEach(() => vi.restoreAllMocks());

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  );
}

it('shows loading then ready on the home page', async () => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ status: 'ok' })));
  renderAt('/');
  expect(screen.getByRole('status')).toBeInTheDocument();
  expect(await screen.findByText('API is ready.')).toBeInTheDocument();
});

it('shows an accessible error with retry when the API is down', async () => {
  vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('down'));
  renderAt('/');
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not reach the server');
  expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument();
});

it('renders placeholder routes and a 404', () => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}'));
  renderAt('/login');
  expect(screen.getByRole('heading', { name: 'Log in' })).toBeInTheDocument();
});

it('renders not-found for unknown paths', () => {
  renderAt('/nope');
  expect(screen.getByRole('heading', { name: 'Page not found' })).toBeInTheDocument();
});
