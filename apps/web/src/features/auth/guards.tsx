import type { ReactNode } from 'react';
import { Navigate } from 'react-router-dom';
import { EmptyState, Loading } from '../../components/States';
import { useSession } from './session';

/** UX gate only; the API enforces authentication and authorization on every request. */
export function RequireSession({ children }: { children: ReactNode }) {
  const { status } = useSession();
  if (status === 'loading') return <Loading label="Checking your session…" />;
  if (status === 'anonymous') return <Navigate to="/login" replace />;
  return <>{children}</>;
}

export function RequirePermission({ permission, children }: { permission: string; children: ReactNode }) {
  const { user } = useSession();
  if (!user?.permissions.includes(permission)) {
    return (
      <EmptyState title="Not permitted">
        <p>You do not have access to this area.</p>
      </EmptyState>
    );
  }
  return <>{children}</>;
}
