import { useState } from 'react';
import { useAsync } from '../../lib/useAsync';
import { ErrorState, Loading } from '../../components/States';
import { auditApi } from '../applications/api';

export function OpsAudit() {
  const [entity, setEntity] = useState('');
  const [action, setAction] = useState('');
  const [applied, setApplied] = useState({ entity: '', action: '' });
  const list = useAsync(() => auditApi.list(applied.entity, applied.action), [applied]);
  return (
    <section>
      <h2>Audit log</h2>
      <form onSubmit={(e) => { e.preventDefault(); setApplied({ entity: entity.trim(), action: action.trim() }); }}>
        <label htmlFor="entity">Entity type</label> <input id="entity" value={entity} onChange={(e) => setEntity(e.target.value)} placeholder="institute" />{' '}
        <label htmlFor="action">Action</label> <input id="action" value={action} onChange={(e) => setAction(e.target.value)} placeholder="institute.suspended" />{' '}
        <button type="submit">Filter</button>
      </form>
      {list.state.status === 'loading' && <Loading />}
      {list.state.status === 'error' && <ErrorState message={list.state.error.message} onRetry={list.retry} />}
      {list.state.status === 'success' && (
        <table>
          <thead><tr><th scope="col">When</th><th scope="col">Action</th><th scope="col">Entity</th><th scope="col">Actor</th><th scope="col">Details</th></tr></thead>
          <tbody>
            {list.state.data.map((r) => (
              <tr key={r.id}>
                <td>{new Date(r.occurred_at).toLocaleString()}</td>
                <td><code>{r.action}</code></td>
                <td>{r.entity_type} {r.entity_id}</td>
                <td>{r.actor_type}{r.actor_user_id ? ` ${r.actor_user_id}` : ''}</td>
                <td><code>{r.metadata ? JSON.stringify(r.metadata) : ''}</code></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
