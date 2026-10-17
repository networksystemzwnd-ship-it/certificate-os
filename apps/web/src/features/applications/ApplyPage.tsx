import { useState, type FormEvent } from 'react';
import { ApiError } from '../../lib/api';
import { useAsync } from '../../lib/useAsync';
import { ErrorState, Loading } from '../../components/States';
import { fieldErrors } from '../auth/session';
import { applicantApi, CATEGORIES, STATUS_LABEL, type Application, type Details } from './api';

const empty: Details = {
  name: '', slug: '', category: '', address_line1: '', address_line2: '', city: '', region: '', postal_code: '',
  country: '', official_email: '', phone: '', rep_name: '', rep_role: '', website: '', attested: false,
};

type TextKey = Exclude<keyof Details, 'attested' | 'category'>;
const TEXT_FIELDS: { key: TextKey; label: string; hint?: string; type?: string; autoComplete?: string }[] = [
  { key: 'name', label: 'Institute name' },
  { key: 'slug', label: 'Workspace address', hint: '3–40 lowercase letters, digits or hyphens. Cannot be changed later.' },
  { key: 'address_line1', label: 'Address line 1', autoComplete: 'address-line1' },
  { key: 'address_line2', label: 'Address line 2 (optional)', autoComplete: 'address-line2' },
  { key: 'city', label: 'City' },
  { key: 'region', label: 'State / region (optional)' },
  { key: 'postal_code', label: 'Postal code (optional)' },
  { key: 'country', label: 'Country code', hint: 'Two letters, e.g. IN.' },
  { key: 'official_email', label: 'Official email', type: 'email' },
  { key: 'phone', label: 'Phone', type: 'tel' },
  { key: 'rep_name', label: 'Representative name' },
  { key: 'rep_role', label: 'Representative role' },
  { key: 'website', label: 'Website (optional)', type: 'url', hint: 'Starts with https://' },
];

function ApplicationForm({ app, onChanged }: { app: Application | null; onChanged: () => void }) {
  const [current, setCurrent] = useState<Application | null>(app); // the draft once it exists server-side
  const [d, setD] = useState<Details>(app?.details ?? empty);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [saved, setSaved] = useState(false);
  const fe = fieldErrors(error);

  async function save(): Promise<Application | null> {
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      const result = current ? await applicantApi.update(current.id, d) : await applicantApi.create(d);
      setCurrent(result);
      setSaved(true);
      return result;
    } catch (err) {
      setError(err instanceof Error ? err : new Error('Something went wrong.'));
      return null;
    } finally {
      setBusy(false);
    }
  }

  async function onSave(e: FormEvent) {
    e.preventDefault();
    await save(); // stays on the form; onChanged is only needed once the status changes (submit)
  }

  async function onSubmitForReview() {
    const a = await save();
    if (!a) return;
    setBusy(true);
    try {
      await applicantApi.submit(a.id);
      onChanged();
    } catch (err) {
      setError(err instanceof Error ? err : new Error('Something went wrong.'));
    } finally {
      setBusy(false);
    }
  }

  const set = <K extends keyof Details>(k: K, v: Details[K]) => setD((p) => ({ ...p, [k]: v }));
  const submitDisabled = busy;

  return (
    <form onSubmit={onSave} noValidate>
      {current?.status === 'changes_requested' && current.decision_reason && (
        <div role="note" className="state">
          <strong>The reviewer asked for changes:</strong> {current.decision_reason}
        </div>
      )}
      <p>
        <label htmlFor="category">Type of institute</label>
        <br />
        <select id="category" value={d.category} onChange={(e) => set('category', e.target.value)} aria-invalid={Boolean(fe.category)}>
          <option value="">Choose…</option>
          {CATEGORIES.map((c) => (
            <option key={c.value} value={c.value}>{c.label}</option>
          ))}
        </select>
        {fe.category && <span role="alert"> {fe.category}</span>}
      </p>
      {TEXT_FIELDS.map((f) => (
        <p key={f.key}>
          <label htmlFor={f.key}>{f.label}</label>
          <br />
          <input
            id={f.key}
            type={f.type ?? 'text'}
            autoComplete={f.autoComplete}
            value={d[f.key]}
            onChange={(e) => set(f.key, e.target.value)}
            aria-invalid={Boolean(fe[f.key])}
            aria-describedby={fe[f.key] ? `${f.key}-error` : f.hint ? `${f.key}-hint` : undefined}
          />
          {f.hint && !fe[f.key] && <small id={`${f.key}-hint`}> {f.hint}</small>}
          {fe[f.key] && <span id={`${f.key}-error`} role="alert"> {fe[f.key]}</span>}
        </p>
      ))}
      <p>
        <label>
          <input type="checkbox" checked={d.attested} onChange={(e) => set('attested', e.target.checked)} /> I confirm the information is
          accurate and that I am authorized to apply on behalf of this institute.
        </label>
        {fe.attested && <span role="alert"> {fe.attested}</span>}
      </p>
      {error && !Object.keys(fe).length && (
        <ErrorState message={error.message} requestId={error instanceof ApiError ? error.requestId : undefined} />
      )}
      {saved && <p role="status">Saved.</p>}
      <button type="submit" disabled={busy}>Save draft</button>{' '}
      <button type="button" disabled={submitDisabled} onClick={onSubmitForReview}>
        Submit for review
      </button>
    </form>
  );
}

export function ApplyPage() {
  const list = useAsync(() => applicantApi.list(), []);
  if (list.state.status === 'loading') return <Loading />;
  if (list.state.status === 'error') {
    const e = list.state.error;
    return <ErrorState message={e.message} requestId={e instanceof ApiError ? e.requestId : undefined} onRetry={list.retry} />;
  }
  const apps = list.state.data;
  const open = apps.find((a) => a.status !== 'approved' && a.status !== 'rejected') ?? null;
  const past = apps.filter((a) => a !== open);

  return (
    <section>
      <h1>Apply for an institute workspace</h1>
      {open && !open.editable && (
        <div role="status" className="state">
          <p>
            <strong>{STATUS_LABEL[open.status]}</strong>
          </p>
          <p>
            {open.details.name} ({open.details.slug}). We'll review your application; you cannot edit it while it is being reviewed.
          </p>
        </div>
      )}
      {(!open || open.editable) && <ApplicationForm key={open?.id ?? 'new'} app={open} onChanged={list.retry} />}
      {past.length > 0 && (
        <>
          <h2>Earlier applications</h2>
          <ul>
            {past.map((a) => (
              <li key={a.id}>
                {a.details.name || '(unnamed)'} — {STATUS_LABEL[a.status]}
                {a.status === 'rejected' && a.decision_reason ? `: ${a.decision_reason}` : ''}
                {a.status === 'approved' && ' — open it from your workspace.'}
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}
