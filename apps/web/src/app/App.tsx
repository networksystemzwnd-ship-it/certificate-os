import { Route, Routes } from 'react-router-dom';
import { Layout } from '../components/Layout';
import { SessionProvider } from '../features/auth/session';
import { AcceptInvite } from '../features/staff/AcceptInvite';
import { VerifyPage } from '../features/verification/Verify';
import { Apply, Home, Login, NotFoundPage, Operations, Register, Workspace } from '../routes/pages';

export function App() {
  return (
    <SessionProvider>
      <Routes>
        {/* Public: the page behind a certificate's QR code. Outside the workspace layout, no login needed. */}
        <Route path="verify/:publicId" element={<VerifyPage />} />
        <Route element={<Layout />}>
          <Route index element={<Home />} />
          <Route path="login" element={<Login />} />
          <Route path="register" element={<Register />} />
          <Route path="app/*" element={<Workspace />} />
          <Route path="apply" element={<Apply />} />
          <Route path="accept-invite" element={<AcceptInvite />} />
          <Route path="ops/*" element={<Operations />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </SessionProvider>
  );
}
