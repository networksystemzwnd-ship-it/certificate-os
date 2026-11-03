import { Link } from 'react-router-dom';

/** The seal: a perforated-edge stamp with a check. Decorative, so hidden from assistive technology. */
export function SealMark({ size = 26 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 28 28" aria-hidden="true" focusable="false">
      <circle cx="14" cy="14" r="13" fill="var(--seal)" />
      <circle cx="14" cy="14" r="10" fill="none" stroke="var(--seal-ink)" strokeOpacity=".55" strokeWidth="1" strokeDasharray="1.6 1.6" />
      <path d="M9 14.4l3.4 3.4 6.8-7" fill="none" stroke="var(--seal-ink)" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function Brand({ to = '/' }: { to?: string }) {
  return (
    <Link to={to} className="brand">
      <SealMark />
      <span>CertificateOS</span>
    </Link>
  );
}
