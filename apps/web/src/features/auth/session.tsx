import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { api, ApiError } from '../../lib/api';

export interface User {
  id: string;
  email: string;
  platform_role?: string;
  /** For showing/hiding UI only. The server is authoritative for every permission check. */
  permissions: string[];
}

type Status = 'loading' | 'anonymous' | 'authenticated';

interface SessionValue {
  status: Status;
  user: User | null;
  login: (email: string, password: string) => Promise<void>;
  register: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
}

const SessionContext = createContext<SessionValue | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<Status>('loading');
  const [user, setUser] = useState<User | null>(null);

  useEffect(() => {
    let cancelled = false;
    api<{ user: User }>('/api/v1/me').then(
      (r) => {
        if (cancelled) return;
        setUser(r.user);
        setStatus('authenticated');
      },
      () => {
        if (cancelled) return;
        // 401 is the normal anonymous case; any other failure is also treated as anonymous
        // (the server still enforces access on every request).
        setUser(null);
        setStatus('anonymous');
      },
    );
    return () => {
      cancelled = true;
    };
  }, []);

  const login = useCallback(async (email: string, password: string) => {
    const r = await api<{ user: User }>('/api/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify({ email, password }),
    });
    setUser(r.user);
    setStatus('authenticated');
  }, []);

  const register = useCallback(
    async (email: string, password: string) => {
      await api('/api/v1/auth/register', { method: 'POST', body: JSON.stringify({ email, password }) });
      await login(email, password);
    },
    [login],
  );

  const logout = useCallback(async () => {
    try {
      await api('/api/v1/auth/logout', { method: 'POST' });
    } finally {
      setUser(null);
      setStatus('anonymous');
    }
  }, []);

  const value = useMemo(() => ({ status, user, login, register, logout }), [status, user, login, register, logout]);
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionValue {
  const ctx = useContext(SessionContext);
  if (!ctx) throw new Error('useSession must be used inside SessionProvider');
  return ctx;
}

export function fieldErrors(error: unknown): Record<string, string> {
  if (error instanceof ApiError && error.details && typeof error.details === 'object') {
    return error.details as Record<string, string>;
  }
  return {};
}
