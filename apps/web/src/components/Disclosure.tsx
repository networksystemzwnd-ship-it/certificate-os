import type { ReactNode } from 'react';

/** A create form that stays out of the way until it is wanted. Native <details>: keyboard and screen-reader friendly. */
export function Disclosure({ title, open, children }: { title: string; open?: boolean; children: ReactNode }) {
  return (
    <details className="disclosure" open={open}>
      <summary>{title}</summary>
      <div className="disclosure-body">{children}</div>
    </details>
  );
}
