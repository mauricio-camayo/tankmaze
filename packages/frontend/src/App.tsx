import { lazy, Suspense, useEffect } from 'react';
import { createBrowserRouter, RouterProvider, Navigate, useLocation, Outlet } from 'react-router-dom';
import { Hub } from 'aws-amplify/utils';
import { getAuthUser, getUserProfile } from './services/auth';
import { useAuthStore } from './store/authStore';
import Landing from './pages/Landing';
import Dashboard from './pages/Dashboard';
import TankDetail from './pages/TankDetail';
import TankEditor from './pages/TankEditor';
import Leaderboard from './pages/Leaderboard';
import GameDay from './pages/GameDay';
import GameDayList from './pages/GameDayList';
import AdminUsers from './pages/admin/Users';
import AdminTanks from './pages/admin/Tanks';
import AdminAdConfig from './pages/admin/AdConfig';
import Account from './pages/Account';
import PrivacyPolicy from './pages/PrivacyPolicy';

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

function RequireAdmin() {
  const { user, loading } = useAuthStore();
  if (loading) return <div>Loading…</div>;
  if (!user?.isAdmin) return <Navigate to="/dashboard" replace />;
  return <Outlet />;
}

function UpgradePlaceholder() {
  return (
    <div style={{ minHeight: '100vh', background: '#0f0f1a', color: '#e2e8f0', display: 'flex', alignItems: 'center', justifyContent: 'center', flexDirection: 'column', gap: 16 }}>
      <h1 style={{ fontSize: 28, fontWeight: 700, margin: 0 }}>Upgrade Coming Soon</h1>
      <p style={{ color: '#64748b', margin: 0 }}>Paid plans are not yet available. Check back later.</p>
    </div>
  );
}

function LandingRoute() {
  const { user, loading } = useAuthStore();
  if (loading) return null;
  if (user) return <Navigate to="/dashboard" replace />;
  return <Landing />;
}

const router = createBrowserRouter([
  { path: '/', element: <LandingRoute /> },
  { path: '/login', element: <LandingRoute /> },
  { path: '/privacy', element: <PrivacyPolicy /> },
  { path: '/watch', element: <Suspense fallback={null}><Watch /></Suspense> },
  { path: '/leaderboard', element: <Leaderboard /> },
  { path: '/gamedays', element: <GameDayList /> },
  { path: '/gameday/:gameDayId', element: <GameDay /> },
  {
    element: <RequireAuth />,
    children: [
      { path: '/dashboard', element: <Dashboard /> },
      { path: '/tanks/new/edit', element: <TankEditor /> },
      { path: '/tanks/:tankId', element: <TankDetail /> },
      { path: '/tanks/:tankId/edit', element: <TankEditor /> },
      { path: '/account', element: <Account /> },
      { path: '/upgrade', element: <UpgradePlaceholder /> },
    ],
  },
  {
    element: <RequireAdmin />,
    children: [
      { path: '/admin/users', element: <AdminUsers /> },
      { path: '/admin/tanks', element: <AdminTanks /> },
      { path: '/admin/ads', element: <AdminAdConfig /> },
    ],
  },
  { path: '*', element: <Navigate to="/" replace /> },
]);

export default function App() {
  const { setUser, setLoading } = useAuthStore();

  useEffect(() => {
    const LOCAL_DEV = import.meta.env.VITE_LOCAL_DEV === 'true';

    const checkUser = () =>
      getAuthUser().then(async (u) => {
        if (u) {
          const profile = await getUserProfile();
          setUser({ userId: profile.sub ?? u.userId, username: u.username, name: profile.name, picture: profile.picture, isAdmin: profile.isAdmin });
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

  return <RouterProvider router={router} />;
}
