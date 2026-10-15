import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, ApiError } from '../src/lib/api';

afterEach(() => vi.restoreAllMocks());

describe('api client', () => {
  it('sends a request id and returns JSON', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ status: 'ok' }), { status: 200 }),
    );
    const out = await api<{ status: string }>('/readyz', {}, 'http://x');
    expect(out.status).toBe('ok');
    const headers = new Headers((fetchMock.mock.calls[0][1] as RequestInit).headers);
    expect(headers.get('X-Request-ID')).toMatch(/^[0-9a-f]{24}$/);
  });

  it('normalizes the server error envelope', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({ error: { code: 'forbidden', message: 'Nope', request_id: 'abc12345' } }),
        { status: 403 },
      ),
    );
    await expect(api('/x', {}, 'http://x')).rejects.toMatchObject({
      status: 403,
      code: 'forbidden',
      requestId: 'abc12345',
    });
  });

  it('normalizes network failures and non-JSON errors', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValueOnce(new TypeError('fail'));
    await expect(api('/x', {}, 'http://x')).rejects.toBeInstanceOf(ApiError);
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response('<html>', { status: 502 }));
    await expect(api('/x', {}, 'http://x')).rejects.toMatchObject({ status: 502, code: 'unknown_error' });
  });
});
