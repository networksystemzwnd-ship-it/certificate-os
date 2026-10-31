import { api } from '../../lib/api';

export interface TemplateConfig {
  preset: 'classic' | 'modern' | 'minimal';
  orientation: 'landscape' | 'portrait';
  accent_color: string;
  title: string;
  body: string;
  signatory_name: string;
  signatory_title: string;
  footer: string;
  show_grade: boolean;
  show_completion_date: boolean;
  show_certificate_number: boolean;
  show_qr: boolean;
}
export interface VersionInfo { number: number; created_at: string; config?: TemplateConfig }
export interface Template {
  id: string; name: string; archived_at: string | null; current_version: VersionInfo; versions?: VersionInfo[];
}
export interface Preset { preset: string; label: string; config: TemplateConfig }

const json = (body: unknown): RequestInit => ({ body: JSON.stringify(body) });
const base = '/api/v1/certificate-templates';

export const templatesApi = {
  presets: () => api<{ presets: Preset[]; placeholders: string[] }>('/api/v1/certificate-presets'),
  list: (archived?: string) => api<{ templates: Template[]; has_more: boolean }>(`${base}${archived ? `?archived=${archived}` : ''}`),
  get: (id: string) => api<{ template: Template }>(`${base}/${id}`),
  create: (name: string, config: TemplateConfig) => api<{ template: Template }>(base, { method: 'POST', ...json({ name, config }) }),
  rename: (id: string, name: string) => api<{ template: Template }>(`${base}/${id}`, { method: 'PATCH', ...json({ name }) }),
  archive: (id: string) => api<{ template: Template }>(`${base}/${id}`, { method: 'DELETE' }),
  restore: (id: string) => api<{ template: Template }>(`${base}/${id}/restore`, { method: 'POST' }),
  addVersion: (id: string, config: TemplateConfig) => api<{ version: VersionInfo }>(`${base}/${id}/versions`, { method: 'POST', ...json({ config }) }),
  previewDraft: (config: TemplateConfig) => api<{ html: string }>(`${base}/preview`, { method: 'POST', ...json({ config }) }),
  previewVersion: (id: string, version: number) => api<{ html: string }>(`${base}/${id}/preview`, { method: 'POST', ...json({ version }) }),
};
