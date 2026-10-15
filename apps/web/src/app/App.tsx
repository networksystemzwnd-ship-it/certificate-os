import { Route, Routes } from 'react-router-dom';
import { Layout } from '../components/Layout';
import { Home, Login, NotFoundPage, Operations, Workspace } from '../routes/pages';

export function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<Home />} />
        <Route path="login" element={<Login />} />
        <Route path="app/*" element={<Workspace />} />
        <Route path="ops/*" element={<Operations />} />
        <Route path="*" element={<NotFoundPage />} />
      </Route>
    </Routes>
  );
}
