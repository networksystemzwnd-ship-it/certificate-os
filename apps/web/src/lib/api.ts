// Shared API client: base URL, request IDs, normalized errors.
// Never put credentials in the frontend; the server is authoritative for permissions.

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public requestId?: string,
    public details?: unknown,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

const BASE_URL: string = import.meta.env?.VITE_API_BASE_URL ?? '';

function newRequestId(): string {
  const bytes = new Uint8Array(12);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

export async function api<T>(path: string, init: RequestInit = {}, baseUrl = BASE_URL): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set('X-Request-ID', newRequestId());
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
  // CSRF defence: the API requires this header on state-changing requests (cross-site forms cannot set it).
  const method = (init.method ?? 'GET').toUpperCase();
  if (method !== 'GET' && method !== 'HEAD') headers.set('X-CSRF-Protection', '1');

  let res: Response;
  try {
    res = await fetch(`${baseUrl}${path}`, { ...init, headers, credentials: 'include' });
  } catch {
    throw new ApiError(0, 'network_error', 'Could not reach the server. Check your connection and try again.');
  }

  if (res.ok) return (res.status === 204 ? undefined : await res.json()) as T;

  let body: { error?: { code?: string; message?: string; request_id?: string; details?: unknown } } = {};
  try {
    body = await res.json();
  } catch {
    /* non-JSON error body */
  }
  const e = body.error;
  throw new ApiError(
    res.status,
    e?.code ?? 'unknown_error',
    e?.message ?? 'Something went wrong. Please try again.',
    e?.request_id ?? res.headers.get('X-Request-ID') ?? undefined,
    e?.details,
  );
}
