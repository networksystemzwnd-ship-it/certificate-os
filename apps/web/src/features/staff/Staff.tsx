import { useState, type FormEvent } from 'react';
import { ApiError } from '../../lib/api';
import { useAsync } from '../../lib/useAsync';
import { EmptyState, ErrorState, Loading } from '../../components/States';
import { fieldErrors } from '../auth/session';
import { GRANTABLE, staffApi, type Invitation, type Member } from './api';

// Owner-only (staff.manage). The UI only hides what the API would refuse anyway.

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
  return { busy, error, run, fe: fieldErrors(error) };
}

function Problem({ error }: { error: Error | null }) {
  if (!error || (error instanceof ApiError && error.status === 422)) return null;
  return <p role="alert">{error.message}{error instanceof ApiError && error.requestId ? ` (reference: ${error.requestId})` : ''}</p>;
}

function Grants({ value, onChange, idPrefix }: { value: string[]; onChange: (v: string[]) => void; idPrefix: string }) {
  return (
    <fieldset>
      <legend>Extra permissions (optional)</legend>
      {GRANTABLE.map((g) => (
        <label key={g.key} style={{ display: 'block' }}>
          <input
            id={`${idPrefix}-${g.key}`}
            type="checkbox"
            style={{ minWidth: 0 }}
            checked={value.includes(g.key)}
            onChange={(e) => onChange(e.target.checked ? [...value, g.key] : value.filter((x) => x !== g.key))}
          />{' '}
          {g.label}
        </label>
      ))}
    </fieldset>
  );
}

function InviteForm({ onInvited }: { onInvited: () => void }) {
  const [email, setEmail] = useState('');
  const [role, setRole] = useState<'staff' | 'admin'>('staff');
  const [grants, setGrants] = useState<string[]>([]);
  const [link, setLink] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const m = useMutation();

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setLink(null);
    setCopied(false);
    m.run(async () => {
      const res = await staffApi.invite({ email, role, grants: role === 'staff' ? grants : [] });
      setLink(`${window.location.origin}${res.accept_path}`);
    }, () => {
      setEmail('');
      setGrants([]);
      onInvited();
    });
  };

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(link!);
      setCopied(true);
    } catch {
      /* the link stays visible for manual copying */
    }
  };

  return (
    <section aria-labelledby="invite-h">
      <h4 id="invite-h">Invite someone</h4>
      <form onSubmit={submit} aria-label="Invite staff">
        <p>
          <label htmlFor="inv-email">Their email address</label>
          <br />
          <input id="inv-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required maxLength={254} />
          {m.fe.email && <span role="alert"> {m.fe.email}</span>}
        </p>
        <p>
          <label htmlFor="inv-role">Role</label>
          <br />
          <select id="inv-role" value={role} onChange={(e) => setRole(e.target.value as 'staff' | 'admin')}>
            <option value="staff">Staff — manage students, courses and batches</option>
            <option value="admin">Admin — also import students and manage certificates</option>
          </select>
          {m.fe.role && <span role="alert"> {m.fe.role}</span>}
        </p>
        {role === 'staff' && <Grants value={grants} onChange={setGrants} idPrefix="inv" />}
        {m.fe.grants && <p role="alert">{m.fe.grants}</p>}
        <Problem error={m.error} />
        <button type="submit" disabled={m.busy}>Create invitation link</button>
      </form>
      {link && (
        <div role="status" aria-label="Invitation link">
          <p>
            Send this link to the invited person. <strong>It is shown only once</strong>, works for 7 days, and only for the invited
            email address.
          </p>
          <p>
            <input readOnly aria-label="Invitation link" value={link} onFocus={(e) => e.currentTarget.select()} style={{ width: '100%' }} />
          </p>
          <button type="button" onClick={copy}>{copied ? 'Copied' : 'Copy link'}</button>
        </div>
      )}
    </section>
  );
}

function MemberRow({ member, self, onChanged }: { member: Member; self: boolean; onChanged: () => void }) {
  const [role, setRole] = useState<'admin' | 'staff'>(member.role === 'admin' ? 'admin' : 'staff');
  const [grants, setGrants] = useState<string[]>(member.grants);
  const [editing, setEditing] = useState(false);
  const m = useMutation();
  const protectedRow = member.role === 'owner' || self;

  return (
    <tr>
      <td>{member.email || member.user_id}</td>
      <td>
        {editing ? (
          <>
            <label htmlFor={`role-${member.id}`} className="sr-only">Role for {member.email}</label>
            <select id={`role-${member.id}`} value={role} onChange={(e) => setRole(e.target.value as 'admin' | 'staff')}>
              <option value="staff">staff</option>
              <option value="admin">admin</option>
            </select>
            {role === 'staff' && <Grants value={grants} onChange={setGrants} idPrefix={`g-${member.id}`} />}
          </>
        ) : (
          <>
            {member.role}
            {member.grants.length > 0 && <small> (+ {member.grants.map((g) => g.replace('certificates.', '')).join(', ')})</small>}
          </>
        )}
      </td>
      <td>{member.status}</td>
      <td>
        {!protectedRow && member.status === 'active' && (
          <>
            {editing ? (
              <>
                <button type="button" disabled={m.busy} onClick={() => m.run(() => staffApi.changeMember(member.id, { role, grants: role === 'staff' ? grants : [] }), () => { setEditing(false); onChanged(); })}>Save</button>{' '}
                <button type="button" onClick={() => setEditing(false)}>Cancel</button>
              </>
            ) : (
              <button type="button" onClick={() => setEditing(true)}>Change role</button>
            )}{' '}
            <button
              type="button"
              disabled={m.busy}
              onClick={() => {
                if (window.confirm(`Remove ${member.email || 'this member'}? They lose access immediately.`)) m.run(() => staffApi.removeMember(member.id), onChanged);
              }}
            >
              Remove
            </button>
            <Problem error={m.error} />
          </>
        )}
        {protectedRow && <small>{member.role === 'owner' ? 'Owner' : 'You'}</small>}
      </td>
    </tr>
  );
}

function InvitationRow({ inv, onChanged }: { inv: Invitation; onChanged: () => void }) {
  const m = useMutation();
  return (
    <tr>
      <td>{inv.email}</td>
      <td>{inv.role}</td>
      <td>{inv.status}</td>
      <td>{new Date(inv.expires_at).toLocaleDateString()}</td>
      <td>
        {inv.status === 'pending' && (
          <button type="button" disabled={m.busy} onClick={() => m.run(() => staffApi.revoke(inv.id), onChanged)}>Revoke</button>
        )}
        <Problem error={m.error} />
      </td>
    </tr>
  );
}

export function StaffPage({ selfEmail }: { selfEmail?: string }) {
  const members = useAsync(() => staffApi.members(), []);
  const invites = useAsync(() => staffApi.invitations(), []);
  const reload = () => {
    members.retry();
    invites.retry();
  };

  return (
    <section aria-labelledby="staff-h">
      <h3 id="staff-h">Staff</h3>
      <InviteForm onInvited={reload} />

      <h4>Members</h4>
      {members.state.status === 'loading' && <Loading />}
      {members.state.status === 'error' && <ErrorState message={members.state.error.message} onRetry={members.retry} />}
      {members.state.status === 'success' && (
        <table aria-label="Members">
          <thead>
            <tr><th scope="col">Email</th><th scope="col">Role</th><th scope="col">Status</th><th scope="col">Actions</th></tr>
          </thead>
          <tbody>
            {members.state.data.members.map((mem) => (
              <MemberRow key={mem.id} member={mem} self={!!selfEmail && mem.email === selfEmail} onChanged={reload} />
            ))}
          </tbody>
        </table>
      )}

      <h4>Invitations</h4>
      {invites.state.status === 'loading' && <Loading />}
      {invites.state.status === 'error' && <ErrorState message={invites.state.error.message} onRetry={invites.retry} />}
      {invites.state.status === 'success' &&
        (invites.state.data.invitations.length === 0 ? (
          <EmptyState title="No invitations yet" />
        ) : (
          <table aria-label="Invitations">
            <thead>
              <tr><th scope="col">Email</th><th scope="col">Role</th><th scope="col">Status</th><th scope="col">Expires</th><th scope="col">Actions</th></tr>
            </thead>
            <tbody>
              {invites.state.data.invitations.map((i) => (
                <InvitationRow key={i.id} inv={i} onChanged={reload} />
              ))}
            </tbody>
          </table>
        ))}
    </section>
  );
}
