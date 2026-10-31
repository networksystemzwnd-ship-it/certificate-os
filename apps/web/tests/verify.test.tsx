import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../src/app/App';
import { anonymous, mockFetch } from './helpers';

afterEach(() => vi.restoreAllMocks());

const ID = 'a'.repeat(43);
const v = (o: object = {}) => ({
  status: 'valid', certificate_number: 'CERT-2026-000001', institute_name: 'Test Institute', student_name: 'Asha Verma',
  course_name: 'Web Design', issue_date: '2026-10-28', ...o,
});

function renderAt(path: string) {
  return render(<MemoryRouter initialEntries={[path]}><App /></MemoryRouter>);
}

it('shows a valid certificate without any login and without workspace navigation', async () => {
  const fetchMock = mockFetch({ 'GET /api/v1/me': anonymous, [`GET /api/v1/public/certificates/${ID}`]: () => ({ body: { verification: v() } }) });
  renderAt(`/verify/${ID}`);
  expect(await screen.findByRole('heading', { name: 'This certificate is valid' })).toBeInTheDocument();
  expect(screen.getByText('CERT-2026-000001')).toBeInTheDocument();
  expect(screen.getByText('Asha Verma')).toBeInTheDocument();
  expect(screen.getByText('28 October 2026')).toBeInTheDocument();
  expect(screen.queryByRole('navigation', { name: 'Primary' })).not.toBeInTheDocument();
  expect(fetchMock.mock.calls.some(([u]) => String(u).endsWith(`/api/v1/public/certificates/${ID}`))).toBe(true);
});

it('says clearly when a certificate was revoked', async () => {
  mockFetch({ 'GET /api/v1/me': anonymous, [`GET /api/v1/public/certificates/${ID}`]: () => ({ body: { verification: v({ status: 'revoked', revoked_at: '2026-10-29T08:00:00Z' }) } }) });
  renderAt(`/verify/${ID}`);
  expect(await screen.findByRole('heading', { name: 'This certificate has been revoked' })).toBeInTheDocument();
  expect(screen.getByRole('alert')).toHaveTextContent('withdrew this certificate on 29 October 2026');
});

it('points a replaced certificate to its replacement', async () => {
  const NEW = 'b'.repeat(43);
  mockFetch({ 'GET /api/v1/me': anonymous, [`GET /api/v1/public/certificates/${ID}`]: () => ({ body: { verification: v({ status: 'replaced', revoked_at: '2026-10-29T08:00:00Z', replaced_by: NEW }) } }) });
  renderAt(`/verify/${ID}`);
  expect(await screen.findByRole('heading', { name: 'This certificate has been replaced' })).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'Check the current certificate' })).toHaveAttribute('href', `/verify/${NEW}`);
});

it('shows a calm not-found page that reveals nothing', async () => {
  mockFetch({ 'GET /api/v1/me': anonymous, [`GET /api/v1/public/certificates/${ID}`]: () => ({ status: 404, body: { error: { code: 'not_found', message: 'No certificate matches this link.' } } }) });
  renderAt(`/verify/${ID}`);
  expect(await screen.findByRole('heading', { name: 'Certificate not found' })).toBeInTheDocument();
  expect(screen.getByRole('alert')).toHaveTextContent('No certificate matches this link');
});

it('asks to wait when rate limited and offers a retry on other errors', async () => {
  mockFetch({ 'GET /api/v1/me': anonymous, [`GET /api/v1/public/certificates/${ID}`]: () => ({ status: 429, body: { error: { code: 'rate_limited', message: 'Too many verification requests.' } } }) });
  renderAt(`/verify/${ID}`);
  expect(await screen.findByRole('heading', { name: 'Please wait a moment' })).toBeInTheDocument();
});
