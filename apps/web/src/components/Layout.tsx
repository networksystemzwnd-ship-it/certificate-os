import { NavLink, Outlet, useNavigate } from 'react-router-dom';
import { useSession } from '../features/auth/session';

const link = ({ isActive }: { isActive: boolean }) => (isActive ? 'nav-link active' : 'nav-link');

export function Layout() {
  const { status, user, logout } = useSession();
  const navigate = useNavigate();
  return (
    <>
      <a href="#main" className="skip-link">
        Skip to content
      </a>
      <header className="app-header">
        <strong>CertificateOS</strong>
        <nav aria-label="Primary">
          <NavLink to="/" end className={link}>
            Home
          </NavLink>
          {status === 'anonymous' && (
            <NavLink to="/login" className={link}>
              Log in
            </NavLink>
          )}
          <NavLink to="/app" className={link}>
            Workspace
          </NavLink>
          <NavLink to="/ops" className={link}>
            Operations
          </NavLink>
        </nav>
        {user && (
          <span className="session">
            {user.email}{' '}
            <button
              type="button"
              onClick={async () => {
                await logout();
                navigate('/login');
              }}
            >
              Log out
            </button>
          </span>
        )}
      </header>
      <main id="main" tabIndex={-1}>
        <Outlet />
      </main>
    </>
  );
}
