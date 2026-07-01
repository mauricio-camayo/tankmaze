import { useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { signIn, signInWithGoogle } from '../services/auth';
import { useAuthStore } from '../store/authStore';

export default function Login() {
  const navigate = useNavigate();
  const setUser = useAuthStore((s) => s.setUser);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  async function handleGoogleSignIn() {
    if (import.meta.env.VITE_LOCAL_DEV === 'true') {
      setUser({ userId: 'local-user', username: 'local' });
      navigate('/dashboard');
      return;
    }
    setLoading(true);
    try {
      await signInWithGoogle();
      // signInWithRedirect navigates away — only reached on error
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Google sign-in failed');
      setLoading(false);
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      const result = await signIn(username, password);
      if (result.isSignedIn) {
        setUser({ userId: '', username });
        navigate('/dashboard');
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sign in failed');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div style={{ minHeight: '100vh', background: '#0f0f1a', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '24px 16px' }}>
      <div style={{ width: '100%', maxWidth: 400 }}>

        {/* Product description */}
        <div style={{ textAlign: 'center', marginBottom: 40 }}>
          <h1 style={{ fontSize: 32, fontWeight: 800, color: '#e2e8f0', margin: '0 0 8px', letterSpacing: '-0.5px' }}>
            Tank<span style={{ color: '#7c6af7' }}>Maze</span>
          </h1>
          <p style={{ color: '#64748b', fontSize: 15, margin: '0 0 20px', lineHeight: 1.6 }}>
            Program a tank in Go. Compete in scheduled Game Days.<br />Climb the global leaderboard.
          </p>
          <div style={{ display: 'flex', justifyContent: 'center', gap: 16, fontSize: 13 }}>
            <Link to="/leaderboard" style={{ color: '#7c6af7', textDecoration: 'none' }}>Leaderboard →</Link>
            <Link to="/gamedays"    style={{ color: '#7c6af7', textDecoration: 'none' }}>Game Days →</Link>
          </div>
        </div>

        {/* Sign-in card */}
        <div style={{ background: '#1a1a2e', border: '1px solid #2d2d4e', borderRadius: 12, padding: '28px 24px' }}>
          <button
            type="button"
            onClick={handleGoogleSignIn}
            disabled={loading}
            style={{
              width: '100%', padding: '10px 0', marginBottom: 4,
              background: '#fff', color: '#1a1a1a', border: 'none',
              borderRadius: 8, cursor: loading ? 'not-allowed' : 'pointer',
              fontWeight: 600, fontSize: 14, display: 'flex', alignItems: 'center',
              justifyContent: 'center', gap: 8,
            }}
          >
            <svg width="18" height="18" viewBox="0 0 48 48">
              <path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z"/>
              <path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z"/>
              <path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z"/>
              <path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.18 1.48-4.97 2.31-8.16 2.31-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z"/>
            </svg>
            Sign in with Google
          </button>

          <div style={{ display: 'flex', alignItems: 'center', margin: '16px 0' }}>
            <hr style={{ flex: 1, border: 'none', borderTop: '1px solid #2d2d4e' }} />
            <span style={{ padding: '0 10px', color: '#475569', fontSize: 12 }}>or</span>
            <hr style={{ flex: 1, border: 'none', borderTop: '1px solid #2d2d4e' }} />
          </div>

          <form onSubmit={handleSubmit}>
            <div style={{ marginBottom: 12 }}>
              <label style={{ fontSize: 13, color: '#94a3b8', display: 'block', marginBottom: 4 }}>Username</label>
              <input
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoFocus
                required
                style={{ width: '100%', background: '#0f0f1a', border: '1px solid #2d2d4e', borderRadius: 6, color: '#e2e8f0', padding: '8px 10px', fontSize: 14, boxSizing: 'border-box' }}
              />
            </div>
            <div style={{ marginBottom: 16 }}>
              <label style={{ fontSize: 13, color: '#94a3b8', display: 'block', marginBottom: 4 }}>Password</label>
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                style={{ width: '100%', background: '#0f0f1a', border: '1px solid #2d2d4e', borderRadius: 6, color: '#e2e8f0', padding: '8px 10px', fontSize: 14, boxSizing: 'border-box' }}
              />
            </div>
            {error && <p style={{ color: '#f87171', fontSize: 13, margin: '0 0 12px' }}>{error}</p>}
            <button
              type="submit"
              disabled={loading}
              style={{ width: '100%', background: '#7c6af7', border: 'none', color: '#fff', borderRadius: 8, padding: '10px 0', fontWeight: 600, fontSize: 14, cursor: loading ? 'not-allowed' : 'pointer', opacity: loading ? 0.7 : 1 }}
            >
              {loading ? 'Signing in…' : 'Sign in'}
            </button>
          </form>

          <p style={{ marginTop: 20, textAlign: 'center', fontSize: 12, color: '#475569', margin: '20px 0 0' }}>
            By signing in you agree to our{' '}
            <Link to="/privacy" style={{ color: '#7c6af7' }}>Privacy Policy</Link>.
          </p>
        </div>
      </div>
    </div>
  );
}
