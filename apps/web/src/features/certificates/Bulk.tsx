import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError } from '../../lib/api';
import { batchesApi, type Batch } from '../records/api';
import { templatesApi, type Template } from '../templates/api';
import { bulkApi, isActive, type BulkItem, type BulkJob, type BulkPreview, type JobStatus } from './api';

const STATUS_LABEL: Record<JobStatus, string> = {
  queued: 'Waiting to start', processing: 'Issuing…', completed: 'Completed', completed_with_errors: 'Completed with errors',
  failed: 'Failed', cancelled: 'Cancelled',
};

function message(e: unknown): string {
  return e instanceof ApiError || e instanceof Error ? e.message : 'Something went wrong.';
}

function Progress({ job }: { job: BulkJob }) {
  const done = job.succeeded + job.failed;
  return (
    <>
      <progress aria-label="Bulk issuing progress" max={job.total} value={done} />
      <p role="status">
        {STATUS_LABEL[job.status]} — {job.succeeded} of {job.total} issued
        {job.failed > 0 ? `, ${job.failed} failed` : ''}
        {job.remaining > 0 ? `, ${job.remaining} remaining` : ''}.
      </p>
    </>
  );
}

function JobView({ job, canExport, reload, onChanged }: { job: BulkJob; canExport: boolean; reload: () => void; onChanged: () => void }) {
  const [failed, setFailed] = useState<BulkItem[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (isActive(job.status) || job.failed === 0) {
      setFailed([]);
      return;
    }
    let cancelled = false;
    bulkApi.items(job.id, 'failed').then(
      (r) => !cancelled && setFailed(r.items),
      (e) => !cancelled && setError(message(e)),
    );
    return () => {
      cancelled = true;
    };
  }, [job.id, job.status, job.failed]);

  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      reload();
      onChanged();
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  const download = async () => {
    setBusy(true);
    setError(null);
    try {
      const blob = await bulkApi.exportZip(job.id);
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `certificates-${job.id.slice(0, 8).toLowerCase()}.zip`;
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

  const active = isActive(job.status);
  return (
    <div aria-label="Bulk job">
      <h5>{job.batch_name} — {job.template_name} (version {job.template_version})</h5>
      <Progress job={job} />
      {(job.skipped_already_issued > 0 || job.skipped_not_eligible > 0) && (
        <p>Left out: {job.skipped_already_issued} already had a certificate, {job.skipped_not_eligible} not completed or archived.</p>
      )}
      {active && (
        <p>
          <button type="button" disabled={busy} onClick={() => act(() => bulkApi.cancel(job.id))}>Cancel job</button>{' '}
          <button type="button" disabled={busy} onClick={() => act(() => bulkApi.resume(job.id))}>Resume job</button>
          <br /><small>Resume is only needed if progress has stopped, for example after a server restart.</small>
        </p>
      )}
      {!active && job.failed > 0 && (
        <>
          <table aria-label="Failed certificates">
            <thead><tr><th scope="col">Student</th><th scope="col">Why it failed</th></tr></thead>
            <tbody>{failed.map((i) => <tr key={i.enrollment_id}><td>{i.student_name}</td><td>{i.error}</td></tr>)}</tbody>
          </table>
          <button type="button" disabled={busy} onClick={() => act(() => bulkApi.retryFailed(job.id))}>Retry failed ({job.failed})</button>
        </>
      )}
      {!active && canExport && job.succeeded > 0 && (
        <p><button type="button" disabled={busy} onClick={download}>Download all as ZIP</button></p>
      )}
      {error && <p role="alert">{error}</p>}
    </div>
  );
}

/** Issue a whole batch: preview, start, watch progress, retry what failed, download the PDFs as a ZIP. */
export function BulkPanel({ canExport, onChanged, pollMs = 2000 }: { canExport: boolean; onChanged: () => void; pollMs?: number }) {
  const [open, setOpen] = useState(false);
  return (
    <section aria-labelledby="bulk-h">
      <h4 id="bulk-h">Issue a whole batch</h4>
      {!open ? (
        <button type="button" onClick={() => setOpen(true)}>Issue for a batch…</button>
      ) : (
        <BulkBody canExport={canExport} onChanged={onChanged} pollMs={pollMs} />
      )}
    </section>
  );
}

function BulkBody({ canExport, onChanged, pollMs }: { canExport: boolean; onChanged: () => void; pollMs: number }) {
  const [batches, setBatches] = useState<Batch[]>([]);
  const [templates, setTemplates] = useState<Template[]>([]);
  const [batchId, setBatchId] = useState('');
  const [templateId, setTemplateId] = useState('');
  const [preview, setPreview] = useState<BulkPreview | null>(null);
  const [job, setJob] = useState<BulkJob | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const wasActive = useRef(false);

  const reload = useCallback(async () => {
    try {
      const r = await bulkApi.list(1);
      setJob(r.jobs[0] ?? null);
    } catch (e) {
      setError(message(e));
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    Promise.all([batchesApi.list({ limit: 100 }), templatesApi.list(), bulkApi.list(1)]).then(
      ([b, t, j]) => {
        if (cancelled) return;
        setBatches(b.batches);
        setTemplates(t.templates);
        setJob(j.jobs[0] ?? null);
        setLoaded(true);
      },
      (e) => {
        if (!cancelled) {
          setError(message(e));
          setLoaded(true);
        }
      },
    );
    return () => {
      cancelled = true;
    };
  }, []);

  // Poll while a job is running; tell the parent (the certificates list) once it has finished.
  const active = job ? isActive(job.status) : false;
  useEffect(() => {
    if (!active) {
      if (wasActive.current) onChanged();
      wasActive.current = false;
      return;
    }
    wasActive.current = true;
    const t = setInterval(() => { void reload(); }, pollMs);
    return () => clearInterval(t);
  }, [active, pollMs, reload, onChanged]);

  const check = async () => {
    setBusy(true);
    setError(null);
    setPreview(null);
    try {
      setPreview((await bulkApi.preview(batchId, templateId)).preview);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  const start = async () => {
    setBusy(true);
    setError(null);
    try {
      setJob((await bulkApi.start(batchId, templateId)).job);
      setPreview(null);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };

  if (!loaded) return <p role="status">Loading…</p>;
  const tpl = templates.filter((t) => !t.archived_at);
  return (
    <div>
      {!active && (
        <form aria-label="Issue for a batch" onSubmit={(e) => { e.preventDefault(); void check(); }}>
          <p>
            <label htmlFor="bulk-batch">Batch</label><br />
            <select id="bulk-batch" value={batchId} onChange={(e) => { setBatchId(e.target.value); setPreview(null); }} required>
              <option value="">Choose…</option>
              {batches.filter((b) => !b.archived_at).map((b) => <option key={b.id} value={b.id}>{b.course_name} — {b.name}</option>)}
            </select>
          </p>
          <p>
            <label htmlFor="bulk-template">Template</label><br />
            <select id="bulk-template" value={templateId} onChange={(e) => { setTemplateId(e.target.value); setPreview(null); }} required>
              <option value="">Choose…</option>
              {tpl.map((t) => <option key={t.id} value={t.id}>{t.name} (version {t.current_version.number})</option>)}
            </select>
          </p>
          <button type="submit" disabled={busy || !batchId || !templateId}>Check what will be issued</button>
        </form>
      )}
      {preview && (
        <div role="region" aria-label="Preview">
          <p>
            <strong>{preview.eligible}</strong> certificate{preview.eligible === 1 ? '' : 's'} will be issued for {preview.batch_name}.
            {' '}{preview.already_issued} already {preview.already_issued === 1 ? 'has' : 'have'} one and {preview.not_eligible} {preview.not_eligible === 1 ? 'is' : 'are'} not completed or archived (left out).
          </p>
          {preview.eligible > preview.max_per_job && <p role="alert">A job can hold at most {preview.max_per_job} certificates. Issue this batch in smaller groups.</p>}
          <button type="button" disabled={busy || preview.eligible === 0 || preview.eligible > preview.max_per_job} onClick={start}>
            Issue {preview.eligible} certificate{preview.eligible === 1 ? '' : 's'}
          </button>
        </div>
      )}
      {job && <JobView job={job} canExport={canExport} reload={reload} onChanged={onChanged} />}
      {error && <p role="alert">{error}</p>}
    </div>
  );
}
