import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { ApiError } from '../../lib/api';
import { useAsync } from '../../lib/useAsync';
import { EmptyState, ErrorState, Loading } from '../../components/States';
import { fieldErrors } from '../auth/session';
import { CATEGORIES, opsApi, STATUS_LABEL, type Application, type Status } from '../applications/api';

const FILTERS: { label: string; statuses: Status[] }[] = [
  { label: 'Needs action', statuses: ['submitted', 'under_review'] },
  { label: 'Waiting on applicant', statuses: ['changes_requested'] },
  { label: 'Decided', statuses: ['approved', 'rejected'] },
];

const fmt = (iso: string | null) => (iso ? new Date(iso).toLocaleString() : '—');
const errRef = (e: Error) => (e instanceof ApiError ? e.requestId : undefined);

export function ApplicationQueue() {
  const [idx, setIdx] = useState(0);
  const list = useAsync(() => opsApi.list(FILTERS[idx]!.statuses), [idx]);
  return (
    <section>
      <h2>Institute applications</h2>
      <div role="group" aria-label="Filter by status">
        {FILTERS.map((f, i) => (
          <button key={f.label} type="button" aria-pressed={i === idx} onClick={() => setIdx(i)}>
            {f.label}
          </button>
        ))}
      </div>
      {list.state.status === 'loading' && <Loading />}
      {list.state.status === 'error' && (
        <ErrorState message={list.state.error.message} requestId={errRef(list.state.error)} onRetry={list.retry} />
      )}
      {list.state.status === 'success' &&
        (list.state.data.length === 0 ? (
          <EmptyState title="Nothing here">
            <p>No applications in this view.</p>
          </EmptyState>
        ) : (
          <table>
            <thead>
              <tr>
                <th scope="col">Institute</th>
                <th scope="col">Address</th>
                <th scope="col">Status</th>
                <th scope="col">Submitted</th>
              </tr>
            </thead>
            <tbody>
              {list.state.data.map((a) => (
                <tr key={a.id}>
                  <td>
                    <Link to={`/ops/applications/${a.id}`}>{a.details.name}</Link>
                  </td>
                  <td>{a.details.slug}</td>
                  <td>{STATUS_LABEL[a.status]}</td>
                  <td>{fmt(a.submitted_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ))}
    </section>
  );
}

function Summary({ a }: { a: Application }) {
  const d = a.details;
  const cat = CATEGORIES.find((c) => c.value === d.category)?.label ?? d.category;
  const rows: [string, string][] = [
    ['Name', d.name], ['Workspace address', d.slug], ['Type', cat],
    ['Address', [d.address_line1, d.address_line2, d.city, d.region, d.postal_code, d.country].filter(Boolean).join(', ')],
    ['Official email', d.official_email], ['Phone', d.phone], ['Representative', `${d.rep_name} (${d.rep_role})`],
    ['Website', d.website || '—'], ['Applicant confirmed accuracy and authority', d.attested ? 'Yes' : 'No'],
    ['Applicant user', a.applicant_user_id ?? '—'],
  ];
  return (
    <dl>
      {rows.map(([k, v]) => (
        <div key={k}>
          <dt>{k}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  );
}

type Action = 'approve' | 'reject' | 'changes' | null;

export function ApplicationDetail() {
  const { id = '' } = useParams();
  const app = useAsync(() => opsApi.get(id), [id]);
  const history = useAsync(() => opsApi.history(id), [id]);
  const [action, setAction] = useState<Action>(null);
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const fe = fieldErrors(error);

  if (app.state.status === 'loading') return <Loading />;
  if (app.state.status === 'error') {
    const e = app.state.error;
    if (e instanceof ApiError && e.status === 404) {
      return <EmptyState title="Application not found"><Link to="/ops">Back to the queue</Link></EmptyState>;
    }
    return <ErrorState message={e.message} requestId={errRef(e)} onRetry={app.retry} />;
  }
  const a = app.state.data;

  async function run(fn: () => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      await fn();
      setAction(null);
      setText('');
      app.retry();
      history.retry();
    } catch (err) {
      setError(err instanceof Error ? err : new Error('Something went wrong.'));
    } finally {
      setBusy(false);
    }
  }

  const confirm = () => {
    if (action === 'approve') return run(() => opsApi.approve(a.id, text));
    if (action === 'reject') return run(() => opsApi.reject(a.id, text));
    return run(() => opsApi.requestChanges(a.id, text));
  };

  return (
    <section>
      <p><Link to="/ops">← Back to the queue</Link></p>
      <h2>{a.details.name}</h2>
      <p>
        <strong>{STATUS_LABEL[a.status]}</strong> · submitted {fmt(a.submitted_at)}
      </p>
      {a.decision_reason && <p>Reviewer note: {a.decision_reason}</p>}
      <Summary a={a} />

      {a.status === 'submitted' && (
        <button type="button" disabled={busy} onClick={() => run(() => opsApi.startReview(a.id))}>Start review</button>
      )}
      {a.status === 'under_review' && !action && (
        <p>
          <button type="button" onClick={() => setAction('approve')}>Approve…</button>{' '}
          <button type="button" onClick={() => setAction('changes')}>Request changes…</button>{' '}
          <button type="button" onClick={() => setAction('reject')}>Reject…</button>
        </p>
      )}
      {a.status === 'under_review' && action && (
        <form onSubmit={(e) => { e.preventDefault(); void confirm(); }} aria-label="Confirm decision">
          {action === 'approve' ? (
            <>
              <p>
                Approving creates the workspace <strong>{a.details.slug}</strong> and makes the applicant its owner. This cannot be undone
                from here.
              </p>
              <label htmlFor="note">Note (optional)</label><br />
              <textarea id="note" value={text} onChange={(e) => setText(e.target.value)} rows={3} />
              {fe.note && <span role="alert"> {fe.note}</span>}
            </>
          ) : (
            <>
              <label htmlFor="reason">{action === 'reject' ? 'Reason for rejecting' : 'What should the applicant change?'} (10–2000 characters; the applicant sees this)</label><br />
              <textarea id="reason" value={text} onChange={(e) => setText(e.target.value)} rows={4} required aria-invalid={Boolean(fe.reason)} />
              {fe.reason && <span role="alert"> {fe.reason}</span>}
            </>
          )}
          {error && !Object.keys(fe).length && <ErrorState message={error.message} requestId={errRef(error)} />}
          <p>
            <button type="submit" disabled={busy}>
              {action === 'approve' ? 'Confirm approval' : action === 'reject' ? 'Confirm rejection' : 'Send change request'}
            </button>{' '}
            <button type="button" disabled={busy} onClick={() => { setAction(null); setError(null); }}>Cancel</button>
          </p>
        </form>
      )}
      {error && !action && <ErrorState message={error.message} requestId={errRef(error)} />}

      <h3>History</h3>
      {history.state.status === 'loading' && <Loading />}
      {history.state.status === 'error' && <ErrorState message={history.state.error.message} onRetry={history.retry} />}
      {history.state.status === 'success' && (
        <ol reversed>
          {history.state.data.map((h) => (
            <li key={h.id}>
              <code>{h.action}</code> — {fmt(h.occurred_at)}
              {typeof h.metadata?.reason === 'string' ? ` — “${h.metadata.reason}”` : ''}
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
