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

let activeInstitute: string | null = null;
/** Selects which institute subsequent requests act on (sent as X-Institute-ID; the server verifies membership). */
export function setActiveInstitute(id: string | null) {
  activeInstitute = id;
}

function newRequestId(): string {
  const bytes = new Uint8Array(12);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

async function request(path: string, init: RequestInit, baseUrl: string): Promise<Response> {
  const headers = new Headers(init.headers);
  headers.set('X-Request-ID', newRequestId());
  // JSON by default; a FormData body (file upload) must keep the browser-generated multipart boundary.
  if (init.body && !(init.body instanceof FormData) && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
  if (activeInstitute) headers.set('X-Institute-ID', activeInstitute);
  // CSRF defence: the API requires this header on state-changing requests (cross-site forms cannot set it).
  const method = (init.method ?? 'GET').toUpperCase();
  if (method !== 'GET' && method !== 'HEAD') headers.set('X-CSRF-Protection', '1');

  let res: Response;
  try {
    res = await fetch(`${baseUrl}${path}`, { ...init, headers, credentials: 'include' });
  } catch {
    throw new ApiError(0, 'network_error', 'Could not reach the server. Check your connection and try again.');
  }
  if (res.ok) return res;

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

export async function api<T>(path: string, init: RequestInit = {}, baseUrl = BASE_URL): Promise<T> {
  const res = await request(path, init, baseUrl);
  return (res.status === 204 ? undefined : await res.json()) as T;
}

/** For text downloads (e.g. the import error report): same headers, credentials and error handling as api(). */
export async function apiText(path: string, init: RequestInit = {}, baseUrl = BASE_URL): Promise<string> {
  return (await request(path, init, baseUrl)).text();
}

/** For binary downloads (e.g. certificate PDFs): same headers, credentials and error handling as api(). */
export async function apiBlob(path: string, init: RequestInit = {}, baseUrl = BASE_URL): Promise<Blob> {
  return (await request(path, init, baseUrl)).blob();
}
