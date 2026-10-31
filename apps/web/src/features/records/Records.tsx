import { useState, type FormEvent, type ReactNode } from 'react';
import { ApiError } from '../../lib/api';
import { useAsync } from '../../lib/useAsync';
import { EmptyState, ErrorState, Loading } from '../../components/States';
import { fieldErrors } from '../auth/session';
import { batchesApi, coursesApi, studentsApi, type Batch, type Course, type Enrollment, type Student } from './api';

// All data here is tenant-scoped by the server (X-Institute-ID is verified against membership).
// The UI only hides actions the user cannot perform; the API enforces permissions regardless.

function useMutation() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  async function run(fn: () => Promise<unknown>, done?: () => void) {
    setBusy(true);
    setError(null);
    try {
      await fn();
      done?.();
    } catch (e) {
      setError(e instanceof Error ? e : new Error('Something went wrong.'));
    } finally {
      setBusy(false);
    }
  }
  return { busy, error, run, fe: fieldErrors(error), clear: () => setError(null) };
}

function Field({ id, label, error, children }: { id: string; label: string; error?: string; children: ReactNode }) {
  return (
    <p>
      <label htmlFor={id}>{label}</label>
      <br />
      {children}
      {error && <span id={`${id}-err`} role="alert"> {error}</span>}
    </p>
  );
}

function FormError({ error }: { error: Error | null }) {
  if (!error || (error instanceof ApiError && error.status === 422)) return null;
  return <p role="alert">{error.message}{error instanceof ApiError && error.requestId ? ` (reference: ${error.requestId})` : ''}</p>;
}

function Listing<T>({ state, retry, empty, children }: {
  state: ReturnType<typeof useAsync<T[]>>['state']; retry: () => void; empty: string; children: (items: T[]) => ReactNode;
}) {
  if (state.status === 'loading') return <Loading />;
  if (state.status === 'error') return <ErrorState message={state.error.message} requestId={state.error instanceof ApiError ? state.error.requestId : undefined} onRetry={retry} />;
  if (state.data.length === 0) return <EmptyState title={empty} />;
  return <>{children(state.data)}</>;
}

function ArchiveFilter({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <label>
      <input type="checkbox" style={{ minWidth: 0 }} checked={value === 'only'} onChange={(e) => onChange(e.target.checked ? 'only' : '')} /> Show archived
    </label>
  );
}

// ---------- Courses ----------

function CourseForm({ initial, onSaved, onCancel }: { initial?: Course; onSaved: () => void; onCancel?: () => void }) {
  const [name, setName] = useState(initial?.name ?? '');
  const [code, setCode] = useState(initial?.code ?? '');
  const [description, setDescription] = useState(initial?.description ?? '');
  const m = useMutation();
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const body = { name, code, description };
    m.run(() => (initial ? coursesApi.update(initial.id, body) : coursesApi.create(body)), () => {
      if (!initial) { setName(''); setCode(''); setDescription(''); }
      onSaved();
    });
  };
  return (
    <form onSubmit={submit} aria-label={initial ? 'Edit course' : 'New course'}>
      <Field id={`cn-${initial?.id ?? 'new'}`} label="Course name" error={m.fe.name}><input id={`cn-${initial?.id ?? 'new'}`} value={name} onChange={(e) => setName(e.target.value)} required maxLength={200} /></Field>
      <Field id={`cc-${initial?.id ?? 'new'}`} label="Code (optional)" error={m.fe.code}><input id={`cc-${initial?.id ?? 'new'}`} value={code} onChange={(e) => setCode(e.target.value)} maxLength={40} /></Field>
      <Field id={`cd-${initial?.id ?? 'new'}`} label="Description (optional)" error={m.fe.description}><input id={`cd-${initial?.id ?? 'new'}`} value={description} onChange={(e) => setDescription(e.target.value)} maxLength={2000} /></Field>
      <FormError error={m.error} />
      <button type="submit" disabled={m.busy}>{initial ? 'Save' : 'Add course'}</button>{' '}
      {onCancel && <button type="button" onClick={onCancel}>Cancel</button>}
    </form>
  );
}

export function CoursesPage({ canWrite }: { canWrite: boolean }) {
  const [q, setQ] = useState('');
  const [archived, setArchived] = useState('');
  const [editing, setEditing] = useState<string | null>(null);
  const list = useAsync(() => coursesApi.list({ q, archived }).then((r) => r.courses), [q, archived]);
  const m = useMutation();
  return (
    <section aria-labelledby="courses-h">
      <h2 id="courses-h">Courses</h2>
      {canWrite && <CourseForm onSaved={list.retry} />}
      <p><label htmlFor="cq">Search</label> <input id="cq" value={q} onChange={(e) => setQ(e.target.value)} /> <ArchiveFilter value={archived} onChange={setArchived} /></p>
      <FormError error={m.error} />
      <Listing state={list.state} retry={list.retry} empty="No courses yet">
        {(items) => (
          <table>
            <thead><tr><th scope="col">Name</th><th scope="col">Code</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead>
            <tbody>
              {items.map((c) => (
                <tr key={c.id}>
                  <td>{editing === c.id ? <CourseForm initial={c} onSaved={() => { setEditing(null); list.retry(); }} onCancel={() => setEditing(null)} /> : c.name}{c.archived_at && <em> (archived)</em>}</td>
                  <td>{c.code}</td>
                  <td>
                    {canWrite && editing !== c.id && <button type="button" onClick={() => setEditing(c.id)}>Edit</button>}{' '}
                    {canWrite && (c.archived_at
                      ? <button type="button" disabled={m.busy} onClick={() => m.run(() => coursesApi.restore(c.id), list.retry)}>Restore</button>
                      : <button type="button" disabled={m.busy} onClick={() => m.run(() => coursesApi.archive(c.id), list.retry)}>Archive</button>)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Listing>
    </section>
  );
}

// ---------- Batches ----------

function BatchForm({ courses, initial, onSaved, onCancel }: { courses: Course[]; initial?: Batch; onSaved: () => void; onCancel?: () => void }) {
  const [courseId, setCourseId] = useState(initial?.course_id ?? '');
  const [name, setName] = useState(initial?.name ?? '');
  const [start, setStart] = useState(initial?.start_date ?? '');
  const [end, setEnd] = useState(initial?.end_date ?? '');
  const m = useMutation();
  const k = initial?.id ?? 'new';
  const submit = (e: FormEvent) => {
    e.preventDefault();
    m.run(() => (initial
      ? batchesApi.update(initial.id, { name, start_date: start, end_date: end })
      : batchesApi.create({ course_id: courseId, name, start_date: start, end_date: end })), () => {
      if (!initial) { setName(''); setStart(''); setEnd(''); }
      onSaved();
    });
  };
  return (
    <form onSubmit={submit} aria-label={initial ? 'Edit batch' : 'New batch'}>
      {!initial && (
        <Field id={`bc-${k}`} label="Course" error={m.fe.course_id}>
          <select id={`bc-${k}`} value={courseId} onChange={(e) => setCourseId(e.target.value)} required>
            <option value="">Choose a course…</option>
            {courses.filter((c) => !c.archived_at).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
          </select>
        </Field>
      )}
      <Field id={`bn-${k}`} label="Batch name" error={m.fe.name}><input id={`bn-${k}`} value={name} onChange={(e) => setName(e.target.value)} required maxLength={200} /></Field>
      <Field id={`bs-${k}`} label="Start date" error={m.fe.start_date}><input id={`bs-${k}`} type="date" value={start} onChange={(e) => setStart(e.target.value)} /></Field>
      <Field id={`be-${k}`} label="End date" error={m.fe.end_date}><input id={`be-${k}`} type="date" value={end} onChange={(e) => setEnd(e.target.value)} /></Field>
      <FormError error={m.error} />
      <button type="submit" disabled={m.busy}>{initial ? 'Save' : 'Add batch'}</button>{' '}
      {onCancel && <button type="button" onClick={onCancel}>Cancel</button>}
    </form>
  );
}

export function BatchesPage({ canWrite }: { canWrite: boolean }) {
  const [courseId, setCourseId] = useState('');
  const [archived, setArchived] = useState('');
  const [editing, setEditing] = useState<string | null>(null);
  const courses = useAsync(() => coursesApi.list({ limit: 200 }).then((r) => r.courses), []);
  const list = useAsync(() => batchesApi.list({ course_id: courseId, archived }).then((r) => r.batches), [courseId, archived]);
  const m = useMutation();
  const cs = courses.state.status === 'success' ? courses.state.data : [];
  return (
    <section aria-labelledby="batches-h">
      <h2 id="batches-h">Batches</h2>
      {canWrite && <BatchForm courses={cs} onSaved={list.retry} />}
      <p>
        <label htmlFor="bf">Course</label>{' '}
        <select id="bf" value={courseId} onChange={(e) => setCourseId(e.target.value)}>
          <option value="">All courses</option>
          {cs.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
        </select>{' '}
        <ArchiveFilter value={archived} onChange={setArchived} />
      </p>
      <FormError error={m.error} />
      <Listing state={list.state} retry={list.retry} empty="No batches yet">
        {(items) => (
          <table>
            <thead><tr><th scope="col">Batch</th><th scope="col">Course</th><th scope="col">Dates</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead>
            <tbody>
              {items.map((b) => (
                <tr key={b.id}>
                  <td>{editing === b.id ? <BatchForm courses={cs} initial={b} onSaved={() => { setEditing(null); list.retry(); }} onCancel={() => setEditing(null)} /> : b.name}{b.archived_at && <em> (archived)</em>}</td>
                  <td>{b.course_name}</td>
                  <td>{b.start_date || '—'} to {b.end_date || '—'}</td>
                  <td>
                    {canWrite && editing !== b.id && <button type="button" onClick={() => setEditing(b.id)}>Edit</button>}{' '}
                    {canWrite && (b.archived_at
                      ? <button type="button" disabled={m.busy} onClick={() => m.run(() => batchesApi.restore(b.id), list.retry)}>Restore</button>
                      : <button type="button" disabled={m.busy} onClick={() => m.run(() => batchesApi.archive(b.id), list.retry)}>Archive</button>)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Listing>
    </section>
  );
}

// ---------- Students ----------

function StudentForm({ initial, onSaved, onCancel }: { initial?: Student; onSaved: () => void; onCancel?: () => void }) {
  const [f, setF] = useState({ student_number: initial?.student_number ?? '', full_name: initial?.full_name ?? '', email: initial?.email ?? '', phone: initial?.phone ?? '' });
  const m = useMutation();
  const k = initial?.id ?? 'new';
  const set = (key: keyof typeof f) => (e: { target: { value: string } }) => setF({ ...f, [key]: e.target.value });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    m.run(() => (initial ? studentsApi.update(initial.id, f) : studentsApi.create(f)), () => {
      if (!initial) setF({ student_number: '', full_name: '', email: '', phone: '' });
      onSaved();
    });
  };
  return (
    <form onSubmit={submit} aria-label={initial ? 'Edit student' : 'New student'}>
      <Field id={`sn-${k}`} label="Full name" error={m.fe.full_name}><input id={`sn-${k}`} value={f.full_name} onChange={set('full_name')} required maxLength={200} /></Field>
      <Field id={`sno-${k}`} label="Student number (optional)" error={m.fe.student_number}><input id={`sno-${k}`} value={f.student_number} onChange={set('student_number')} maxLength={40} /></Field>
      <Field id={`se-${k}`} label="Email (optional)" error={m.fe.email}><input id={`se-${k}`} type="email" value={f.email} onChange={set('email')} /></Field>
      <Field id={`sp-${k}`} label="Phone (optional)" error={m.fe.phone}><input id={`sp-${k}`} type="tel" value={f.phone} onChange={set('phone')} maxLength={20} /></Field>
      <FormError error={m.error} />
      <button type="submit" disabled={m.busy}>{initial ? 'Save' : 'Add student'}</button>{' '}
      {onCancel && <button type="button" onClick={onCancel}>Cancel</button>}
    </form>
  );
}

function EnrollmentRow({ studentId, e, canWrite, onChanged }: { studentId: string; e: Enrollment; canWrite: boolean; onChanged: () => void }) {
  const [date, setDate] = useState(e.completion_date);
  const [grade, setGrade] = useState(e.grade);
  const m = useMutation();
  return (
    <li>
      <strong>{e.course_name}</strong> / {e.batch_name} — {e.status}
      {e.status === 'completed' && <> on {e.completion_date}{e.grade && <>, grade {e.grade}</>}</>}
      {canWrite && e.status === 'enrolled' && (
        <form onSubmit={(ev) => { ev.preventDefault(); m.run(() => studentsApi.updateEnrollment(studentId, e.id, { status: 'completed', completion_date: date, grade }), onChanged); }} aria-label="Mark completed">
          <label htmlFor={`cd-${e.id}`}>Completion date</label> <input id={`cd-${e.id}`} type="date" value={date} onChange={(x) => setDate(x.target.value)} required />{' '}
          <label htmlFor={`g-${e.id}`}>Grade</label> <input id={`g-${e.id}`} value={grade} onChange={(x) => setGrade(x.target.value)} maxLength={40} />{' '}
          <button type="submit" disabled={m.busy}>Mark completed</button>{' '}
          <button type="button" disabled={m.busy} onClick={() => m.run(() => studentsApi.updateEnrollment(studentId, e.id, { status: 'withdrawn' }), onChanged)}>Withdraw</button>
          {m.fe.completion_date && <span role="alert"> {m.fe.completion_date}</span>}
          <FormError error={m.error} />
        </form>
      )}
    </li>
  );
}

function StudentDetail({ id, canWrite, onClose, onChanged }: { id: string; canWrite: boolean; onClose: () => void; onChanged: () => void }) {
  const detail = useAsync(() => studentsApi.get(id).then((r) => r.student), [id]);
  const batches = useAsync(() => batchesApi.list({ limit: 200 }).then((r) => r.batches), []);
  const [batchId, setBatchId] = useState('');
  const [editing, setEditing] = useState(false);
  const m = useMutation();
  if (detail.state.status === 'loading') return <Loading />;
  if (detail.state.status === 'error') return <ErrorState message={detail.state.error.message} onRetry={detail.retry} />;
  const s = detail.state.data;
  const refresh = () => { detail.retry(); onChanged(); };
  const bs = batches.state.status === 'success' ? batches.state.data : [];
  return (
    <section aria-labelledby="sd-h">
      <h3 id="sd-h">{s.full_name}{s.archived_at && <em> (archived)</em>}</h3>
      <button type="button" onClick={onClose}>Close</button>
      {editing
        ? <StudentForm initial={s} onSaved={() => { setEditing(false); refresh(); }} onCancel={() => setEditing(false)} />
        : (
          <dl>
            <div><dt>Student number</dt><dd>{s.student_number || '—'}</dd></div>
            <div><dt>Email</dt><dd>{s.email || '—'}</dd></div>
            <div><dt>Phone</dt><dd>{s.phone || '—'}</dd></div>
          </dl>
        )}
      {canWrite && !editing && <p><button type="button" onClick={() => setEditing(true)}>Edit</button></p>}
      <h4>Enrollments</h4>
      {(s.enrollments ?? []).length === 0 ? <p>Not enrolled in any batch.</p> : (
        <ul>{(s.enrollments ?? []).map((e) => <EnrollmentRow key={e.id} studentId={s.id} e={e} canWrite={canWrite} onChanged={refresh} />)}</ul>
      )}
      {canWrite && !s.archived_at && (
        <form onSubmit={(e) => { e.preventDefault(); m.run(() => studentsApi.enroll(s.id, batchId), () => { setBatchId(''); refresh(); }); }} aria-label="Enroll in a batch">
          <label htmlFor="eb">Enroll in a batch</label>{' '}
          <select id="eb" value={batchId} onChange={(e) => setBatchId(e.target.value)} required>
            <option value="">Choose a batch…</option>
            {bs.filter((b) => !b.archived_at).map((b) => <option key={b.id} value={b.id}>{b.course_name} / {b.name}</option>)}
          </select>{' '}
          <button type="submit" disabled={m.busy}>Enroll</button>
          {m.fe.batch_id && <span role="alert"> {m.fe.batch_id}</span>}
          <FormError error={m.error} />
        </form>
      )}
    </section>
  );
}

export function StudentsPage({ canWrite, canExport = false }: { canWrite: boolean; canExport?: boolean }) {
  const [q, setQ] = useState('');
  const [term, setTerm] = useState('');
  const [courseId, setCourseId] = useState('');
  const [status, setStatus] = useState('');
  const [archived, setArchived] = useState('');
  const [offset, setOffset] = useState(0);
  const [open, setOpen] = useState<string | null>(null);
  const limit = 25;
  const courses = useAsync(() => coursesApi.list({ limit: 200 }).then((r) => r.courses), []);
  const list = useAsync(() => studentsApi.list({ q: term, course_id: courseId, status, archived, limit, offset }), [term, courseId, status, archived, offset]);
  const m = useMutation();
  const cs = courses.state.status === 'success' ? courses.state.data : [];
  const reset = <T,>(set: (v: T) => void) => (v: T) => { setOffset(0); set(v); };
  return (
    <section aria-labelledby="students-h">
      <h2 id="students-h">Students</h2>
      {canWrite && <StudentForm onSaved={list.retry} />}
      <form role="search" onSubmit={(e) => { e.preventDefault(); setOffset(0); setTerm(q.trim()); }}>
        <label htmlFor="sq">Search name, email or number</label> <input id="sq" value={q} onChange={(e) => setQ(e.target.value)} /> <button type="submit">Search</button>{' '}
        <label htmlFor="sc">Course</label>{' '}
        <select id="sc" value={courseId} onChange={(e) => reset(setCourseId)(e.target.value)}>
          <option value="">All</option>{cs.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
        </select>{' '}
        <label htmlFor="ss">Status</label>{' '}
        <select id="ss" value={status} onChange={(e) => reset(setStatus)(e.target.value)}>
          <option value="">Any</option><option value="enrolled">Enrolled</option><option value="completed">Completed</option><option value="withdrawn">Withdrawn</option>
        </select>{' '}
        <ArchiveFilter value={archived} onChange={reset(setArchived)} />
      </form>
      {canExport && (
        <p>
          <button type="button" disabled={m.busy} onClick={() => m.run(async () => {
            const blob = await studentsApi.exportCsv({ q: term, course_id: courseId, status, archived });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = 'students.csv';
            document.body.appendChild(a);
            a.click();
            a.remove();
            setTimeout(() => URL.revokeObjectURL(url), 1000);
          })}>Export students (CSV)</button>{' '}
          <small>Uses the search and filters above.</small>
        </p>
      )}
      <FormError error={m.error} />
      {open && <StudentDetail id={open} canWrite={canWrite} onClose={() => setOpen(null)} onChanged={list.retry} />}
      {list.state.status === 'loading' && <Loading />}
      {list.state.status === 'error' && <ErrorState message={list.state.error.message} requestId={list.state.error instanceof ApiError ? list.state.error.requestId : undefined} onRetry={list.retry} />}
      {list.state.status === 'success' && (list.state.data.students.length === 0
        ? <EmptyState title="No students found" />
        : (
          <>
            <table>
              <thead><tr><th scope="col">Name</th><th scope="col">Number</th><th scope="col">Email</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead>
              <tbody>
                {list.state.data.students.map((s) => (
                  <tr key={s.id}>
                    <td>{s.full_name}{s.archived_at && <em> (archived)</em>}</td>
                    <td>{s.student_number}</td>
                    <td>{s.email}</td>
                    <td>
                      <button type="button" onClick={() => setOpen(s.id)}>View</button>{' '}
                      {canWrite && (s.archived_at
                        ? <button type="button" disabled={m.busy} onClick={() => m.run(() => studentsApi.restore(s.id), list.retry)}>Restore</button>
                        : <button type="button" disabled={m.busy} onClick={() => m.run(() => studentsApi.archive(s.id), list.retry)}>Archive</button>)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <p>
              <button type="button" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - limit))}>Previous</button>{' '}
              <button type="button" disabled={!list.state.data.has_more} onClick={() => setOffset(offset + limit)}>Next</button>
            </p>
          </>
        ))}
    </section>
  );
}
