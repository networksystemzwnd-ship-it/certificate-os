import { useState, type FormEvent } from 'react';
import { ApiError } from '../../lib/api';
import { useAsync } from '../../lib/useAsync';
import { EmptyState, ErrorState, Loading } from '../../components/States';
import { studentsApi, type Student } from '../records/api';
import { templatesApi } from '../templates/api';
import { certificatesApi, type Certificate } from './api';

const STATUS_LABEL: Record<Certificate['status'], string> = {
  issued: 'Issued', revoked: 'Revoked', failed: 'Failed', pending: 'In progress',
};

function message(e: unknown): string {
  return e instanceof ApiError || e instanceof Error ? e.message : 'Something went wrong.';
}

function IssuePanel({ onIssued }: { onIssued: (c: Certificate) => void }) {
  const [term, setTerm] = useState('');
  const [students, setStudents] = useState<Student[] | null>(null);
  const [student, setStudent] = useState<Student | null>(null);
  const [enrollmentId, setEnrollmentId] = useState('');
  const [templateId, setTemplateId] = useState('');
  const templates = useAsync(() => templatesApi.list(), []);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const search = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    try {
      setStudents((await studentsApi.list({ q: term, limit: 10 })).students);
    } catch (err) {
      setError(message(err));
    }
  };
  const pick = async (id: string) => {
    setError(null);
    try {
      const s = (await studentsApi.get(id)).student;
      setStudent(s);
      setEnrollmentId(s.enrollments?.find((x) => x.status === 'completed')?.id ?? '');
    } catch (err) {
      setError(message(err));
    }
  };
  const issue = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      onIssued((await certificatesApi.issue(enrollmentId, templateId)).certificate);
      setStudent(null);
      setStudents(null);
      setTerm('');
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  };

  const completed = student?.enrollments?.filter((x) => x.status === 'completed') ?? [];
  const tpl = templates.state.status === 'success' ? templates.state.data.templates : [];
  return (
    <section aria-labelledby="issue-h">
      <h4 id="issue-h">Issue a certificate</h4>
      <form onSubmit={search} aria-label="Find student">
        <label htmlFor="cert-student-q">Find a student</label>{' '}
        <input id="cert-student-q" value={term} onChange={(e) => setTerm(e.target.value)} placeholder="Name, number or email" />{' '}
        <button type="submit">Search</button>
      </form>
      {students && students.length === 0 && <p>No students found.</p>}
      {students && students.length > 0 && !student && (
        <ul>
          {students.map((s) => (
            <li key={s.id}>{s.full_name} <button type="button" onClick={() => pick(s.id)}>Select {s.full_name}</button></li>
          ))}
        </ul>
      )}
      {student && (
        <form onSubmit={issue} aria-label="Issue certificate">
          <p>Student: <strong>{student.full_name}</strong> <button type="button" onClick={() => setStudent(null)}>Change</button></p>
          {completed.length === 0 ? (
            <p role="status">This student has no completed course yet. Mark an enrollment as completed first.</p>
          ) : (
            <>
              <p>
                <label htmlFor="cert-enrollment">Completed course</label><br />
                <select id="cert-enrollment" value={enrollmentId} onChange={(e) => setEnrollmentId(e.target.value)} required>
                  <option value="">Choose…</option>
                  {completed.map((x) => (
                    <option key={x.id} value={x.id}>{x.course_name} — {x.batch_name} ({x.completion_date})</option>
                  ))}
                </select>
              </p>
              <p>
                <label htmlFor="cert-template">Template</label><br />
                <select id="cert-template" value={templateId} onChange={(e) => setTemplateId(e.target.value)} required>
                  <option value="">Choose…</option>
                  {tpl.map((t) => (
                    <option key={t.id} value={t.id}>{t.name} (version {t.current_version.number})</option>
                  ))}
                </select>
              </p>
              <button type="submit" disabled={busy || !enrollmentId || !templateId}>{busy ? 'Creating the PDF…' : 'Issue certificate'}</button>
            </>
          )}
        </form>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}

function Row({ cert, canRevoke, onChanged }: { cert: Certificate; canRevoke: boolean; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const download = async () => {
    setBusy(true);
    setError(null);
    try {
      const blob = await certificatesApi.pdf(cert.id);
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `${cert.certificate_number}.pdf`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  const revoke = async () => {
    const reason = window.prompt(`Why is ${cert.certificate_number} being revoked? (shown to your team, 3–500 characters)`);
    if (reason === null) return;
    setBusy(true);
    setError(null);
    try {
      await certificatesApi.revoke(cert.id, reason);
      onChanged();
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  const hasPdf = cert.status === 'issued' || cert.status === 'revoked';
  return (
    <tr>
      <td>{cert.certificate_number}</td>
      <td>{cert.student_name}</td>
      <td>{cert.course_name}</td>
      <td>{STATUS_LABEL[cert.status]}{cert.status === 'revoked' && cert.revoke_reason ? ` — ${cert.revoke_reason}` : ''}{cert.status === 'failed' ? ' — not issued' : ''}</td>
      <td>
        {hasPdf && <button type="button" disabled={busy} onClick={download}>Download {cert.certificate_number}</button>}{' '}
        {canRevoke && cert.status === 'issued' && <button type="button" disabled={busy} onClick={revoke}>Revoke {cert.certificate_number}</button>}
        {error && <span role="alert"> {error}</span>}
      </td>
    </tr>
  );
}

export function CertificatesPage({ canIssue, canRevoke }: { canIssue: boolean; canRevoke: boolean }) {
  const [status, setStatus] = useState('');
  const [q, setQ] = useState('');
  const [applied, setApplied] = useState('');
  const list = useAsync(() => certificatesApi.list({ status, q: applied }), [status, applied]);
  const [notice, setNotice] = useState<string | null>(null);

  return (
    <section aria-labelledby="certs-h">
      <h3 id="certs-h">Certificates</h3>
      {canIssue && <IssuePanel onIssued={(c) => { setNotice(`Issued ${c.certificate_number} for ${c.student_name}.`); list.retry(); }} />}
      {notice && <p role="status">{notice}</p>}
      <form onSubmit={(e) => { e.preventDefault(); setApplied(q); }} aria-label="Filter certificates">
        <label htmlFor="cert-q">Search</label>{' '}
        <input id="cert-q" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Number or student name" />{' '}
        <label htmlFor="cert-status">Status</label>{' '}
        <select id="cert-status" value={status} onChange={(e) => setStatus(e.target.value)}>
          <option value="">All</option><option value="issued">Issued</option><option value="revoked">Revoked</option><option value="failed">Failed</option>
        </select>{' '}
        <button type="submit">Search</button>
      </form>
      {list.state.status === 'loading' && <Loading />}
      {list.state.status === 'error' && <ErrorState message={list.state.error.message} onRetry={list.retry} />}
      {list.state.status === 'success' &&
        (list.state.data.certificates.length === 0 ? (
          <EmptyState title="No certificates yet">{canIssue ? 'Issue the first one above.' : 'An admin can issue certificates.'}</EmptyState>
        ) : (
          <table aria-label="Certificates">
            <thead><tr><th scope="col">Number</th><th scope="col">Student</th><th scope="col">Course</th><th scope="col">Status</th><th scope="col">Actions</th></tr></thead>
            <tbody>
              {list.state.data.certificates.map((c) => (
                <Row key={c.id} cert={c} canRevoke={canRevoke} onChanged={list.retry} />
              ))}
            </tbody>
          </table>
        ))}
    </section>
  );
}
