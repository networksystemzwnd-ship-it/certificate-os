import { useEffect, useState, type FormEvent } from 'react';
import { ApiError } from '../../lib/api';
import { useAsync } from '../../lib/useAsync';
import { EmptyState, ErrorState, Loading } from '../../components/States';
import { fieldErrors } from '../auth/session';
import { templatesApi, type Template, type TemplateConfig } from './api';

// Templates are a preset plus a few fields: there is no free-form HTML. The preview is server-rendered
// from sample data and shown in a fully sandboxed iframe (no scripts, no network, no same-origin access).

function Preview({ load, label }: { load: () => Promise<{ html: string }>; label: string }) {
  const [html, setHtml] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const run = () => {
    setBusy(true);
    setError(null);
    load()
      .then((r) => setHtml(r.html))
      .catch((e) => setError(e instanceof ApiError && e.status === 422 ? 'Fix the highlighted fields to see a preview.' : e.message))
      .finally(() => setBusy(false));
  };
  return (
    <div>
      <button type="button" onClick={run} disabled={busy}>{label}</button>
      {error && <p role="alert">{error}</p>}
      {html && (
        <iframe title="Certificate preview" sandbox="" srcDoc={html}
          style={{ width: '100%', height: 480, border: '1px solid #ccc', background: '#fff' }} />
      )}
    </div>
  );
}

const CHECKS: { key: keyof TemplateConfig; label: string }[] = [
  { key: 'show_completion_date', label: 'Show completion date' },
  { key: 'show_grade', label: 'Show grade (when the student has one)' },
  { key: 'show_certificate_number', label: 'Show certificate number' },
  { key: 'show_qr', label: 'Show verification QR code' },
];

function ConfigFields({ value, onChange, errors, placeholders }: {
  value: TemplateConfig; onChange: (c: TemplateConfig) => void; errors: Record<string, string>; placeholders: string[];
}) {
  const set = <K extends keyof TemplateConfig>(k: K, v: TemplateConfig[K]) => onChange({ ...value, [k]: v });
  const err = (k: string) => errors[k] && <span role="alert"> {errors[k]}</span>;
  return (
    <>
      <p>
        <label htmlFor="tpl-preset">Style</label><br />
        <select id="tpl-preset" value={value.preset} onChange={(e) => set('preset', e.target.value as TemplateConfig['preset'])}>
          <option value="classic">Classic</option><option value="modern">Modern</option><option value="minimal">Minimal</option>
        </select>{err('preset')}
      </p>
      <p>
        <label htmlFor="tpl-orient">Page</label><br />
        <select id="tpl-orient" value={value.orientation} onChange={(e) => set('orientation', e.target.value as TemplateConfig['orientation'])}>
          <option value="landscape">A4 landscape</option><option value="portrait">A4 portrait</option>
        </select>{err('orientation')}
      </p>
      <p>
        <label htmlFor="tpl-color">Accent colour</label><br />
        <input id="tpl-color" type="color" value={/^#[0-9a-f]{6}$/i.test(value.accent_color) ? value.accent_color : '#000000'}
          onChange={(e) => set('accent_color', e.target.value)} style={{ minWidth: 0 }} />{err('accent_color')}
      </p>
      <p>
        <label htmlFor="tpl-title">Title</label><br />
        <input id="tpl-title" value={value.title} maxLength={80} onChange={(e) => set('title', e.target.value)} />{err('title')}
      </p>
      <p>
        <label htmlFor="tpl-body">Certificate text</label><br />
        <textarea id="tpl-body" rows={5} value={value.body} maxLength={600} onChange={(e) => set('body', e.target.value)} style={{ width: '100%' }} />{err('body')}
        <br /><small>Placeholders: {placeholders.map((p) => `{{${p}}}`).join(' ')}. The text must include {'{{student_name}}'}.</small>
      </p>
      <p>
        <label htmlFor="tpl-signame">Signatory name</label><br />
        <input id="tpl-signame" value={value.signatory_name} maxLength={100} onChange={(e) => set('signatory_name', e.target.value)} />{err('signatory_name')}
      </p>
      <p>
        <label htmlFor="tpl-sigtitle">Signatory title</label><br />
        <input id="tpl-sigtitle" value={value.signatory_title} maxLength={100} onChange={(e) => set('signatory_title', e.target.value)} />{err('signatory_title')}
      </p>
      <p>
        <label htmlFor="tpl-footer">Footer (plain text)</label><br />
        <input id="tpl-footer" value={value.footer} maxLength={200} onChange={(e) => set('footer', e.target.value)} />{err('footer')}
      </p>
      <fieldset>
        <legend>Show on the certificate</legend>
        {CHECKS.map((c) => (
          <label key={c.key} style={{ display: 'block' }}>
            <input type="checkbox" style={{ minWidth: 0 }} checked={value[c.key] as boolean} onChange={(e) => set(c.key, e.target.checked as never)} /> {c.label}
          </label>
        ))}
      </fieldset>
    </>
  );
}

function Editor({ initial, existing, onSaved, onCancel }: {
  initial: TemplateConfig | null; existing?: Template; onSaved: () => void; onCancel: () => void;
}) {
  const presets = useAsync(() => templatesApi.presets(), []);
  const [name, setName] = useState(existing?.name ?? '');
  const [cfg, setCfg] = useState<TemplateConfig | null>(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    if (!cfg && presets.state.status === 'success') setCfg(presets.state.data.presets[0]!.config);
  }, [cfg, presets.state]);

  if (presets.state.status === 'loading' || !cfg) return <Loading />;
  if (presets.state.status === 'error') return <ErrorState message={presets.state.error.message} onRetry={presets.retry} />;
  const { presets: list, placeholders } = presets.state.data;
  const errors = fieldErrors(error);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (existing) await templatesApi.addVersion(existing.id, cfg);
      else await templatesApi.create(name, cfg);
      onSaved();
    } catch (err) {
      setError(err instanceof Error ? err : new Error('Something went wrong.'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} aria-label={existing ? 'New version' : 'New template'}>
      <h4>{existing ? `New version of ${existing.name}` : 'New template'}</h4>
      {!existing && (
        <>
          <p>
            <label htmlFor="tpl-name">Template name</label><br />
            <input id="tpl-name" value={name} maxLength={100} onChange={(e) => setName(e.target.value)} required />
            {errors.name && <span role="alert"> {errors.name}</span>}
          </p>
          <p>
            Start from:{' '}
            {list.map((p) => (
              <button key={p.preset} type="button" onClick={() => setCfg(p.config)}>{p.label}</button>
            ))}
          </p>
        </>
      )}
      {existing && <p><small>Saving creates a new version. Earlier versions never change, so certificates already issued stay exactly as they were.</small></p>}
      <ConfigFields value={cfg} onChange={setCfg} errors={errors} placeholders={placeholders} />
      {error && !(error instanceof ApiError && error.status === 422) && <p role="alert">{error.message}</p>}
      <Preview label="Preview with sample data" load={() => templatesApi.previewDraft(cfg)} />
      <p>
        <button type="submit" disabled={busy}>{existing ? 'Save as new version' : 'Create template'}</button>{' '}
        <button type="button" onClick={onCancel}>Cancel</button>
      </p>
    </form>
  );
}

function Detail({ id, canWrite, onBack, onChanged }: { id: string; canWrite: boolean; onBack: () => void; onChanged: () => void }) {
  const { state, retry } = useAsync(() => templatesApi.get(id), [id]);
  const [editing, setEditing] = useState(false);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [viewing, setViewing] = useState<number | null>(null);

  if (state.status === 'loading') return <Loading />;
  if (state.status === 'error') return <ErrorState message={state.error.message} onRetry={retry} />;
  const t = state.data.template;
  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      setRenaming(null);
      retry();
      onChanged();
    } catch (e) {
      setError(e instanceof Error ? e : new Error('Something went wrong.'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section aria-label={`Template ${t.name}`}>
      <p><button type="button" onClick={onBack}>← All templates</button></p>
      <h4>{t.name} {t.archived_at && <small>(archived)</small>}</h4>
      <p>Current version: <strong>{t.current_version.number}</strong> · style {t.current_version.config?.preset}</p>
      {error && <p role="alert">{fieldErrors(error).name ?? error.message}</p>}
      {canWrite && (
        <p>
          {renaming === null ? (
            <button type="button" onClick={() => setRenaming(t.name)}>Rename</button>
          ) : (
            <>
              <label htmlFor="tpl-rename" className="sr-only">New name</label>
              <input id="tpl-rename" value={renaming} onChange={(e) => setRenaming(e.target.value)} />{' '}
              <button type="button" disabled={busy} onClick={() => act(() => templatesApi.rename(id, renaming))}>Save name</button>{' '}
              <button type="button" onClick={() => setRenaming(null)}>Cancel</button>
            </>
          )}{' '}
          {!t.archived_at && <button type="button" onClick={() => setEditing(true)}>Edit (new version)</button>}{' '}
          {t.archived_at ? (
            <button type="button" disabled={busy} onClick={() => act(() => templatesApi.restore(id))}>Restore</button>
          ) : (
            <button type="button" disabled={busy} onClick={() => act(() => templatesApi.archive(id))}>Archive</button>
          )}
        </p>
      )}
      {editing && t.current_version.config && (
        <Editor initial={t.current_version.config} existing={t} onCancel={() => setEditing(false)}
          onSaved={() => { setEditing(false); retry(); onChanged(); }} />
      )}
      <Preview label="Preview current version" load={() => templatesApi.previewVersion(id, t.current_version.number)} />
      <h5>Versions</h5>
      <ul>
        {(t.versions ?? []).map((v) => (
          <li key={v.number}>
            Version {v.number} · {new Date(v.created_at).toLocaleDateString()}{' '}
            <button type="button" onClick={() => setViewing(v.number)}>Show version {v.number}</button>
          </li>
        ))}
      </ul>
      {viewing !== null && (
        <Preview key={viewing} label={`Preview version ${viewing}`} load={() => templatesApi.previewVersion(id, viewing)} />
      )}
    </section>
  );
}

export function TemplatesPage({ canWrite }: { canWrite: boolean }) {
  const [showArchived, setShowArchived] = useState(false);
  const list = useAsync(() => templatesApi.list(showArchived ? 'all' : undefined), [showArchived]);
  const [creating, setCreating] = useState(false);
  const [openId, setOpenId] = useState<string | null>(null);

  if (openId) return <Detail id={openId} canWrite={canWrite} onBack={() => setOpenId(null)} onChanged={list.retry} />;
  return (
    <section aria-labelledby="tpl-h">
      <h3 id="tpl-h">Certificate templates</h3>
      {canWrite && !creating && <p><button type="button" onClick={() => setCreating(true)}>New template</button></p>}
      {creating && <Editor initial={null} onCancel={() => setCreating(false)} onSaved={() => { setCreating(false); list.retry(); }} />}
      <label><input type="checkbox" style={{ minWidth: 0 }} checked={showArchived} onChange={(e) => setShowArchived(e.target.checked)} /> Show archived</label>
      {list.state.status === 'loading' && <Loading />}
      {list.state.status === 'error' && <ErrorState message={list.state.error.message} onRetry={list.retry} />}
      {list.state.status === 'success' &&
        (list.state.data.templates.length === 0 ? (
          <EmptyState title="No templates yet">{canWrite ? 'Create one from a preset.' : 'An admin can create templates.'}</EmptyState>
        ) : (
          <table aria-label="Templates">
            <thead><tr><th scope="col">Name</th><th scope="col">Style</th><th scope="col">Version</th><th scope="col">Status</th><th scope="col"><span className="sr-only">Open</span></th></tr></thead>
            <tbody>
              {list.state.data.templates.map((t) => (
                <tr key={t.id}>
                  <td>{t.name}</td>
                  <td>{t.current_version.config?.preset}</td>
                  <td>{t.current_version.number}</td>
                  <td>{t.archived_at ? 'archived' : 'active'}</td>
                  <td><button type="button" onClick={() => setOpenId(t.id)}>Open {t.name}</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        ))}
    </section>
  );
}
