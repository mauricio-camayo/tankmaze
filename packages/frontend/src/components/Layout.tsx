import { Link, useNavigate } from 'react-router-dom';
import { signOut } from '../services/auth';
import { useAuthStore } from '../store/authStore';

interface LayoutProps {
  children: React.ReactNode;
}

export default function Layout({ children }: LayoutProps) {
  const { user, setUser } = useAuthStore();
  const navigate = useNavigate();

  async function handleSignOut() {
    await signOut();
    setUser(null);
    navigate('/login');
  }

  return (
    <div style={{ minHeight: '100vh', background: '#0f0f1a', color: '#e2e8f0' }}>
      <nav style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '0 24px', height: 56,
        background: '#1a1a2e', borderBottom: '1px solid #2d2d4e',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 24 }}>
          <Link to="/dashboard" style={{ color: '#7c6af7', fontWeight: 700, fontSize: 18, textDecoration: 'none' }}>
            TankMaze
          </Link>
          <Link to="/leaderboard" style={navLinkStyle}>Leaderboard</Link>
          {user?.isAdmin && <Link to="/admin/users" style={{ ...navLinkStyle, color: '#f59e0b' }}>Admin</Link>}
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          {user && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
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
            </div>
          )}
          <button onClick={handleSignOut} style={ghostButtonStyle}>Sign out</button>
        </div>
      </nav>
      <main style={{ maxWidth: 960, margin: '0 auto', padding: '32px 24px' }}>
        {children}
      </main>
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
