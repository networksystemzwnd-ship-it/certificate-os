import { Link, useParams } from 'react-router-dom';
import { ApiError, api } from '../../lib/api';
import { useAsync } from '../../lib/useAsync';
import { SealMark } from '../../components/Brand';

export interface Verification {
  status: 'valid' | 'revoked' | 'replaced';
  certificate_number: string; institute_name: string; student_name: string; course_name: string; issue_date: string;
  revoked_at?: string; replaced_by?: string;
}

const longDate = (iso: string) => {
  const d = new Date(`${iso.slice(0, 10)}T00:00:00Z`);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString('en-GB', { day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC' });
};

const HEADLINE: Record<Verification['status'], string> = {
  valid: 'This certificate is valid',
  revoked: 'This certificate has been revoked',
  replaced: 'This certificate has been replaced',
};

/** Public page behind the QR code: no account, no workspace navigation, only the approved fields. */
export function VerifyPage() {
  const { publicId = '' } = useParams();
  const { state, retry } = useAsync(() => api<{ verification: Verification }>(`/api/v1/public/certificates/${encodeURIComponent(publicId)}`), [publicId]);

  return (
    <main id="main" className="verify" aria-labelledby="verify-h">
      <p className="brand"><SealMark /><span><strong>CertificateOS</strong> <span className="muted">Certificate verification</span></span></p>
      {state.status === 'loading' && <p role="status" className="loading"><span className="spinner" aria-hidden="true" />Checking this certificate…</p>}
      {state.status === 'error' && state.error instanceof ApiError && state.error.status === 404 && (
        <div className="verify-card" data-status="revoked">
          <header>
            <span className="mark" aria-hidden="true">?</span>
            <div>
              <h1 id="verify-h">Certificate not found</h1>
              <p role="alert">No certificate matches this link. Check that the link or QR code was copied completely, or ask the institute that issued it.</p>
            </div>
          </header>
        </div>
      )}
      {state.status === 'error' && state.error instanceof ApiError && state.error.status === 429 && (
        <div className="verify-card" data-status="replaced">
          <header>
            <span className="mark" aria-hidden="true">…</span>
            <div>
              <h1 id="verify-h">Please wait a moment</h1>
              <p role="alert">Too many checks were made from your connection. Try again in a minute.</p>
            </div>
          </header>
        </div>
      )}
      {state.status === 'error' && !(state.error instanceof ApiError && (state.error.status === 404 || state.error.status === 429)) && (
        <div className="verify-card" data-status="replaced">
          <header>
            <span className="mark" aria-hidden="true">!</span>
            <div>
              <h1 id="verify-h">We could not check this certificate</h1>
              <p role="alert">{state.error.message}</p>
              <button type="button" onClick={retry}>Try again</button>
            </div>
          </header>
        </div>
      )}
      {state.status === 'success' && <Result v={state.data.verification} />}
    </main>
  );
}

const MARK: Record<Verification['status'], string> = { valid: '✓', revoked: '✕', replaced: '↻' };

function Result({ v }: { v: Verification }) {
  return (
    <div className="verify-card" data-status={v.status}>
      <header>
        <span className="mark" aria-hidden="true">{MARK[v.status]}</span>
        <div>
          <h1 id="verify-h" data-status={v.status}>{HEADLINE[v.status]}</h1>
          <p>{v.status === 'valid' ? 'It has not been revoked or replaced.' : 'Read the notice below before relying on it.'}</p>
        </div>
      </header>
      {v.status === 'revoked' && (
        <p role="alert" className="notice bad">The issuing institute withdrew this certificate{v.revoked_at ? ` on ${longDate(v.revoked_at)}` : ''}. It should not be relied on.</p>
      )}
      {v.status === 'replaced' && (
        <p role="alert" className="notice warn">
          The issuing institute replaced it{v.revoked_at ? ` on ${longDate(v.revoked_at)}` : ''} with a corrected certificate.{' '}
          {v.replaced_by && <Link to={`/verify/${v.replaced_by}`}>Check the current certificate</Link>}
        </p>
      )}
      <dl className="facts">
        <dt>Awarded to</dt><dd className="big">{v.student_name}</dd>
        <dt>Course</dt><dd>{v.course_name}</dd>
        <dt>Issued by</dt><dd>{v.institute_name}</dd>
        <dt>Issue date</dt><dd>{longDate(v.issue_date)}</dd>
        <dt>Certificate number</dt><dd className="mono">{v.certificate_number}</dd>
      </dl>
      <footer>
        <p>Compare these details with the certificate you were given. This page never shows contact details or grades.</p>
      </footer>
    </div>
  );
}
