import { api, apiBlob } from '../../lib/api';

export interface Certificate {
  id: string; certificate_number: string; status: 'pending' | 'issued' | 'failed' | 'revoked';
  student_id: string; student_name: string; course_name: string; batch_name: string; completion_date: string; grade: string;
  issue_date: string; template_name: string; template_version: number; verify_url: string;
  created_at: string; issued_at: string | null; failure?: string; revoked_at: string | null; revoke_reason?: string;
}

const json = (body: unknown): RequestInit => ({ body: JSON.stringify(body) });
const qs = (p: Record<string, string | number | undefined>) => {
  const u = new URLSearchParams();
  Object.entries(p).forEach(([k, v]) => v !== undefined && v !== '' && u.set(k, String(v)));
  const s = u.toString();
  return s ? `?${s}` : '';
};

export const certificatesApi = {
  list: (p: { status?: string; q?: string; limit?: number; offset?: number } = {}) =>
    api<{ certificates: Certificate[]; has_more: boolean }>(`/api/v1/certificates${qs(p)}`),
  issue: (enrollment_id: string, template_id: string, template_version?: number) =>
    api<{ certificate: Certificate }>('/api/v1/certificates', { method: 'POST', ...json({ enrollment_id, template_id, template_version: template_version ?? 0 }) }),
  revoke: (id: string, reason: string) => api<{ certificate: Certificate }>(`/api/v1/certificates/${id}/revoke`, { method: 'POST', ...json({ reason }) }),
  // The download needs the institute header, so it is fetched with the shared client and saved from a blob.
  pdf: (id: string) => apiBlob(`/api/v1/certificates/${id}/pdf`),
};
