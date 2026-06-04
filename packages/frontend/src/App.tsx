import { lazy, Suspense, useEffect } from 'react';
import { BrowserRouter, Routes, Route, Navigate, useLocation, Outlet } from 'react-router-dom';
import { Hub } from 'aws-amplify/utils';
import { getAuthUser, getUserProfile } from './services/auth';
import { useAuthStore } from './store/authStore';
import Login from './pages/Login';
import Dashboard from './pages/Dashboard';
import TankDetail from './pages/TankDetail';
import TankEditor from './pages/TankEditor';
import Leaderboard from './pages/Leaderboard';
import GameDay from './pages/GameDay';

// Watch imports Phaser (~1 MB) — keep it in a separate lazy chunk
const Watch = lazy(() => import('./pages/Watch'));

// Global reset — keep out of component to avoid re-injection
const globalStyle = document.createElement('style');
globalStyle.textContent = `
  *, *::before, *::after { box-sizing: border-box; }
  body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; }
  a { color: inherit; }
`;
document.head.appendChild(globalStyle);

function RequireAuth() {
  const { user, loading } = useAuthStore();
  const location = useLocation();

  if (loading) return <div>Loading…</div>;
  if (!user) return <Navigate to="/login" state={{ from: location }} replace />;
  return <Outlet />;
}

function AppRoutes() {
  const { user, loading } = useAuthStore();

  return (
    <Routes>
      <Route path="/login" element={loading ? null : user ? <Navigate to="/dashboard" replace /> : <Login />} />
      <Route path="/watch" element={<Suspense fallback={null}><Watch /></Suspense>} />
      <Route path="/leaderboard" element={<Leaderboard />} />
      <Route path="/gameday/:gameDayId" element={<GameDay />} />
      <Route element={<RequireAuth />}>
        <Route path="/dashboard" element={<Dashboard />} />
        <Route path="/tanks/new/edit" element={<TankEditor />} />
        <Route path="/tanks/:tankId" element={<TankDetail />} />
        <Route path="/tanks/:tankId/edit" element={<TankEditor />} />
      </Route>
      <Route path="*" element={<Navigate to="/dashboard" replace />} />
    </Routes>
  );
}

export default function App() {
  const { setUser, setLoading } = useAuthStore();

  useEffect(() => {
    const LOCAL_DEV = import.meta.env.VITE_LOCAL_DEV === 'true';

    const checkUser = () =>
      getAuthUser().then(async (u) => {
        if (u) {
          const profile = await getUserProfile();
          setUser({ userId: profile.sub ?? u.userId, username: u.username, name: profile.name, picture: profile.picture });
        } else {
          setUser(null);
        }
        setLoading(false);
      });

    checkUser();

    if (LOCAL_DEV) return;

    // Re-check session after Google OAuth redirect completes
    const unsubscribe = Hub.listen('auth', ({ payload }) => {
      if (payload.event === 'signInWithRedirect') checkUser();
    });
    return unsubscribe;
  }, [setUser, setLoading]);

  return (
    <BrowserRouter>
      <AppRoutes />
    </BrowserRouter>
  );
}
