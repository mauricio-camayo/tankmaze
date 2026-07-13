import { useState, useEffect } from 'react';
import { Link, useNavigate, useLocation } from 'react-router-dom';
import { signOut } from '../services/auth';
import { listFriends } from '../services/api';
import { isUnread } from '../utils/chatUnread';
import { useAuthStore } from '../store/authStore';
import { formatNavClock } from '../utils/time';
import AdSlots from './AdSlots';

interface LayoutProps {
  children: React.ReactNode;
}

const CHAT_UNREAD_POLL_MS = 30_000;

export default function Layout({ children }: LayoutProps) {
  const { user, setUser } = useAuthStore();
  const navigate = useNavigate();
  const location = useLocation();
  const [clock, setClock] = useState(() => formatNavClock(new Date()));
  const [menuOpen, setMenuOpen] = useState(false);
  const [hasUnreadChat, setHasUnreadChat] = useState(false);

  useEffect(() => {
    const id = setInterval(() => setClock(formatNavClock(new Date())), 60_000);
    return () => clearInterval(id);
  }, []);

  // Chat unread badge (item 223 Part 2) — low-frequency poll, mirrors the
  // clock's interval pattern above. No server-side read receipts, so this
  // just re-derives isUnread() per friend on every tick.
  useEffect(() => {
    if (!user) return;
    let cancelled = false;
    function check() {
      listFriends()
        .then((data) => { if (!cancelled) setHasUnreadChat(data.friends.some(isUnread)); })
        .catch(() => { /* leave as-is — non-critical */ });
    }
    check();
    const id = setInterval(check, CHAT_UNREAD_POLL_MS);
    return () => { cancelled = true; clearInterval(id); };
  }, [user]);

  // Close drawer on navigation
  useEffect(() => { setMenuOpen(false); }, [location.pathname]);

  async function handleSignOut() {
    await signOut();
    setUser(null);
    // Belt-and-suspenders alongside the per-user draft-key scoping in
    // TankEditor.tsx (item 222): also wipe any leftover tankmaze-* draft
    // entries so nothing from this session lingers for the next login on
    // this browser.
    for (let i = localStorage.length - 1; i >= 0; i--) {
      const key = localStorage.key(i);
      if (key?.startsWith('tankmaze-')) localStorage.removeItem(key);
    }
    navigate('/login');
  }

  const isAdminRoute = location.pathname.startsWith('/admin');
  const isLoginRoute = location.pathname === '/login';
  const showAds = !isAdminRoute && !isLoginRoute;

  return (
    <div style={{
      minHeight: '100vh', color: 'var(--bp-line)', position: 'relative',
      backgroundColor: 'var(--bp-bg)',
      backgroundImage: 'linear-gradient(var(--bp-grid) 1px, transparent 1px), linear-gradient(90deg, var(--bp-grid) 1px, transparent 1px)',
      backgroundSize: '28px 28px',
    }}>
      <nav className="tm-nav" style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '0 24px', height: 56, position: 'relative',
        background: 'var(--bp-panel)', borderBottom: '1px solid var(--bp-hairline)',
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
        <Link to="/dashboard" style={{ display: 'flex', alignItems: 'center', gap: 9, fontWeight: 700, fontSize: 18, textDecoration: 'none', flexShrink: 0 }}>
          <img src="/avatar.png" alt="" width={28} height={28} style={{ display: 'block' }} />
          <span style={{ fontFamily: 'var(--font-display)', textTransform: 'uppercase', letterSpacing: '0.02em', color: 'var(--bp-line)' }}>
            Tank<span style={{ color: 'var(--bp-readout)' }}>Maze</span>
          </span>
          <span style={{ color: 'var(--bp-steel-faint)', fontSize: 10, fontWeight: 500, fontFamily: 'var(--font-mono)' }}>v{import.meta.env.VITE_APP_VERSION}</span>
        </Link>

        {/* Desktop nav links (hidden on mobile/tablet via CSS) */}
        <div className="tm-nav-links" style={{ alignItems: 'center', gap: 24 }}>
          <Link to="/leaderboard" className="tm-navlink" style={navLinkStyle}>Leaderboard</Link>
          <Link to="/gamedays" className="tm-navlink" style={navLinkStyle}>Game Days</Link>
          {user && (
            <Link to="/friends" className="tm-navlink" style={{ ...navLinkStyle, position: 'relative' }}>
              Friends
              {hasUnreadChat && (
                <span style={{
                  position: 'absolute', top: -2, right: -8, width: 6, height: 6,
                  borderRadius: '50%', background: 'var(--bp-hazard)',
                }} />
              )}
            </Link>
          )}
          {user?.isAdmin && (
            <Link to="/admin" className="tm-navlink" style={{ ...navLinkStyle, color: 'var(--bp-hazard)' }}>Admin</Link>
          )}
        </div>

        <span className="tm-nav-clock" style={{ color: 'var(--bp-readout)', fontSize: 12.5, fontWeight: 600, fontFamily: 'var(--font-mono)' }}>
          {clock}
        </span>

        <div className="tm-nav-right-pair" style={{ display: 'flex', alignItems: 'center', gap: 12, minWidth: 0 }}>
          {user && (
            <Link to="/account" title={user.name ?? user.username} style={{ display: 'flex', alignItems: 'center', gap: 8, textDecoration: 'none', minWidth: 0 }}>
              {user.picture ? (
                <img
                  src={user.picture}
                  alt=""
                  referrerPolicy="no-referrer"
                  style={{ width: 30, height: 30, borderRadius: '50%', objectFit: 'cover', border: '1px solid var(--bp-hairline)', flexShrink: 0 }}
                />
              ) : (
                <div style={{
                  width: 30, height: 30, borderRadius: '50%',
                  background: 'var(--bp-hazard)', display: 'flex', alignItems: 'center',
                  justifyContent: 'center', fontSize: 13, fontWeight: 700, color: '#0a2135', flexShrink: 0,
                }}>
                  {(user.name ?? user.username).charAt(0).toUpperCase()}
                </div>
              )}
              {/* Truncated rather than left to overflow/wrap the whole
                  right-pair — the full name is still available via the
                  Link's title tooltip above (item 230). */}
              <span className="tm-nav-username" style={{ color: 'var(--bp-steel)', fontSize: 14 }}>
                {user.name ?? user.username}
              </span>
            </Link>
          )}
          {/* Desktop-only; moves into the drawer below at <1024px (item 230) */}
          <button onClick={handleSignOut} className="tm-signout-desktop" style={ghostButtonStyle}>Sign out</button>
        </div>
      </nav>

      {/* Mobile/tablet slide-down drawer */}
      <div className={`tm-nav-drawer${menuOpen ? ' open' : ''}`}>
        {[
          { to: '/dashboard', label: 'Dashboard', color: undefined },
          { to: '/leaderboard', label: 'Leaderboard', color: undefined },
          { to: '/gamedays', label: 'Game Days', color: undefined },
          { to: '/friends', label: 'Friends', color: undefined },
          { to: '/account', label: 'Account', color: undefined },
          ...(user?.isAdmin ? [
            { to: '/admin', label: 'Admin', color: 'var(--bp-hazard)' },
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
        {user && (
          <button
            onClick={handleSignOut}
            style={{
              ...navLinkStyle, fontSize: 16, minHeight: 44, display: 'flex', alignItems: 'center',
              background: 'none', border: 'none', padding: 0, width: '100%', textAlign: 'left', cursor: 'pointer',
            }}
          >
            Sign out
          </button>
        )}
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
        borderTop: '1px solid var(--bp-hairline)', padding: '12px 24px',
        display: 'flex', justifyContent: 'center', gap: 24,
        fontSize: 11, color: 'var(--bp-steel-faint)', fontFamily: 'var(--font-mono)', letterSpacing: '0.04em',
      }}>
        <Link to="/privacy" style={{ color: 'var(--bp-steel-faint)', textDecoration: 'none' }}>PRIVACY POLICY</Link>
        <span>© {new Date().getFullYear()} TANKMAZE</span>
      </footer>
    </div>
  );
}

const navLinkStyle: React.CSSProperties = {
  color: 'var(--bp-steel)', textDecoration: 'none', fontSize: 12.5,
  fontFamily: 'var(--font-mono)', textTransform: 'uppercase', letterSpacing: '0.06em',
};

export const ghostButtonStyle: React.CSSProperties = {
  background: 'none', border: '1px solid var(--bp-hairline)', color: 'var(--bp-steel)',
  padding: '5px 12px', borderRadius: 0, cursor: 'pointer', fontSize: 13, fontFamily: 'var(--font-mono)',
  textTransform: 'uppercase', letterSpacing: '0.04em',
};

export const primaryButtonStyle: React.CSSProperties = {
  background: 'var(--bp-hazard)', border: '1px solid var(--bp-hazard)', color: '#0a2135',
  padding: '8px 16px', borderRadius: 0, cursor: 'pointer', fontSize: 14, fontWeight: 700,
  fontFamily: 'var(--font-body)',
};

export const cardStyle: React.CSSProperties = {
  background: 'var(--bp-panel)', border: '1px solid var(--bp-hairline)', borderRadius: 0,
  padding: '20px 24px',
};
