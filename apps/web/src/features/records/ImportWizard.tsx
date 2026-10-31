import { useState, type ChangeEvent } from 'react';
import { ApiError } from '../../lib/api';
import { fieldErrors } from '../auth/session';
import { importsApi, type ImportJob } from './api';

// Student import wizard: upload a CSV → map columns → review row-level problems → confirm.
// Nothing is saved to the student list until the user confirms; the server validates everything again.

const FIELDS: { key: string; label: string; required?: boolean; hint?: string }[] = [
  { key: 'full_name', label: 'Full name', required: true },
  { key: 'student_number', label: 'Student number', hint: 'Used to detect duplicates' },
  { key: 'email', label: 'Email' },
  { key: 'phone', label: 'Phone' },
  { key: 'course', label: 'Course', hint: 'Only needed when batch names repeat across courses' },
  { key: 'batch', label: 'Batch', hint: 'Must already exist; enrols the student' },
  { key: 'status', label: 'Course status', hint: 'enrolled, completed or withdrawn' },
  { key: 'completion_date', label: 'Completion date' },
  { key: 'grade', label: 'Grade / result' },
];

function download(name: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/csv;charset=utf-8' }));
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}

export function ImportWizard() {
  const [job, setJob] = useState<ImportJob | null>(null);
  const [mapping, setMapping] = useState<Record<string, string>>({});
  const [dateFormat, setDateFormat] = useState<'iso' | 'dmy' | 'mdy'>('iso');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const fe = fieldErrors(error);
  async function run(fn: () => Promise<void>) {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(e instanceof Error ? e : new Error('Something went wrong.'));
    } finally {
      setBusy(false);
    }
  }

  const load = (j: ImportJob) => {
    setJob(j);
    const m = j.status === 'uploaded' ? (j.suggested_mapping ?? j.mapping) : j.mapping;
    setMapping(Object.fromEntries(Object.entries(m).map(([k, v]) => [k, String(v)])));
    setDateFormat(j.date_format);
  };

  const onFile = (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    run(async () => load((await importsApi.upload(file)).import));
  };

  const check = () =>
    run(async () => {
      const m = Object.fromEntries(Object.entries(mapping).filter(([, v]) => v !== '').map(([k, v]) => [k, Number(v)]));
      load((await importsApi.setMapping(job!.id, m, dateFormat)).import);
    });

  const confirm = () => run(async () => setJob((await importsApi.confirm(job!.id)).import));
  const cancel = () =>
    run(async () => {
      await importsApi.cancel(job!.id);
      setJob(null);
    });
  const report = () => run(async () => download('import-errors.csv', await importsApi.errorReport(job!.id)));

  const problem = error && !(error instanceof ApiError && error.status === 422 && Object.keys(fe).length > 0) ? error : null;
  const uploadErr = fe.file;

  return (
    <section aria-labelledby="import-h">
      <h3 id="import-h">Import students</h3>
      {problem && (
        <p role="alert">
          {problem.message}
          {problem instanceof ApiError && problem.requestId ? ` (reference: ${problem.requestId})` : ''}
        </p>
      )}

      {!job && (
        <div>
          <p>
            Upload a CSV or Excel (.xlsx) file; only the first sheet is read. The first row must be the column headings. Up to 2000 rows
            and 2 MB. Phone numbers and ids that start with 0 should be formatted as text in Excel. Nothing is saved until you confirm.
          </p>
          <label htmlFor="import-file">CSV or Excel file</label>
          <br />
          <input id="import-file" type="file" accept=".csv,.xlsx,text/csv,text/plain,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" onChange={onFile} disabled={busy} />
          {uploadErr && <p role="alert">{uploadErr}</p>}
        </div>
      )}

      {job && (job.status === 'uploaded' || job.status === 'validated') && (
        <div>
          <p>
            File: <strong>{job.filename || 'upload'}</strong> · {job.counts.total || job.preview.length} rows previewed below
          </p>
          <h4>1. Match your columns</h4>
          <table>
            <thead>
              <tr>
                <th scope="col">Student field</th>
                <th scope="col">Column in your file</th>
              </tr>
            </thead>
            <tbody>
              {FIELDS.map((f) => (
                <tr key={f.key}>
                  <th scope="row">
                    <label htmlFor={`map-${f.key}`}>
                      {f.label}
                      {f.required ? ' *' : ''}
                    </label>
                    {f.hint && <small> — {f.hint}</small>}
                  </th>
                  <td>
                    <select id={`map-${f.key}`} value={mapping[f.key] ?? ''} onChange={(e) => setMapping({ ...mapping, [f.key]: e.target.value })}>
                      <option value="">— not in my file —</option>
                      {job.headers.map((h, i) => (
                        <option key={i} value={String(i)}>
                          {h}
                        </option>
                      ))}
                    </select>
                    {(fe[f.key] || (f.key === 'full_name' && fe.mapping)) && <span role="alert"> {fe[f.key] ?? fe.mapping}</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <p>
            <label htmlFor="date-format">Dates in your file are written as</label>{' '}
            <select id="date-format" value={dateFormat} onChange={(e) => setDateFormat(e.target.value as 'iso' | 'dmy' | 'mdy')}>
              <option value="iso">2026-03-31 (year-month-day)</option>
              <option value="dmy">31/03/2026 (day/month/year)</option>
              <option value="mdy">03/31/2026 (month/day/year)</option>
            </select>
            {fe.date_format && <span role="alert"> {fe.date_format}</span>}
          </p>
          <button type="button" onClick={check} disabled={busy}>
            {job.status === 'validated' ? 'Check again' : 'Check rows'}
          </button>{' '}
          <button type="button" onClick={cancel} disabled={busy}>
            Cancel import
          </button>

          {job.status === 'validated' && (
            <div>
              <h4>2. Review</h4>
              <p role="status">
                <strong>{job.counts.valid}</strong> ready to import · <strong>{job.counts.errors}</strong> with problems ·{' '}
                <strong>{job.counts.duplicates}</strong> duplicates (already exist, will be skipped, never overwritten)
              </p>
              {job.issues.length > 0 && (
                <table aria-label="Rows that need attention">
                  <thead>
                    <tr>
                      <th scope="col">Row</th>
                      <th scope="col">Result</th>
                      <th scope="col">What to fix</th>
                    </tr>
                  </thead>
                  <tbody>
                    {job.issues.map((i) => (
                      <tr key={i.row}>
                        <td>{i.row}</td>
                        <td>{i.state}</td>
                        <td>{i.message}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              {job.issues_truncated && <p>Only the first 100 problems are shown. The error report lists them all after you import.</p>}
              <h4>3. Import</h4>
              <p>Rows with problems are skipped. Fix them in your spreadsheet and upload only those rows again.</p>
              <button type="button" onClick={confirm} disabled={busy || job.counts.valid === 0}>
                Import {job.counts.valid} {job.counts.valid === 1 ? 'student' : 'students'}
              </button>
            </div>
          )}
        </div>
      )}

      {job && job.status === 'completed' && (
        <div>
          <p role="status">
            Import finished: <strong>{job.counts.imported}</strong> imported · <strong>{job.counts.errors}</strong> with problems ·{' '}
            <strong>{job.counts.duplicates}</strong> duplicates skipped
            {job.counts.failed > 0 ? ` · ${job.counts.failed} could not be saved` : ''}.
          </p>
          {job.counts.errors + job.counts.duplicates + job.counts.failed > 0 && (
            <p>
              <button type="button" onClick={report} disabled={busy}>
                Download error report (CSV)
              </button>
            </p>
          )}
          <button type="button" onClick={() => setJob(null)}>
            Start another import
          </button>
        </div>
      )}

      {job && job.status === 'confirming' && (
        <p role="status">
          This import is already running or was interrupted. <button type="button" onClick={confirm} disabled={busy}>Resume</button>
        </p>
      )}
    </section>
  );
}
