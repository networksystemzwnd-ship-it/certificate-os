import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { ApiError } from '../../lib/api';
import { Loading } from '../../components/States';
import { useSession } from '../auth/session';
import { staffApi } from './api';

const KEY = 'pending_invite';

function stash(token: string | null) {
  try {
    if (token) sessionStorage.setItem(KEY, token);
    else sessionStorage.removeItem(KEY);
  } catch {
    /* private mode: the user can open the link again after logging in */
  }
}
function stashed(): string | null {
  try {
    return sessionStorage.getItem(KEY);
  } catch {
    return null;
  }
}

/** True when a login/registration should return to the invitation page. */
export const hasPendingInvite = () => stashed() !== null;

// The token travels in the URL fragment (never sent to the server or logged). It is moved into
// sessionStorage for the login/registration detour and removed as soon as it has been used.
export function AcceptInvite() {
  const { status } = useSession();
  const [token] = useState<string | null>(() => {
    const m = /(?:^#|&)token=([^&]+)/.exec(window.location.hash);
    const t = m ? decodeURIComponent(m[1]) : stashed();
    if (m) {
      stash(t);
      window.history.replaceState(null, '', window.location.pathname);
    }
    return t;
  });
  const [result, setResult] = useState<{ name: string; role: string } | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const started = useRef(false);

  useEffect(() => {
    if (status !== 'authenticated' || !token || started.current) return;
    started.current = true;
    staffApi
      .accept(token)
      .then((r) => setResult({ name: r.institute.name, role: r.role }))
      .catch((e) => setError(e instanceof Error ? e : new Error('Something went wrong.')))
      .finally(() => stash(null));
  }, [status, token]);

  if (!token && !result) {
    return (
      <section>
        <h1>Invitation</h1>
        <p role="alert">This invitation link is incomplete. Ask the institute owner to send it again.</p>
      </section>
    );
  }
  if (status === 'loading') return <Loading label="Checking your session…" />;
  if (status === 'anonymous') {
    return (
      <section>
        <h1>You have been invited</h1>
        <p>
          Log in or create an account <strong>using the email address the invitation was sent to</strong>, then you will join the institute
          automatically.
        </p>
        <p>
          <Link to="/login">Log in</Link> · <Link to="/register">Create account</Link>
        </p>
      </section>
    );
  }
  return (
    <section>
      <h1>Invitation</h1>
      {!result && !error && <Loading label="Joining the institute…" />}
      {result && (
        <p role="status">
          You joined <strong>{result.name}</strong> as <strong>{result.role}</strong>. <Link to="/app">Open your workspace</Link>
        </p>
      )}
      {error && (
        <p role="alert">
          {error instanceof ApiError && error.status === 404
            ? 'This invitation is invalid or has expired, or it was sent to a different email address than the one you are signed in with.'
            : error.message}
        </p>
      )}
    </section>
  );
}
