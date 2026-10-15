import type { ReactNode } from 'react';

export function Loading({ label = 'Loading…' }: { label?: string }) {
  return (
    <p role="status" aria-live="polite">
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
        <div>
          <button type="button" onClick={onRetry}>
            Try again
          </button>
        </div>
      )}
    </div>
  );
}

export function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="state state-empty">
      <h2>{title}</h2>
      {children}
    </div>
  );
}
