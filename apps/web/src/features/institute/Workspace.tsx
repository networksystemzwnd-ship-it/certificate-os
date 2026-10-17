import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, ApiError, setActiveInstitute } from '../../lib/api';
import { useAsync } from '../../lib/useAsync';
import { EmptyState, ErrorState, Loading } from '../../components/States';

interface InstituteSummary {
  id: string;
  slug: string;
  name: string;
  status: string;
  role: string;
}

interface InstituteDetail extends InstituteSummary {
  permissions?: string[];
}

function Detail({ id }: { id: string }) {
  const { state, retry } = useAsync(() => {
    setActiveInstitute(id); // the server still verifies membership on every request
    return api<{ institute: InstituteDetail }>('/api/v1/institute');
  }, [id]);

  if (state.status === 'loading') return <Loading label="Loading institute…" />;
  if (state.status === 'error') {
    return (
      <ErrorState
        message={state.error.message}
        requestId={state.error instanceof ApiError ? state.error.requestId : undefined}
        onRetry={retry}
      />
    );
  }
  const inst = state.data.institute;
  return (
    <section aria-labelledby="inst-name">
      <h2 id="inst-name">{inst.name}</h2>
      <p>
        Your role: <strong>{inst.role}</strong>
      </p>
      {inst.status !== 'active' && (
        <p role="status">
          This institute is <strong>{inst.status}</strong>. Access is read-only.
        </p>
      )}
    </section>
  );
}

export function WorkspaceView() {
  const { state, retry } = useAsync(() => api<{ institutes: InstituteSummary[] }>('/api/v1/me/institutes'), []);
  const [selected, setSelected] = useState<string | null>(null);

  const institutes = state.status === 'success' ? state.data.institutes : [];
  useEffect(() => {
    if (institutes.length === 1) setSelected(institutes[0].id);
  }, [institutes]);

  return (
    <section>
      <h1>Institute workspace</h1>
      {state.status === 'loading' && <Loading label="Loading your institutes…" />}
      {state.status === 'error' && <ErrorState message={state.error.message} onRetry={retry} />}
      {state.status === 'success' && institutes.length === 0 && (
        <EmptyState title="No institute access">
          <p>Your account is not a member of any institute yet. Ask an institute owner to add you, or{' '}
            <Link to="/apply">apply to register your own institute</Link>.
          </p>
        </EmptyState>
      )}
      {institutes.length > 1 && (
        <p>
          <label htmlFor="institute">Institute</label>
          <br />
          <select id="institute" value={selected ?? ''} onChange={(e) => setSelected(e.target.value || null)}>
            <option value="">Choose an institute…</option>
            {institutes.map((i) => (
              <option key={i.id} value={i.id}>
                {i.name}
              </option>
            ))}
          </select>
        </p>
      )}
      {selected && <Detail id={selected} />}
    </section>
  );
}
