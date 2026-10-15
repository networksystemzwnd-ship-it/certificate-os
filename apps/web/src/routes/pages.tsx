import { api, ApiError } from '../lib/api';
import { useAsync } from '../lib/useAsync';
import { EmptyState, ErrorState, Loading } from '../components/States';
import { AuthForm } from '../features/auth/AuthForm';
import { RequirePermission, RequireSession } from '../features/auth/guards';

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
    <EmptyState title="Institute workspace">
      <p>Institute workspaces arrive with tenancy (COS-008).</p>
    </EmptyState>
  </RequireSession>
);
export const Operations = () => (
  <RequireSession>
    <RequirePermission permission="ops.applications.read">
      <EmptyState title="Operations">
        <p>Application review arrives with COS-009/010.</p>
      </EmptyState>
    </RequirePermission>
  </RequireSession>
);
export const NotFoundPage = () => (
  <EmptyState title="Page not found">
    <p>The page you requested does not exist.</p>
  </EmptyState>
);
