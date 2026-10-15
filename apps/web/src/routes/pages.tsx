import { api, ApiError } from '../lib/api';
import { useAsync } from '../lib/useAsync';
import { EmptyState, ErrorState, Loading } from '../components/States';

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
export const Login = () => (
  <EmptyState title="Log in">
    <p>Authentication arrives with COS-007.</p>
  </EmptyState>
);
export const Workspace = () => (
  <EmptyState title="Institute workspace">
    <p>Available after authentication and tenancy are implemented (COS-007/008).</p>
  </EmptyState>
);
export const Operations = () => (
  <EmptyState title="Operations">
    <p>Application review arrives with COS-009/010.</p>
  </EmptyState>
);
export const NotFoundPage = () => (
  <EmptyState title="Page not found">
    <p>The page you requested does not exist.</p>
  </EmptyState>
);
