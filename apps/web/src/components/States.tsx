import type { ReactNode } from 'react';

export function Loading({ label = 'Loading…' }: { label?: string }) {
  return (
    <p role="status" aria-live="polite" className="loading">
      <span className="spinner" aria-hidden="true" />
      {label}
    </p>
  );
}

export function ErrorState({ message, requestId, onRetry }: { message: string; requestId?: string; onRetry?: () => void }) {
  return (
    <div role="alert" className="state state-error">
      <strong>Error:</strong> {message}
      {requestId && <small> (reference: {requestId})</small>}
      {onRetry && (
        <div className="actions" style={{ marginTop: '.6rem' }}>
          <button type="button" onClick={onRetry}>
            Try again
          </button>
        </div>
      )}
    </div>
  );
}

/** An empty screen is an invitation: say what belongs here and what to do first. */
export function EmptyState({ title, children, action }: { title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="state state-empty">
      <h2>{title}</h2>
      {children}
      {action && <div className="actions">{action}</div>}
    </div>
  );
}

/** A quiet note (not an error): information the person should not miss. */
export function Notice({ tone = 'info', children }: { tone?: 'info' | 'warn' | 'bad'; children: ReactNode }) {
  return (
    <p role="status" className={tone === 'info' ? 'notice' : `notice ${tone}`}>
      {children}
    </p>
  );
}
