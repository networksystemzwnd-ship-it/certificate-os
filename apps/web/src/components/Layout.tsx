import { NavLink, Outlet, useNavigate } from 'react-router-dom';
import { useSession } from '../features/auth/session';
import { Brand } from './Brand';

const link = ({ isActive }: { isActive: boolean }) => (isActive ? 'nav-link active' : 'nav-link');

export function Layout() {
  const { status, user, logout } = useSession();
  const navigate = useNavigate();
  const isPlatformStaff = user?.permissions.some((p) => p.startsWith('ops.')) ?? false;
  return (
    <>
      <a href="#main" className="skip-link">
        Skip to content
      </a>
      <header className="app-header">
        <Brand />
        <nav aria-label="Primary">
          <NavLink to="/" end className={link}>
            Home
          </NavLink>
          {status === 'anonymous' && (
            <NavLink to="/login" className={link}>
              Log in
            </NavLink>
          )}
          {status !== 'anonymous' && (
            <NavLink to="/app" className={link}>
              Workspace
            </NavLink>
          )}
          {isPlatformStaff && (
            <NavLink to="/ops" className={link}>
              Operations
            </NavLink>
          )}
        </nav>
        {user && (
          <div className="session">
            <span className="who">
              <span className="avatar" aria-hidden="true">
                {user.email.slice(0, 1).toUpperCase()}
              </span>
              <span>{user.email}</span>
            </span>
            <button
              type="button"
              onClick={async () => {
                await logout();
                navigate('/login');
              }}
            >
              Log out
            </button>
          </div>
        )}
      </header>
      <main id="main" tabIndex={-1}>
        <Outlet />
      </main>
    </>
  );
}
