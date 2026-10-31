import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { TemplatesPage } from '../src/features/templates/Templates';
import { mockFetch } from './helpers';

afterEach(() => vi.restoreAllMocks());

const config = (o: object = {}) => ({
  preset: 'classic', orientation: 'landscape', accent_color: '#8a6d1d', title: 'Certificate of Completion',
  body: 'This is to certify that\n{{student_name}}', signatory_name: '', signatory_title: 'Director', footer: '',
  show_grade: false, show_completion_date: true, show_certificate_number: true, show_qr: true, ...o,
});
const template = (o: object = {}) => ({
  id: 't-1', name: 'Diploma', archived_at: null,
  current_version: { number: 2, created_at: '2026-10-01T00:00:00Z', config: config() },
  versions: [{ number: 2, created_at: '2026-10-02T00:00:00Z' }, { number: 1, created_at: '2026-10-01T00:00:00Z' }], ...o,
});
const presets = () => ({
  body: {
    presets: [{ preset: 'classic', label: 'Classic', config: config() }, { preset: 'modern', label: 'Modern', config: config({ preset: 'modern' }) }],
    placeholders: ['student_name', 'course_name'],
  },
});

it('lists templates; read-only users get no create or edit actions', async () => {
  mockFetch({ 'GET /api/v1/certificate-templates': () => ({ body: { templates: [template()], has_more: false } }) });
  render(<TemplatesPage canWrite={false} />);
  expect(await screen.findByRole('table', { name: 'Templates' })).toBeInTheDocument();
  expect(screen.getByText('Diploma')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'New template' })).not.toBeInTheDocument();
});

it('creates a template from a preset and previews it in a sandboxed iframe', async () => {
  let created = false;
  const fetchMock = mockFetch({
    'GET /api/v1/certificate-templates': () => ({ body: { templates: created ? [template()] : [], has_more: false } }),
    'GET /api/v1/certificate-presets': presets,
    'POST /api/v1/certificate-templates/preview': () => ({ body: { html: '<p>Sample certificate</p>' } }),
    'POST /api/v1/certificate-templates': () => {
      created = true;
      return { status: 201, body: { template: template() } };
    },
  });
  const user = userEvent.setup();
  render(<TemplatesPage canWrite />);
  await user.click(await screen.findByRole('button', { name: 'New template' }));
  await user.type(await screen.findByLabelText('Template name'), 'Diploma');
  await user.click(screen.getByRole('button', { name: 'Modern' }));
  expect(screen.getByLabelText('Style')).toHaveValue('modern');

  await user.click(screen.getByRole('button', { name: 'Preview with sample data' }));
  const frame = await screen.findByTitle('Certificate preview');
  expect(frame).toHaveAttribute('sandbox', ''); // no scripts, no same-origin, no network
  expect(frame).toHaveAttribute('srcdoc', '<p>Sample certificate</p>');

  await user.click(screen.getByRole('button', { name: 'Create template' }));
  expect(await screen.findByRole('table', { name: 'Templates' })).toBeInTheDocument();
  const post = fetchMock.mock.calls.find(([u, i]) => String(u).endsWith('/api/v1/certificate-templates') && i?.method === 'POST')!;
  const body = JSON.parse(String(post[1]!.body));
  expect(body.name).toBe('Diploma');
  expect(body.config.preset).toBe('modern');
});

it('shows field problems from the server', async () => {
  mockFetch({
    'GET /api/v1/certificate-templates': () => ({ body: { templates: [], has_more: false } }),
    'GET /api/v1/certificate-presets': presets,
    'POST /api/v1/certificate-templates': () => ({
      status: 422,
      body: { error: { code: 'validation_failed', message: 'Please correct the highlighted fields.', details: { body: 'The text must include {{student_name}}.' } } },
    }),
  });
  const user = userEvent.setup();
  render(<TemplatesPage canWrite />);
  await user.click(await screen.findByRole('button', { name: 'New template' }));
  await user.type(await screen.findByLabelText('Template name'), 'Diploma');
  await user.click(screen.getByRole('button', { name: 'Create template' }));
  expect(await screen.findByText(/must include/)).toBeInTheDocument();
});

it('saves an edit as a new version and lists the versions', async () => {
  const fetchMock = mockFetch({
    'GET /api/v1/certificate-templates': () => ({ body: { templates: [template()], has_more: false } }),
    'GET /api/v1/certificate-templates/t-1': () => ({ body: { template: template() } }),
    'GET /api/v1/certificate-presets': presets,
    'POST /api/v1/certificate-templates/t-1/versions': () => ({ status: 201, body: { version: { number: 3, created_at: '2026-10-03T00:00:00Z' } } }),
  });
  const user = userEvent.setup();
  render(<TemplatesPage canWrite />);
  await user.click(await screen.findByRole('button', { name: 'Open Diploma' }));
  expect(await screen.findByText('Version 1', { exact: false })).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Edit (new version)' }));
  const title = await screen.findByLabelText('Title');
  await user.clear(title);
  await user.type(title, 'New heading');
  await user.click(screen.getByRole('button', { name: 'Save as new version' }));
  await waitFor(() => expect(fetchMock.mock.calls.some(([u, i]) => String(u).endsWith('/t-1/versions') && i?.method === 'POST')).toBe(true));
  const post = fetchMock.mock.calls.find(([u, i]) => String(u).endsWith('/t-1/versions') && i?.method === 'POST')!;
  expect(JSON.parse(String(post[1]!.body)).config.title).toBe('New heading');
});
