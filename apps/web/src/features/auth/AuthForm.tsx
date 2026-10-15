import { useState, type FormEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { ApiError } from '../../lib/api';
import { ErrorState } from '../../components/States';
import { fieldErrors, useSession } from './session';

export function AuthForm({ mode }: { mode: 'login' | 'register' }) {
  const { login, register } = useSession();
  const navigate = useNavigate();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | Error | null>(null);

  const isLogin = mode === 'login';
  const fields = fieldErrors(error);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await (isLogin ? login : register)(email, password);
      navigate('/app');
    } catch (err) {
      setError(err instanceof Error ? err : new Error('Something went wrong.'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section>
      <h1>{isLogin ? 'Log in' : 'Create account'}</h1>
      <form onSubmit={onSubmit} noValidate>
        <p>
          <label htmlFor="email">Email</label>
          <br />
          <input
            id="email"
            type="email"
            autoComplete="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            aria-invalid={Boolean(fields.email)}
            aria-describedby={fields.email ? 'email-error' : undefined}
            required
          />
          {fields.email && <span id="email-error"> {fields.email}</span>}
        </p>
        <p>
          <label htmlFor="password">Password</label>
          <br />
          <input
            id="password"
            type="password"
            autoComplete={isLogin ? 'current-password' : 'new-password'}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            aria-invalid={Boolean(fields.password)}
            aria-describedby={fields.password ? 'password-error' : undefined}
            required
          />
          {fields.password && <span id="password-error"> {fields.password}</span>}
        </p>
        {!isLogin && <p>Use at least 12 characters.</p>}
        {error && Object.keys(fields).length === 0 && (
          <ErrorState message={error.message} requestId={error instanceof ApiError ? error.requestId : undefined} />
        )}
        <button type="submit" disabled={busy}>
          {busy ? 'Please wait…' : isLogin ? 'Log in' : 'Create account'}
        </button>
      </form>
      <p>
        {isLogin ? (
          <>
            New here? <Link to="/register">Create an account</Link>
          </>
        ) : (
          <>
            Already registered? <Link to="/login">Log in</Link>
          </>
        )}
      </p>
    </section>
  );
}
