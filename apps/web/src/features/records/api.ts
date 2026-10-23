import { api, apiText } from '../../lib/api';

export interface Course { id: string; name: string; code: string; description: string; archived_at: string | null }
export interface Batch { id: string; course_id: string; course_name: string; name: string; start_date: string; end_date: string; archived_at: string | null }
export interface Enrollment {
  id: string; student_id: string; batch_id: string; batch_name: string; course_id: string; course_name: string;
  status: 'enrolled' | 'completed' | 'withdrawn'; completion_date: string; grade: string;
}
export interface Student {
  id: string; student_number: string; full_name: string; email: string; phone: string;
  archived_at: string | null; enrollments?: Enrollment[];
}

const json = (body: unknown): RequestInit => ({ body: JSON.stringify(body) });
const qs = (p: Record<string, string | number | undefined>) => {
  const u = new URLSearchParams();
  Object.entries(p).forEach(([k, v]) => v !== undefined && v !== '' && u.set(k, String(v)));
  const s = u.toString();
  return s ? `?${s}` : '';
};

export const coursesApi = {
  list: (p: { q?: string; archived?: string; limit?: number; offset?: number } = {}) =>
    api<{ courses: Course[]; has_more: boolean }>(`/api/v1/courses${qs(p)}`),
  create: (b: Partial<Course>) => api<{ course: Course }>('/api/v1/courses', { method: 'POST', ...json(b) }),
  update: (id: string, b: Partial<Course>) => api<{ course: Course }>(`/api/v1/courses/${id}`, { method: 'PATCH', ...json(b) }),
  archive: (id: string) => api<{ course: Course }>(`/api/v1/courses/${id}`, { method: 'DELETE' }),
  restore: (id: string) => api<{ course: Course }>(`/api/v1/courses/${id}/restore`, { method: 'POST' }),
};

export const batchesApi = {
  list: (p: { q?: string; course_id?: string; archived?: string; limit?: number; offset?: number } = {}) =>
    api<{ batches: Batch[]; has_more: boolean }>(`/api/v1/batches${qs(p)}`),
  create: (b: Partial<Batch>) => api<{ batch: Batch }>('/api/v1/batches', { method: 'POST', ...json(b) }),
  update: (id: string, b: Partial<Batch>) => api<{ batch: Batch }>(`/api/v1/batches/${id}`, { method: 'PATCH', ...json(b) }),
  archive: (id: string) => api<{ batch: Batch }>(`/api/v1/batches/${id}`, { method: 'DELETE' }),
  restore: (id: string) => api<{ batch: Batch }>(`/api/v1/batches/${id}/restore`, { method: 'POST' }),
};

export const studentsApi = {
  list: (p: { q?: string; course_id?: string; batch_id?: string; status?: string; archived?: string; limit?: number; offset?: number } = {}) =>
    api<{ students: Student[]; has_more: boolean }>(`/api/v1/students${qs(p)}`),
  get: (id: string) => api<{ student: Student }>(`/api/v1/students/${id}`),
  create: (b: Partial<Student>) => api<{ student: Student }>('/api/v1/students', { method: 'POST', ...json(b) }),
  update: (id: string, b: Partial<Student>) => api<{ student: Student }>(`/api/v1/students/${id}`, { method: 'PATCH', ...json(b) }),
  archive: (id: string) => api<{ student: Student }>(`/api/v1/students/${id}`, { method: 'DELETE' }),
  restore: (id: string) => api<{ student: Student }>(`/api/v1/students/${id}/restore`, { method: 'POST' }),
  enroll: (id: string, batch_id: string) => api<{ enrollment: Enrollment }>(`/api/v1/students/${id}/enrollments`, { method: 'POST', ...json({ batch_id }) }),
  updateEnrollment: (id: string, eid: string, b: Partial<Pick<Enrollment, 'status' | 'completion_date' | 'grade'>>) =>
    api<{ enrollment: Enrollment }>(`/api/v1/students/${id}/enrollments/${eid}`, { method: 'PATCH', ...json(b) }),
};

export interface ImportCounts { total: number; valid: number; errors: number; duplicates: number; imported: number; failed: number }
export interface ImportJob {
  id: string;
  status: 'uploaded' | 'validated' | 'confirming' | 'completed' | 'cancelled';
  filename: string;
  headers: string[];
  mapping: Record<string, number>;
  suggested_mapping?: Record<string, number>;
  date_format: 'iso' | 'dmy' | 'mdy';
  counts: ImportCounts;
  preview: { row: number; cells: string[]; state?: string; message?: string }[];
  issues: { row: number; state: string; message: string }[];
  issues_truncated: boolean;
}

export const importsApi = {
  upload: (file: File) => {
    const body = new FormData();
    body.append('file', file);
    return api<{ import: ImportJob }>('/api/v1/students/imports', { method: 'POST', body });
  },
  setMapping: (id: string, mapping: Record<string, number>, date_format: string) =>
    api<{ import: ImportJob }>(`/api/v1/students/imports/${id}/mapping`, { method: 'PUT', ...json({ mapping, date_format }) }),
  confirm: (id: string) => api<{ import: ImportJob }>(`/api/v1/students/imports/${id}/confirm`, { method: 'POST' }),
  cancel: (id: string) => api<{ import: ImportJob }>(`/api/v1/students/imports/${id}`, { method: 'DELETE' }),
  errorReport: (id: string) => apiText(`/api/v1/students/imports/${id}/errors`),
};
