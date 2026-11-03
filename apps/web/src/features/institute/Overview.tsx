import { useState } from 'react';
import { useAsync } from '../../lib/useAsync';
import { CertStatus } from '../../components/Badge';
import { Loading } from '../../components/States';
import type { Tab } from './Workspace';
import { dashboardApi, PERIODS } from './dashboardApi';

const JOB_LABEL: Record<string, string> = {
  queued: 'Waiting to start', processing: 'Issuing', completed: 'Completed', completed_with_errors: 'Completed with errors', failed: 'Failed', cancelled: 'Cancelled',
};

const fmt = (iso: string) => new Date(iso).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });

export function PeriodSelect({ value, onChange }: { value: number; onChange: (n: number) => void }) {
  return (
    <div className="toolbar">
      <label htmlFor="dash-period">Period</label>
      <select id="dash-period" value={value} onChange={(e) => onChange(Number(e.target.value))}>
        {PERIODS.map((p) => <option key={p} value={p}>Last {p} days</option>)}
      </select>
    </div>
  );
}

type Go = (t: Tab) => void;

/** Institute overview. Sections the member may not see are simply absent from the API response. */
export function Overview({ go, allowed = [] }: { go?: Go; allowed?: Tab[] }) {
  const [days, setDays] = useState(30);
  const { state, retry } = useAsync(() => dashboardApi.institute(days), [days]);
  return (
    <section aria-labelledby="overview-h">
      <div className="page-head">
        <h2 id="overview-h">Overview</h2>
        <PeriodSelect value={days} onChange={setDays} />
      </div>
      {state.status === 'loading' && <Loading />}
      {state.status === 'error' && (
        <p role="status" className="notice bad">The overview could not be loaded. <button type="button" onClick={retry}>Try again</button></p>
      )}
      {state.status === 'success' && <OverviewBody d={state.data.dashboard} go={go} allowed={allowed} />}
    </section>
  );
}

function plural(n: number, one: string, many: string) {
  return `${n} ${n === 1 ? one : many}`;
}

/** What to do next, in order, until the institute has issued its first certificate. */
function GettingStarted({ d, go, allowed }: { d: import('./dashboardApi').InstituteDashboard; go?: Go; allowed: Tab[] }) {
  if (!d.records) return null;
  const issuedAny = d.certificates ? d.certificates.issued > 0 || d.certificates.revoked > 0 || Boolean(d.last_job) : false;
  const steps = [
    {
      done: d.records.active_students > 0,
      title: 'Add your students',
      hint: 'Type them in one at a time, or import a spreadsheet (CSV or Excel).',
      actions: [
        { label: 'Add students', tab: 'students' as Tab, primary: true },
        ...(allowed.includes('import') ? [{ label: 'Import a spreadsheet', tab: 'import' as Tab, primary: false }] : []),
      ],
    },
    {
      done: d.records.active_batches > 0,
      title: 'Set up a course and a batch',
      hint: 'A batch is one group of students taking a course together. Certificates are issued per batch.',
      actions: [{ label: 'Add a course', tab: 'courses' as Tab, primary: true }],
    },
    ...(d.certificates
      ? [{
          done: issuedAny,
          title: 'Issue your first certificate',
          hint: 'Pick a design in Templates, mark students as completed, then issue one certificate or a whole batch.',
          actions: [
            ...(allowed.includes('templates') ? [{ label: 'Choose a template', tab: 'templates' as Tab, primary: false }] : []),
            { label: 'Issue certificates', tab: 'certificates' as Tab, primary: true },
          ],
        }]
      : []),
  ];
  if (steps.every((s) => s.done)) return null;
  const next = steps.findIndex((s) => !s.done);
  return (
    <section aria-labelledby="start-h">
      <h3 id="start-h">Getting started</h3>
      <ol className="steps">
        {steps.map((s, i) => (
          <li key={s.title} className={s.done ? 'done' : undefined}>
            <div>
              <div className="step-title">{s.title}</div>
              {!s.done && <p className="step-hint">{s.hint}</p>}
            </div>
            {!s.done && i === next && go && (
              <div className="actions">
                {s.actions.map((a) => (
                  <button key={a.label} type="button" className={a.primary ? 'primary' : undefined} onClick={() => go(a.tab)}>{a.label}</button>
                ))}
              </div>
            )}
          </li>
        ))}
      </ol>
    </section>
  );
}

function OverviewBody({ d, go, allowed }: { d: import('./dashboardApi').InstituteDashboard; go?: Go; allowed: Tab[] }) {
  const failedCerts = d.certificates?.failed ?? 0;
  const failedMail = d.deliveries?.failed ?? 0;
  // A brand-new institute sees only what to do next; the figures appear once there is something to count.
  const fresh = Boolean(d.records) && d.records!.active_students === 0 && !d.last_job
    && (d.certificates ? d.certificates.issued + d.certificates.revoked + d.certificates.failed === 0 : true);
  if (fresh && go) return <GettingStarted d={d} go={go} allowed={allowed} />;
  return (
    <div>
      <GettingStarted d={d} go={go} allowed={allowed} />
      {(failedCerts > 0 || failedMail > 0) && go && (
        <p role="status" className="notice warn">
          <strong>Needs attention:</strong>{' '}
          {failedCerts > 0 && `${plural(failedCerts, 'certificate', 'certificates')} could not be created. `}
          {failedMail > 0 && `${plural(failedMail, 'email', 'emails')} could not be sent. `}
          <button type="button" className="link" onClick={() => go('certificates')}>Open Certificates</button>
        </p>
      )}
      {d.records && (
        <dl aria-label="Records" className="stats">
          <div><dt>Students</dt><dd>{d.records.active_students}{d.records.archived_students > 0 && <small>{` (${d.records.archived_students} archived)`}</small>}</dd></div>
          <div><dt>Active batches</dt><dd>{d.records.active_batches}</dd></div>
        </dl>
      )}
      {d.certificates && (
        <dl aria-label="Certificates" className="stats">
          <div><dt>Issued in the last {d.days} days</dt><dd>{d.certificates.issued_in_period}</dd></div>
          <div><dt>Valid certificates in total</dt><dd>{d.certificates.issued}</dd></div>
          <div><dt>Revoked</dt><dd>{d.certificates.revoked}</dd></div>
          <div className={d.certificates.failed > 0 ? 'bad' : undefined}><dt>Could not be issued</dt><dd>{d.certificates.failed}</dd></div>
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
          <dt className="sr-only">Emails requested in this period</dt>
          <dd style={{ margin: 0 }}>
            <small>Emails requested in this period:</small>{' '}
            {d.deliveries.accepted + d.deliveries.failed + d.deliveries.queued + d.deliveries.sending} — {d.deliveries.accepted} accepted by the mail server, {d.deliveries.failed} failed, {d.deliveries.queued + d.deliveries.sending} waiting
          </dd>
        </dl>
      )}
      {d.recent && (
        d.recent.length === 0 ? <p>No certificates yet.</p> : (
          <>
            <h3>Recent certificates</h3>
            <table aria-label="Recent certificates">
              <thead><tr><th scope="col">Number</th><th scope="col">Student</th><th scope="col">Course</th><th scope="col">Status</th><th scope="col">Date</th></tr></thead>
              <tbody>
                {d.recent.map((c) => (
                  <tr key={c.id}><td className="mono">{c.certificate_number}</td><td>{c.student_name}</td><td>{c.course_name}</td><td><CertStatus status={c.status === 'revoked' ? 'revoked' : 'issued'} /></td><td className="num">{fmt(c.at)}</td></tr>
                ))}
              </tbody>
            </table>
          </>
        )
      )}
    </div>
  );
}
