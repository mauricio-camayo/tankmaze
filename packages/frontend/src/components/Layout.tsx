import { useState, useEffect } from 'react';
import { Link, useNavigate, useLocation } from 'react-router-dom';
import { signOut } from '../services/auth';
import { useAuthStore } from '../store/authStore';
import { formatNavClock } from '../utils/time';
import AdSlots from './AdSlots';

interface LayoutProps {
  children: React.ReactNode;
}

export default function Layout({ children }: LayoutProps) {
  const { user, setUser } = useAuthStore();
  const navigate = useNavigate();
  const location = useLocation();
  const [clock, setClock] = useState(() => formatNavClock(new Date()));
  const [menuOpen, setMenuOpen] = useState(false);

  useEffect(() => {
    const id = setInterval(() => setClock(formatNavClock(new Date())), 60_000);
    return () => clearInterval(id);
  }, []);

  // Close drawer on navigation
  useEffect(() => { setMenuOpen(false); }, [location.pathname]);

  async function handleSignOut() {
    await signOut();
    setUser(null);
    navigate('/login');
  }

  const isAdminRoute = location.pathname.startsWith('/admin');
  const isLoginRoute = location.pathname === '/login';
  const showAds = !isAdminRoute && !isLoginRoute;

  return (
    <div style={{ minHeight: '100vh', background: '#0f0f1a', color: '#e2e8f0', position: 'relative' }}>
      <nav className="tm-nav" style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '0 24px', height: 56, position: 'relative',
        background: '#1a1a2e', borderBottom: '1px solid #2d2d4e',
      }}>
        {/* Hamburger button (hidden on desktop via CSS); sits left of the logo on mobile/tablet */}
        <button
          className="tm-hamburger"
          onClick={() => setMenuOpen((o) => !o)}
          aria-label="Menu"
          aria-expanded={menuOpen}
          style={{ fontSize: 20 }}
        >
          {menuOpen ? '✕' : '☰'}
        </button>

        {/* Logo — always visible */}
        <Link to="/dashboard" style={{ color: '#7c6af7', fontWeight: 700, fontSize: 18, textDecoration: 'none', flexShrink: 0 }}>
          TankMaze
        </Link>

        {/* Desktop nav links (hidden on mobile/tablet via CSS) */}
        <div className="tm-nav-links" style={{ alignItems: 'center', gap: 24 }}>
          <Link to="/leaderboard" style={navLinkStyle}>Leaderboard</Link>
          <Link to="/gamedays" style={navLinkStyle}>Game Days</Link>
          {user?.isAdmin && (
            <>
              <Link to="/admin/users" style={{ ...navLinkStyle, color: '#f59e0b' }}>Users</Link>
              <Link to="/admin/ads" style={{ ...navLinkStyle, color: '#f59e0b' }}>Ads</Link>
            </>
          )}
        </div>

        <span className="tm-nav-clock" style={{ color: '#7c6af7', fontSize: 13, fontWeight: 600 }}>
          {clock}
        </span>

        <div className="tm-nav-right-pair" style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          {user && (
            <Link to="/account" style={{ display: 'flex', alignItems: 'center', gap: 8, textDecoration: 'none' }}>
              {user.picture ? (
                <img
                  src={user.picture}
                  alt=""
                  referrerPolicy="no-referrer"
                  style={{ width: 30, height: 30, borderRadius: '50%', objectFit: 'cover' }}
                />
              ) : (
                <div style={{
                  width: 30, height: 30, borderRadius: '50%',
                  background: '#7c6af7', display: 'flex', alignItems: 'center',
                  justifyContent: 'center', fontSize: 13, fontWeight: 700, color: '#fff', flexShrink: 0,
                }}>
                  {(user.name ?? user.username).charAt(0).toUpperCase()}
                </div>
              )}
              <span style={{ color: '#94a3b8', fontSize: 14 }}>
                {user.name ?? user.username}
              </span>
            </Link>
          )}
          <button onClick={handleSignOut} style={ghostButtonStyle}>Sign out</button>
        </div>
      </nav>

      {/* Mobile/tablet slide-down drawer */}
      <div className={`tm-nav-drawer${menuOpen ? ' open' : ''}`}>
        {[
          { to: '/dashboard', label: 'Dashboard', color: undefined },
          { to: '/leaderboard', label: 'Leaderboard', color: undefined },
          { to: '/gamedays', label: 'Game Days', color: undefined },
          { to: '/account', label: 'Account', color: undefined },
          ...(user?.isAdmin ? [
            { to: '/admin/users', label: 'Admin: Users', color: '#f59e0b' },
            { to: '/admin/ads', label: 'Admin: Ads', color: '#f59e0b' },
          ] : []),
        ].map(({ to, label, color }) => (
          <Link
            key={to}
            to={to}
            style={{ ...navLinkStyle, ...(color ? { color } : {}), fontSize: 16, minHeight: 44, display: 'flex', alignItems: 'center' }}
          >
            {label}
          </Link>
        ))}
      </div>

      {showAds && <AdSlots position="top" />}

      <div style={{ position: 'relative' }}>
        <main style={{ maxWidth: 960, margin: '0 auto', padding: '32px 24px' }}>
          {children}
        </main>
        {showAds && <AdSlots position="right" />}
      </div>

      {showAds && <AdSlots position="bottom" />}

      <footer style={{
        borderTop: '1px solid #1e1e35', padding: '12px 24px',
        display: 'flex', justifyContent: 'center', gap: 24,
        fontSize: 12, color: '#475569',
      }}>
        <Link to="/privacy" style={{ color: '#475569', textDecoration: 'none' }}>Privacy Policy</Link>
        <span>© {new Date().getFullYear()} TankMaze</span>
      </footer>
    </div>
  );
}

const navLinkStyle: React.CSSProperties = {
  color: '#94a3b8', textDecoration: 'none', fontSize: 14,
};

export const ghostButtonStyle: React.CSSProperties = {
  background: 'none', border: '1px solid #2d2d4e', color: '#94a3b8',
  padding: '4px 12px', borderRadius: 6, cursor: 'pointer', fontSize: 14,
};

export const primaryButtonStyle: React.CSSProperties = {
  background: '#7c6af7', border: 'none', color: '#fff',
  padding: '8px 16px', borderRadius: 6, cursor: 'pointer', fontSize: 14, fontWeight: 600,
};

export const cardStyle: React.CSSProperties = {
  background: '#1a1a2e', border: '1px solid #2d2d4e', borderRadius: 10,
  padding: '20px 24px',
};
