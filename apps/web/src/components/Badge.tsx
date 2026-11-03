import type { ReactNode } from 'react';

export type Tone = 'ok' | 'warn' | 'bad' | 'info' | 'muted';

/** A small status stamp. The words carry the meaning; colour only reinforces it. */
export function Badge({ tone = 'muted', children }: { tone?: Tone; children: ReactNode }) {
  return <span className={tone === 'muted' ? 'badge' : `badge ${tone}`}>{children}</span>;
}

/** Certificate status in plain words. */
export function CertStatus({ status }: { status: string }) {
  switch (status) {
    case 'issued':
      return <Badge tone="ok">Issued</Badge>;
    case 'revoked':
      return <Badge tone="bad">Revoked</Badge>;
    case 'failed':
      return <Badge tone="bad">Failed</Badge>;
    case 'pending':
      return <Badge tone="warn">Being created</Badge>;
    default:
      return <Badge>{status}</Badge>;
  }
}
