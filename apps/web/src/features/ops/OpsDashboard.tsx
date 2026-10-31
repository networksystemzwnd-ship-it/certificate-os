import { useState } from 'react';
import { useAsync } from '../../lib/useAsync';
import { ErrorState, Loading } from '../../components/States';
import { dashboardApi } from '../institute/dashboardApi';
import { PeriodSelect } from '../institute/Overview';

const DECISION: Record<string, string> = {
  'application.approved': 'Application approved', 'application.rejected': 'Application rejected', 'application.changes_requested': 'Changes requested',
};

/** Platform overview: aggregate counts only, never student or recipient data. */
export function OpsDashboard() {
  const [days, setDays] = useState(30);
  const { state, retry } = useAsync(() => dashboardApi.platform(days), [days]);
  return (
    <section aria-labelledby="ops-overview-h">
      <h2 id="ops-overview-h">Overview</h2>
      <PeriodSelect value={days} onChange={setDays} />
      {state.status === 'loading' && <Loading />}
      {state.status === 'error' && <ErrorState message={state.error.message} onRetry={retry} />}
      {state.status === 'success' && (() => {
        const d = state.data.dashboard;
        const pending = (d.applications.submitted ?? 0) + (d.applications.under_review ?? 0) + (d.applications.changes_requested ?? 0);
        return (
          <div>
            <dl aria-label="Institutes and applications">
              <dt>Applications waiting for review or changes</dt><dd>{pending}</dd>
              <dt>Active institutes</dt><dd>{d.institutes.active ?? 0}</dd>
              <dt>Suspended institutes</dt><dd>{d.institutes.suspended ?? 0}</dd>
              <dt>Closed institutes</dt><dd>{d.institutes.closed ?? 0}</dd>
            </dl>
            <dl aria-label="Volume">
              <dt>Certificates issued in the last {d.days} days</dt><dd>{d.volume.certificates_issued}</dd>
              <dt>Certificates revoked</dt><dd>{d.volume.certificates_revoked}</dd>
              <dt>Bulk jobs started</dt><dd>{d.volume.bulk_jobs_started}</dd>
              <dt>Emails accepted by the mail server</dt><dd>{d.volume.emails_accepted}</dd>
              <dt>Emails that failed</dt><dd>{d.volume.emails_failed}</dd>
            </dl>
            <h3>Recent decisions</h3>
            {d.recent_decisions.length === 0 ? <p>No decisions yet.</p> : (
              <ul>{d.recent_decisions.map((r, i) => <li key={i}>{DECISION[r.action] ?? r.action} — {new Date(r.at).toLocaleString()}</li>)}</ul>
            )}
          </div>
        );
      })()}
    </section>
  );
}
