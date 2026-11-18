import type { ReactNode } from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { useAuthStore } from '@/store/auth';
import { LOGIN_PATH, rememberReturnPath } from '@/lib/returnTo';

// Guarding a route loses the user's position: without recording it, a login
// always ended on the dashboard, so the conversation that was open before the
// redirect had to be found again by hand. The target travels as `?next=` (which
// survives a login-page reload) and is remembered in storage as a fallback.
export default function ProtectedRoute({ children }: { children: ReactNode }) {
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);
  const location = useLocation();
  if (!isAuthenticated) {
    const from = location.pathname + location.search;
    rememberReturnPath(from);
    return <Navigate to={`${LOGIN_PATH}?next=${encodeURIComponent(from)}`} replace />;
  }
  return <>{children}</>;
}
