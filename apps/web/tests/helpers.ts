import { vi } from 'vitest';

type Handler = (init: RequestInit | undefined) => { status?: number; body?: unknown };

/** Mocks fetch by "METHOD /path". Creates a fresh Response per call (bodies are single-use). */
export function mockFetch(routes: Record<string, Handler>) {
  return vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    const path = new URL(url, 'http://localhost').pathname;
    const key = `${(init?.method ?? 'GET').toUpperCase()} ${path}`;
    const handler = routes[key];
    if (!handler) return new Response(JSON.stringify({ error: { code: 'not_found', message: 'nope' } }), { status: 404 });
    const { status = 200, body = {} } = handler(init);
    return new Response(status === 204 ? null : JSON.stringify(body), { status });
  });
}

export const anonymous: Handler = () => ({
  status: 401,
  body: { error: { code: 'unauthenticated', message: 'Authentication required.' } },
});
