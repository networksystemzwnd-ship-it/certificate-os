import { api } from '../../lib/api';

export interface InstituteDashboard {
  days: number; since: string;
  records?: { active_students: number; archived_students: number; active_batches: number };
  certificates?: { issued_in_period: number; issued: number; revoked: number; failed: number };
  last_job?: { id: string; status: string; batch_name: string; total: number; succeeded: number; failed: number; created_at: string } | null;
  deliveries?: { queued: number; sending: number; accepted: number; failed: number };
  recent?: { id: string; certificate_number: string; student_name: string; course_name: string; status: string; at: string }[];
}

export interface PlatformDashboard {
  days: number; since: string;
  applications: Record<string, number>; institutes: Record<string, number>;
  volume: { certificates_issued: number; certificates_revoked: number; bulk_jobs_started: number; emails_accepted: number; emails_failed: number };
  recent_decisions: { action: string; at: string }[];
}

export const PERIODS = [7, 30, 90, 365] as const;

export const dashboardApi = {
  institute: (days: number) => api<{ dashboard: InstituteDashboard }>(`/api/v1/dashboard?days=${days}`),
  platform: (days: number) => api<{ dashboard: PlatformDashboard }>(`/api/v1/ops/dashboard?days=${days}`),
};
