import { useState } from 'react';
import { ApiError } from '../../lib/api';
import { useAsync } from '../../lib/useAsync';
import { EmptyState, ErrorState, Loading } from '../../components/States';
import { fieldErrors } from '../auth/session';
import { opsInstitutesApi, type OpsInstitute } from '../applications/api';

export function OpsInstitutes() {
  const [q, setQ] = useState('');
  const [term, setTerm] = useState('');
  const list = useAsync(() => opsInstitutesApi.list(term), [term]);
  const [target, setTarget] = useState<OpsInstitute | null>(null);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const fe = fieldErrors(error);

  async function run(fn: () => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      await fn();
      setTarget(null);
      setReason('');
      list.retry();
    } catch (err) {
      setError(err instanceof Error ? err : new Error('Something went wrong.'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section>
      <h2>Institutes</h2>
      <form onSubmit={(e) => { e.preventDefault(); setTerm(q.trim()); }} role="search">
        <label htmlFor="q">Search by name or address</label>{' '}
        <input id="q" value={q} onChange={(e) => setQ(e.target.value)} /> <button type="submit">Search</button>
      </form>
      <p><small>Suspended institutes become read-only for their members and cannot export. Closing an institute is done out-of-band by the platform team.</small></p>
      {list.state.status === 'loading' && <Loading />}
      {list.state.status === 'error' && <ErrorState message={list.state.error.message} onRetry={list.retry} />}
      {list.state.status === 'success' &&
        (list.state.data.length === 0 ? (
          <EmptyState title="No institutes found" />
        ) : (
          <table>
            <thead><tr><th scope="col">Name</th><th scope="col">Address</th><th scope="col">Status</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead>
            <tbody>
              {list.state.data.map((i) => (
                <tr key={i.id}>
                  <td>{i.name}</td>
                  <td>{i.slug}</td>
                  <td>{i.status}</td>
                  <td>
                    {i.status === 'active' && <button type="button" onClick={() => { setTarget(i); setError(null); }}>Suspend…</button>}
                    {i.status === 'suspended' && (
                      <button type="button" disabled={busy} onClick={() => run(() => opsInstitutesApi.reactivate(i.id))}>Reactivate</button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ))}
      {target && (
        <form onSubmit={(e) => { e.preventDefault(); void run(() => opsInstitutesApi.suspend(target.id, reason)); }} aria-label="Suspend institute">
          <h3>Suspend {target.name}</h3>
          <label htmlFor="reason">Reason (10–2000 characters, recorded in the audit log)</label><br />
          <textarea id="reason" rows={3} value={reason} onChange={(e) => setReason(e.target.value)} required aria-invalid={Boolean(fe.reason)} />
          {fe.reason && <span role="alert"> {fe.reason}</span>}
          {error && !fe.reason && <ErrorState message={error.message} requestId={error instanceof ApiError ? error.requestId : undefined} />}
          <p>
            <button type="submit" disabled={busy}>Confirm suspension</button>{' '}
            <button type="button" disabled={busy} onClick={() => setTarget(null)}>Cancel</button>
          </p>
        </form>
      )}
      {error && !target && <ErrorState message={error.message} />}
    </section>
  );
}
