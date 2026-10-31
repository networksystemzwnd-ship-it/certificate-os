import { api } from '../../lib/api';

export interface Member { id: string; user_id: string; email: string; role: 'owner' | 'admin' | 'staff'; grants: string[]; status: 'active' | 'removed' }
export interface Invitation {
  id: string; email: string; role: 'admin' | 'staff'; grants: string[];
  status: 'pending' | 'accepted' | 'revoked' | 'expired'; created_at: string; expires_at: string;
}

const json = (body: unknown): RequestInit => ({ body: JSON.stringify(body) });

export const staffApi = {
  members: () => api<{ members: Member[] }>('/api/v1/institute/members'),
  invitations: () => api<{ invitations: Invitation[] }>('/api/v1/institute/invitations'),
  invite: (b: { email: string; role: string; grants: string[] }) =>
    api<{ invitation: Invitation; token: string; accept_path: string }>('/api/v1/institute/invitations', { method: 'POST', ...json(b) }),
  revoke: (id: string) => api<{ invitation: Invitation }>(`/api/v1/institute/invitations/${id}`, { method: 'DELETE' }),
  changeMember: (id: string, b: { role: string; grants: string[] }) =>
    api<{ member: Member }>(`/api/v1/institute/members/${id}`, { method: 'PATCH', ...json(b) }),
  removeMember: (id: string) => api<{ removed: boolean }>(`/api/v1/institute/members/${id}`, { method: 'DELETE' }),
  accept: (token: string) =>
    api<{ institute: { id: string; slug: string; name: string }; role: string }>('/api/v1/invitations/accept', { method: 'POST', ...json({ token }) }),
};

/** Extra permissions an owner can give one staff member (staff keep everything else by default). */
export const GRANTABLE: { key: string; label: string }[] = [
  { key: 'certificates.issue', label: 'Issue certificates' },
  { key: 'certificates.revoke', label: 'Revoke certificates' },
  { key: 'certificates.deliver', label: 'Send certificates' },
  { key: 'certificates.export', label: 'Download certificate ZIPs' },
];
