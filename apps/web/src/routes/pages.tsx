import { Link, NavLink, Route, Routes } from 'react-router-dom';
import { api, ApiError } from '../lib/api';
import { useAsync } from '../lib/useAsync';
import { Badge } from '../components/Badge';
import { EmptyState, ErrorState, Loading } from '../components/States';
import { AuthForm } from '../features/auth/AuthForm';
import { ApplyPage } from '../features/applications/ApplyPage';
import { RequirePermission, RequireSession } from '../features/auth/guards';
import { useSession } from '../features/auth/session';
import { ApplicationDetail, ApplicationQueue } from '../features/ops/OpsApplications';
import { OpsAudit } from '../features/ops/OpsAudit';
import { OpsDashboard } from '../features/ops/OpsDashboard';
import { OpsInstitutes } from '../features/ops/OpsInstitutes';
import { WorkspaceView } from '../features/institute/Workspace';

export function Home() {
  const ready = useAsync(() => api<{ status: string }>('/readyz'), []);
  const { status, user } = useSession();
  return (
    <section>
      <div className="hero">
        <h1>Certificates your students can prove</h1>
        <p>
          CertificateOS helps training institutes issue, send and verify certificates. Every certificate carries a QR code:
          anyone who scans it sees at once whether the certificate is genuine.
        </p>
        <div className="actions">
          {user ? (
            <Link to="/app" className="button primary">Open your workspace</Link>
          ) : status === 'anonymous' ? (
            <>
              <Link to="/login" className="button primary">Log in</Link>
              <Link to="/register" className="button">Create an account</Link>
            </>
          ) : null}
        </div>
      </div>
      <div className="sysline">
        {ready.state.status === 'loading' && <Loading label="Checking API…" />}
        {ready.state.status === 'success' && <><Badge tone="ok">System ready</Badge><p>API is ready.</p></>}
      </div>
      {ready.state.status === 'error' && (
        <ErrorState
          message={ready.state.error.message}
          requestId={ready.state.error instanceof ApiError ? ready.state.error.requestId : undefined}
          onRetry={ready.retry}
        />
      )}
    </section>
  );
}

// Placeholders: real behavior arrives with COS-007 (auth) and later milestones.
export const Login = () => <AuthForm mode="login" />;
export const Register = () => <AuthForm mode="register" />;
export const Workspace = () => (
  <RequireSession>
    <WorkspaceView />
  </RequireSession>
);
export const Apply = () => (
  <RequireSession>
    <ApplyPage />
  </RequireSession>
);

function OperationsHome() {
  const { user } = useSession();
  const can = (p: string) => user?.permissions.includes(p) ?? false;
  return (
    <section>
      <div className="page-head">
        <div>
          <h1>Operations</h1>
          <p>Review institute applications and keep an eye on the platform.</p>
        </div>
      </div>
      <nav aria-label="Operations">
        {can('ops.applications.read') && <NavLink to="/ops" end className="nav-link">Applications</NavLink>}
        {can('ops.applications.read') && <NavLink to="/ops/overview" className="nav-link">Overview</NavLink>}
        {can('ops.institutes.manage') && <NavLink to="/ops/institutes" className="nav-link">Institutes</NavLink>}
        {can('ops.audit.read') && <NavLink to="/ops/audit" className="nav-link">Audit log</NavLink>}
      </nav>
      {/* UX gating only: every endpoint enforces its own platform permission. */}
      <Routes>
        <Route index element={<RequirePermission permission="ops.applications.read"><ApplicationQueue /></RequirePermission>} />
        <Route path="applications/:id" element={<RequirePermission permission="ops.applications.read"><ApplicationDetail /></RequirePermission>} />
        <Route path="overview" element={<RequirePermission permission="ops.applications.read"><OpsDashboard /></RequirePermission>} />
        <Route path="institutes" element={<RequirePermission permission="ops.institutes.manage"><OpsInstitutes /></RequirePermission>} />
        <Route path="audit" element={<RequirePermission permission="ops.audit.read"><OpsAudit /></RequirePermission>} />
        <Route path="*" element={<NotFoundPage />} />
      </Routes>
    </section>
  );
}

export const Operations = () => (
  <RequireSession>
    <OperationsHome />
  </RequireSession>
);
export const NotFoundPage = () => (
  <EmptyState title="Page not found" action={<Link to="/" className="button">Go to the home page</Link>}>
    <p>The page you requested does not exist. Check the address, or go back to the home page.</p>
  </EmptyState>
);
