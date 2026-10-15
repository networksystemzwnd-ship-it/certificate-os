import { Route, Routes } from 'react-router-dom';
import { Layout } from '../components/Layout';
import { SessionProvider } from '../features/auth/session';
import { Home, Login, NotFoundPage, Operations, Register, Workspace } from '../routes/pages';

export function App() {
  return (
    <SessionProvider>
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<Home />} />
          <Route path="login" element={<Login />} />
          <Route path="register" element={<Register />} />
          <Route path="app/*" element={<Workspace />} />
          <Route path="ops/*" element={<Operations />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </SessionProvider>
  );
}
