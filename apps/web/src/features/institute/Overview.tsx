import { useState } from 'react';
import { useAsync } from '../../lib/useAsync';
import { Loading } from '../../components/States';
import { dashboardApi, PERIODS } from './dashboardApi';

const JOB_LABEL: Record<string, string> = {
  queued: 'Waiting to start', processing: 'Issuing', completed: 'Completed', completed_with_errors: 'Completed with errors', failed: 'Failed', cancelled: 'Cancelled',
};

const fmt = (iso: string) => new Date(iso).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });

export function PeriodSelect({ value, onChange }: { value: number; onChange: (n: number) => void }) {
  return (
    <p>
      <label htmlFor="dash-period">Period</label>{' '}
      <select id="dash-period" value={value} onChange={(e) => onChange(Number(e.target.value))}>
        {PERIODS.map((p) => <option key={p} value={p}>Last {p} days</option>)}
      </select>
    </p>
  );
}

/** Institute overview. Sections the member may not see are simply absent from the API response. */
export function Overview() {
  const [days, setDays] = useState(30);
  const { state, retry } = useAsync(() => dashboardApi.institute(days), [days]);
  return (
    <section aria-labelledby="overview-h">
      <h3 id="overview-h">Overview</h3>
      <PeriodSelect value={days} onChange={setDays} />
      {state.status === 'loading' && <Loading />}
      {state.status === 'error' && (
        <p role="status">The overview could not be loaded. <button type="button" onClick={retry}>Try again</button></p>
      )}
      {state.status === 'success' && <OverviewBody d={state.data.dashboard} />}
    </section>
  );
}

function OverviewBody({ d }: { d: import('./dashboardApi').InstituteDashboard }) {
  return (
    <div>
      {d.records && (
        <dl aria-label="Records">
          <dt>Students</dt><dd>{d.records.active_students}{d.records.archived_students > 0 ? ` (${d.records.archived_students} archived)` : ''}</dd>
          <dt>Active batches</dt><dd>{d.records.active_batches}</dd>
        </dl>
      )}
      {d.certificates && (
        <dl aria-label="Certificates">
          <dt>Issued in the last {d.days} days</dt><dd>{d.certificates.issued_in_period}</dd>
          <dt>Valid certificates in total</dt><dd>{d.certificates.issued}</dd>
          <dt>Revoked</dt><dd>{d.certificates.revoked}</dd>
          <dt>Could not be issued</dt><dd>{d.certificates.failed}</dd>
        </dl>
      )}
      {d.last_job !== undefined && (
        <p>
          Latest bulk job:{' '}
          {d.last_job ? (
            <span>{d.last_job.batch_name} — {JOB_LABEL[d.last_job.status] ?? d.last_job.status}, {d.last_job.succeeded} of {d.last_job.total} issued{d.last_job.failed > 0 ? `, ${d.last_job.failed} failed` : ''} ({fmt(d.last_job.created_at)})</span>
          ) : 'none yet'}
        </p>
      )}
      {d.deliveries && (
        <dl aria-label="Email deliveries">
          <dt>Emails requested in this period</dt>
          <dd>
            {d.deliveries.accepted + d.deliveries.failed + d.deliveries.queued + d.deliveries.sending} — {d.deliveries.accepted} accepted by the mail server, {d.deliveries.failed} failed, {d.deliveries.queued + d.deliveries.sending} waiting
          </dd>
        </dl>
      )}
      {d.recent && (
        d.recent.length === 0 ? <p>No certificates yet.</p> : (
          <table aria-label="Recent certificates">
            <thead><tr><th scope="col">Number</th><th scope="col">Student</th><th scope="col">Course</th><th scope="col">Status</th><th scope="col">Date</th></tr></thead>
            <tbody>
              {d.recent.map((c) => (
                <tr key={c.id}><td>{c.certificate_number}</td><td>{c.student_name}</td><td>{c.course_name}</td><td>{c.status === 'revoked' ? 'Revoked' : 'Issued'}</td><td>{fmt(c.at)}</td></tr>
              ))}
            </tbody>
          </table>
        )
      )}
    </div>
  );
}
