import { NavLink, Route, Routes } from 'react-router-dom';
import { api, ApiError } from '../lib/api';
import { useAsync } from '../lib/useAsync';
import { EmptyState, ErrorState, Loading } from '../components/States';
import { AuthForm } from '../features/auth/AuthForm';
import { ApplyPage } from '../features/applications/ApplyPage';
import { RequirePermission, RequireSession } from '../features/auth/guards';
import { useSession } from '../features/auth/session';
import { ApplicationDetail, ApplicationQueue } from '../features/ops/OpsApplications';
import { OpsAudit } from '../features/ops/OpsAudit';
import { OpsInstitutes } from '../features/ops/OpsInstitutes';
import { WorkspaceView } from '../features/institute/Workspace';

export function Home() {
  const ready = useAsync(() => api<{ status: string }>('/readyz'), []);
  return (
    <section>
      <h1>CertificateOS</h1>
      <p>Secure certificate management and public verification for institutes.</p>
      <h2>API status</h2>
      {ready.state.status === 'loading' && <Loading label="Checking API…" />}
      {ready.state.status === 'success' && <p>API is ready.</p>}
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
      <h1>Operations</h1>
      <nav aria-label="Operations">
        {can('ops.applications.read') && <NavLink to="/ops" end className="nav-link">Applications</NavLink>}
        {can('ops.institutes.manage') && <NavLink to="/ops/institutes" className="nav-link">Institutes</NavLink>}
        {can('ops.audit.read') && <NavLink to="/ops/audit" className="nav-link">Audit log</NavLink>}
      </nav>
      {/* UX gating only: every endpoint enforces its own platform permission. */}
      <Routes>
        <Route index element={<RequirePermission permission="ops.applications.read"><ApplicationQueue /></RequirePermission>} />
        <Route path="applications/:id" element={<RequirePermission permission="ops.applications.read"><ApplicationDetail /></RequirePermission>} />
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
  <EmptyState title="Page not found">
    <p>The page you requested does not exist.</p>
  </EmptyState>
);
