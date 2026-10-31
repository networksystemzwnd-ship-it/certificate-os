import { api, apiBlob } from '../../lib/api';

export interface Certificate {
  id: string; certificate_number: string; status: 'pending' | 'issued' | 'failed' | 'revoked';
  student_id: string; student_name: string; course_name: string; batch_name: string; completion_date: string; grade: string;
  issue_date: string; template_name: string; template_version: number; verify_url: string;
  created_at: string; issued_at: string | null; failure?: string; revoked_at: string | null; revoke_reason?: string;
  replaces_id?: string; replaced_by_id?: string;
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
  replace: (id: string, reason: string) => api<{ certificate: Certificate }>(`/api/v1/certificates/${id}/replace`, { method: 'POST', ...json({ reason }) }),
  revoke: (id: string, reason: string) => api<{ certificate: Certificate }>(`/api/v1/certificates/${id}/revoke`, { method: 'POST', ...json({ reason }) }),
  // The download needs the institute header, so it is fetched with the shared client and saved from a blob.
  pdf: (id: string) => apiBlob(`/api/v1/certificates/${id}/pdf`),
};

export type JobStatus = 'queued' | 'processing' | 'completed' | 'completed_with_errors' | 'failed' | 'cancelled';
export interface BulkJob {
  id: string; status: JobStatus; batch_id: string; batch_name: string; template_id: string; template_name: string; template_version: number;
  total: number; succeeded: number; failed: number; remaining: number; skipped_already_issued: number; skipped_not_eligible: number;
  created_at: string; started_at: string | null; finished_at: string | null;
}
export interface BulkItem {
  enrollment_id: string; student_name: string; position: number; status: 'queued' | 'processing' | 'succeeded' | 'failed';
  attempts: number; certificate_id?: string; certificate_number?: string; error?: string;
}
export interface BulkPreview { batch_name: string; eligible: number; already_issued: number; not_eligible: number; max_per_job: number }

export const isActive = (s: JobStatus) => s === 'queued' || s === 'processing';

export const bulkApi = {
  preview: (batch_id: string, template_id: string) =>
    api<{ preview: BulkPreview }>(`/api/v1/certificates/bulk-preview${qs({ batch_id, template_id })}`),
  start: (batch_id: string, template_id: string) =>
    api<{ job: BulkJob }>('/api/v1/certificates/bulk', { method: 'POST', ...json({ batch_id, template_id, template_version: 0 }) }),
  list: (limit = 5) => api<{ jobs: BulkJob[]; has_more: boolean }>(`/api/v1/bulk-jobs${qs({ limit })}`),
  get: (id: string) => api<{ job: BulkJob }>(`/api/v1/bulk-jobs/${id}`),
  items: (id: string, status: string, limit = 100) => api<{ items: BulkItem[]; has_more: boolean }>(`/api/v1/bulk-jobs/${id}/items${qs({ status, limit })}`),
  retryFailed: (id: string) => api<{ job: BulkJob }>(`/api/v1/bulk-jobs/${id}/retry-failed`, { method: 'POST' }),
  resume: (id: string) => api<{ job: BulkJob }>(`/api/v1/bulk-jobs/${id}/resume`, { method: 'POST' }),
  cancel: (id: string) => api<{ job: BulkJob }>(`/api/v1/bulk-jobs/${id}/cancel`, { method: 'POST' }),
  exportZip: (id: string) => apiBlob(`/api/v1/bulk-jobs/${id}/export`),
};
