import { NavLink, Outlet } from 'react-router-dom';

const link = ({ isActive }: { isActive: boolean }) => (isActive ? 'nav-link active' : 'nav-link');

export function Layout() {
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
          <NavLink to="/login" className={link}>
            Log in
          </NavLink>
          <NavLink to="/app" className={link}>
            Workspace
          </NavLink>
          <NavLink to="/ops" className={link}>
            Operations
          </NavLink>
        </nav>
      </header>
      <main id="main" tabIndex={-1}>
        <Outlet />
      </main>
    </>
  );
}
