import { api } from '../../lib/api';

export type Status = 'draft' | 'submitted' | 'under_review' | 'changes_requested' | 'approved' | 'rejected';

export interface Details {
  name: string;
  slug: string;
  category: string;
  address_line1: string;
  address_line2: string;
  city: string;
  region: string;
  postal_code: string;
  country: string;
  official_email: string;
  phone: string;
  rep_name: string;
  rep_role: string;
  website: string;
  attested: boolean;
}

export interface Application {
  id: string;
  status: Status;
  details: Details;
  editable: boolean;
  version: number;
  submitted_at: string | null;
  reviewed_at: string | null;
  decision_reason?: string;
  institute_id?: string;
  applicant_user_id?: string; // operations view only
  created_at: string;
  updated_at: string;
}

export interface HistoryEntry {
  id: string;
  action: string;
  actor_type: string;
  actor_user_id?: string;
  occurred_at: string;
  metadata?: Record<string, unknown>;
}

export const STATUS_LABEL: Record<Status, string> = {
  draft: 'Draft',
  submitted: 'Submitted — waiting for review',
  under_review: 'Under review',
  changes_requested: 'Changes requested',
  approved: 'Approved',
  rejected: 'Rejected',
};

export const CATEGORIES: { value: string; label: string }[] = [
  { value: 'training_institute', label: 'Training institute' },
  { value: 'academy', label: 'Academy' },
  { value: 'vocational_centre', label: 'Vocational centre' },
  { value: 'school', label: 'School' },
  { value: 'college', label: 'College' },
  { value: 'other', label: 'Other' },
];

const A = '/api/v1/institute-applications';
const O = '/api/v1/ops/institute-applications';
const post = (path: string, body?: unknown) =>
  api<{ application: Application }>(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) });

export const applicantApi = {
  list: () => api<{ applications: Application[] }>(A).then((r) => r.applications),
  create: (d: Partial<Details>) => post(A, d).then((r) => r.application),
  update: (id: string, d: Partial<Details>) =>
    api<{ application: Application }>(`${A}/${id}`, { method: 'PATCH', body: JSON.stringify(d) }).then((r) => r.application),
  submit: (id: string) => post(`${A}/${id}/submit`).then((r) => r.application),
};

export const opsApi = {
  list: (statuses: Status[]) =>
    api<{ applications: Application[] }>(`${O}?status=${statuses.join(',')}`).then((r) => r.applications),
  get: (id: string) => api<{ application: Application }>(`${O}/${id}`).then((r) => r.application),
  history: (id: string) => api<{ history: HistoryEntry[] }>(`${O}/${id}/history`).then((r) => r.history),
  startReview: (id: string) => post(`${O}/${id}/start-review`).then((r) => r.application),
  approve: (id: string, note: string) => post(`${O}/${id}/approve`, { note }).then((r) => r.application),
  reject: (id: string, reason: string) => post(`${O}/${id}/reject`, { reason }).then((r) => r.application),
  requestChanges: (id: string, reason: string) => post(`${O}/${id}/request-changes`, { reason }).then((r) => r.application),
};

export interface OpsInstitute {
  id: string;
  slug: string;
  name: string;
  status: 'active' | 'suspended' | 'closed';
  created_at: string;
}

export const opsInstitutesApi = {
  list: (q: string) =>
    api<{ institutes: OpsInstitute[] }>(`/api/v1/ops/institutes?q=${encodeURIComponent(q)}`).then((r) => r.institutes),
  suspend: (id: string, reason: string) =>
    api(`/api/v1/ops/institutes/${id}/suspend`, { method: 'POST', body: JSON.stringify({ reason }) }),
  reactivate: (id: string) => api(`/api/v1/ops/institutes/${id}/reactivate`, { method: 'POST' }),
};

export interface AuditRecord {
  id: string;
  occurred_at: string;
  actor_type: string;
  actor_user_id?: string;
  action: string;
  entity_type: string;
  entity_id: string;
  institute_id?: string;
  request_id?: string;
  metadata?: Record<string, unknown>;
}

export const auditApi = {
  list: (entityType: string, action: string) => {
    const p = new URLSearchParams();
    if (entityType) p.set('entity_type', entityType);
    if (action) p.set('action', action);
    return api<{ records: AuditRecord[] }>(`/api/v1/ops/audit?${p.toString()}`).then((r) => r.records);
  },
};
