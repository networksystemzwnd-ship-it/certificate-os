import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, ApiError, setActiveInstitute } from '../../lib/api';
import { useAsync } from '../../lib/useAsync';
import { Badge } from '../../components/Badge';
import { EmptyState, ErrorState, Loading, Notice } from '../../components/States';
import { BatchesPage, CoursesPage, StudentsPage } from '../records/Records';
import { ImportWizard } from '../records/ImportWizard';
import { StaffPage } from '../staff/Staff';
import { TemplatesPage } from '../templates/Templates';
import { CertificatesPage } from '../certificates/Certificates';
import { useSession } from '../auth/session';
import { Overview } from './Overview';

interface InstituteSummary {
  id: string;
  slug: string;
  name: string;
  status: string;
  role: string;
}

interface InstituteDetail extends InstituteSummary {
  permissions?: string[];
}

export type Tab = 'overview' | 'students' | 'courses' | 'batches' | 'templates' | 'certificates' | 'import' | 'staff';

function Detail({ id }: { id: string }) {
  const { user } = useSession();
  const [tab, setTab] = useState<Tab>('overview');
  const { state, retry } = useAsync(() => {
    setActiveInstitute(id); // the server still verifies membership on every request
    return api<{ institute: InstituteDetail }>('/api/v1/institute');
  }, [id]);

  if (state.status === 'loading') return <Loading label="Loading institute…" />;
  if (state.status === 'error') {
    return (
      <ErrorState
        message={state.error.message}
        requestId={state.error instanceof ApiError ? state.error.requestId : undefined}
        onRetry={retry}
      />
    );
  }
  const inst = state.data.institute;
  const can = (p: string) => inst.permissions?.includes(p) ?? false;
  // Grouped by the job a person is doing, not by database table. Labels stay stable: tests and muscle memory use them.
  const groups: { title: string; items: { tab: Tab; label: string; show: boolean }[] }[] = [
    { title: '', items: [{ tab: 'overview', label: 'Overview', show: true }] },
    {
      title: 'Records',
      items: [
        { tab: 'students', label: 'Students', show: true },
        { tab: 'courses', label: 'Courses', show: true },
        { tab: 'batches', label: 'Batches', show: true },
        { tab: 'import', label: 'Import students', show: can('students.import') },
      ],
    },
    {
      title: 'Certificates',
      items: [
        { tab: 'templates', label: 'Templates', show: can('templates.read') },
        { tab: 'certificates', label: 'Certificates', show: can('certificates.read') },
      ],
    },
    { title: 'Team', items: [{ tab: 'staff', label: 'Staff', show: can('staff.manage') }] },
  ];
  return (
    <section aria-labelledby="inst-name">
      <div className="ws-head">
        <h1 id="inst-name">{inst.name}</h1>
        <div className="meta">
          <Badge tone="info">{inst.role}</Badge>
          <span>Your role in this institute</span>
        </div>
      </div>
      {inst.status !== 'active' && (
        <Notice tone="warn">
          This institute is <strong>{inst.status}</strong>. Access is read-only: you can look at records, but not change or export them.
        </Notice>
      )}
      <div className="ws">
        <nav aria-label="Institute sections" className="sidenav">
          {groups.map((g) => {
            const items = g.items.filter((i) => i.show);
            if (items.length === 0) return null;
            return (
              <div key={g.title || 'main'} style={{ display: 'contents' }}>
                {g.title && <span className="group">{g.title}</span>}
                {items.map((i) => (
                  <button key={i.tab} type="button" aria-pressed={tab === i.tab} onClick={() => setTab(i.tab)}>
                    {i.label}
                  </button>
                ))}
              </div>
            );
          })}
        </nav>
        <div className="ws-body">
          {/* Write actions are shown only when the server granted the capability; the API enforces it regardless. */}
          {tab === 'overview' && <Overview go={(t) => setTab(t)} allowed={groups.flatMap((g) => g.items.filter((i) => i.show).map((i) => i.tab))} />}
          {tab === 'students' && <StudentsPage canWrite={can('students.write')} canExport={can('students.export')} />}
          {tab === 'courses' && <CoursesPage canWrite={can('courses.write')} />}
          {tab === 'batches' && <BatchesPage canWrite={can('courses.write')} />}
          {tab === 'templates' && can('templates.read') && <TemplatesPage canWrite={can('templates.write')} />}
          {tab === 'certificates' && can('certificates.read') && <CertificatesPage canIssue={can('certificates.issue')} canRevoke={can('certificates.revoke')} canExport={can('certificates.export')} canDeliver={can('certificates.deliver')} />}
          {tab === 'import' && can('students.import') && <ImportWizard />}
          {tab === 'staff' && can('staff.manage') && <StaffPage selfEmail={user?.email} />}
        </div>
      </div>
    </section>
  );
}

export function WorkspaceView() {
  const { state, retry } = useAsync(() => api<{ institutes: InstituteSummary[] }>('/api/v1/me/institutes'), []);
  const [selected, setSelected] = useState<string | null>(null);

  const institutes = state.status === 'success' ? state.data.institutes : [];
  useEffect(() => {
    if (institutes.length === 1) setSelected(institutes[0].id);
  }, [institutes]);

  return (
    <section>
      {!selected && <h1>Institute workspace</h1>}
      {state.status === 'loading' && <Loading label="Loading your institutes…" />}
      {state.status === 'error' && <ErrorState message={state.error.message} onRetry={retry} />}
      {state.status === 'success' && institutes.length === 0 && (
        <EmptyState title="No institute access" action={<Link to="/apply" className="button primary">Register an institute</Link>}>
          <p>Your account is not a member of any institute yet. Ask an institute owner to invite you, or register your own institute.</p>
          <p className="muted">Registration is reviewed by our team before your institute goes live.</p>
        </EmptyState>
      )}
      {institutes.length > 1 && (
        <div className="toolbar">
          <label htmlFor="institute">Institute</label>
          <select id="institute" value={selected ?? ''} onChange={(e) => setSelected(e.target.value || null)}>
            <option value="">Choose an institute…</option>
            {institutes.map((i) => (
              <option key={i.id} value={i.id}>
                {i.name}
              </option>
            ))}
          </select>
        </div>
      )}
      {selected && <Detail id={selected} />}
    </section>
  );
}
