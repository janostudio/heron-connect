import { Routes, Route, Navigate, useLocation } from 'react-router-dom';
import { useAuthStore } from '@/store/auth';
import { LOGIN_PATH, returnPath } from '@/lib/returnTo';
import ProtectedRoute from '@/components/ProtectedRoute';
import Layout from '@/components/Layout/Layout';
import Login from '@/pages/Login';
import Dashboard from '@/pages/Dashboard';
import ProjectList from '@/pages/Projects/ProjectList';
import ProjectDetail from '@/pages/Projects/ProjectDetail';
import ProjectOverview from '@/pages/Projects/ProjectOverview';
import ChatList from '@/pages/Chat/ChatList';
import ChatView from '@/pages/Chat/ChatView';
import SessionList from '@/pages/Sessions/SessionList';
import CronList from '@/pages/Cron/CronList';
import SystemConfig from '@/pages/System/Config';
import ProviderList from '@/pages/Providers/ProviderList';
import SkillList from '@/pages/Skills/SkillList';
import { ReportsList, ReportPreview } from '@/pages/Reports';

// Guarding a route loses the user's position: without recording it, a login
// always ended on the dashboard, so the conversation that was open before the
// redirect had to be found again by hand. The target travels as `?next=` (which
// survives a login-page reload) and is remembered in storage as a fallback.
export default function App() {
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);
  const location = useLocation();
  // Only meaningful on the login route: where to resume after authenticating.
  const resumeAt = isAuthenticated ? returnPath(location.search, location.state) : '/';

  return (
    <Routes>
      <Route path={LOGIN_PATH} element={<>{isAuthenticated ? <Navigate to={resumeAt} replace /> : <Login />}</>} />
      <Route element={<ProtectedRoute><Layout /></ProtectedRoute>}>
        <Route index element={<Dashboard />} />
        <Route path="projects" element={<ProjectList />} />
        <Route path="projects/:name" element={<ProjectDetail />} />
        <Route path="projects/:name/overview" element={<ProjectOverview />} />
        <Route path="reports" element={<ReportsList />} />
        <Route path="reports/preview" element={<ReportPreview />} />
        <Route path="providers" element={<ProviderList />} />
        <Route path="skills" element={<SkillList />} />
        <Route path="chat" element={<ChatList />} />
        <Route path="chat/:name" element={<ChatView />} />
        <Route path="chat/:name/:id" element={<ChatView />} />
        <Route path="sessions" element={<SessionList />} />
        <Route path="cron" element={<CronList />} />
        <Route path="system" element={<SystemConfig />} />
      </Route>
    </Routes>
  );
}
