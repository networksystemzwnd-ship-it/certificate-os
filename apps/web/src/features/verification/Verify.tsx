import { Link, useParams } from 'react-router-dom';
import { ApiError, api } from '../../lib/api';
import { useAsync } from '../../lib/useAsync';

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
      <p><strong>CertificateOS</strong> · Certificate verification</p>
      {state.status === 'loading' && <p role="status">Checking this certificate…</p>}
      {state.status === 'error' && state.error instanceof ApiError && state.error.status === 404 && (
        <>
          <h1 id="verify-h">Certificate not found</h1>
          <p role="alert">No certificate matches this link. Check that the link or QR code was copied completely, or ask the institute that issued it.</p>
        </>
      )}
      {state.status === 'error' && state.error instanceof ApiError && state.error.status === 429 && (
        <>
          <h1 id="verify-h">Please wait a moment</h1>
          <p role="alert">Too many checks were made from your connection. Try again in a minute.</p>
        </>
      )}
      {state.status === 'error' && !(state.error instanceof ApiError && (state.error.status === 404 || state.error.status === 429)) && (
        <>
          <h1 id="verify-h">We could not check this certificate</h1>
          <p role="alert">{state.error.message}</p>
          <button type="button" onClick={retry}>Try again</button>
        </>
      )}
      {state.status === 'success' && <Result v={state.data.verification} />}
    </main>
  );
}

function Result({ v }: { v: Verification }) {
  return (
    <>
      <h1 id="verify-h" data-status={v.status}>{HEADLINE[v.status]}</h1>
      {v.status === 'revoked' && (
        <p role="alert">The issuing institute withdrew this certificate{v.revoked_at ? ` on ${longDate(v.revoked_at)}` : ''}. It should not be relied on.</p>
      )}
      {v.status === 'replaced' && (
        <p role="alert">
          The issuing institute replaced it{v.revoked_at ? ` on ${longDate(v.revoked_at)}` : ''} with a corrected certificate.{' '}
          {v.replaced_by && <Link to={`/verify/${v.replaced_by}`}>Check the current certificate</Link>}
        </p>
      )}
      <dl>
        <dt>Certificate number</dt><dd>{v.certificate_number}</dd>
        <dt>Awarded to</dt><dd>{v.student_name}</dd>
        <dt>Course</dt><dd>{v.course_name}</dd>
        <dt>Issued by</dt><dd>{v.institute_name}</dd>
        <dt>Issue date</dt><dd>{longDate(v.issue_date)}</dd>
      </dl>
      <p><small>Compare these details with the certificate you were given. This page never shows contact details or grades.</small></p>
    </>
  );
}
