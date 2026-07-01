import { useState, useEffect } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { signIn, signInWithGoogle } from '../services/auth';
import { useAuthStore } from '../store/authStore';

const feature = (emoji: string, title: string, desc: string) => (
  <div key={title} style={{ display: 'flex', gap: 12, alignItems: 'flex-start' }}>
    <span style={{ fontSize: 20, flexShrink: 0, lineHeight: 1.4 }}>{emoji}</span>
    <div>
      <div style={{ color: '#e2e8f0', fontWeight: 600, fontSize: 14, marginBottom: 2 }}>{title}</div>
      <div style={{ color: '#64748b', fontSize: 13, lineHeight: 1.5 }}>{desc}</div>
    </div>
  </div>
);

export default function Login() {
  const navigate = useNavigate();
  const setUser = useAuthStore((s) => s.setUser);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    document.title = 'TankMaze — the strategic tank programming game';
    const meta = document.querySelector('meta[name="description"]') ?? (() => {
      const m = document.createElement('meta');
      m.setAttribute('name', 'description');
      document.head.appendChild(m);
      return m;
    })();
    meta.setAttribute('content', 'TankMaze is a strategic programming game where you write Go code to control an autonomous tank. Compete in tournaments, climb the global leaderboard, and debug your AI with tick-by-tick replays.');
  }, []);

  async function handleGoogleSignIn() {
    if (import.meta.env.VITE_LOCAL_DEV === 'true') {
      setUser({ userId: 'local-user', username: 'local' });
      navigate('/dashboard');
      return;
    }
    setLoading(true);
    try {
      await signInWithGoogle();
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
    <div style={{ minHeight: '100vh', background: '#0f0f1a', color: '#e2e8f0', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '40px 16px' }}>
      <div style={{ width: '100%', maxWidth: 880, display: 'flex', gap: 64, alignItems: 'flex-start', flexWrap: 'wrap', justifyContent: 'center' }}>

        {/* Left — description */}
        <div style={{ flex: '1 1 340px', maxWidth: 440 }}>
          <h1 style={{ fontSize: 36, fontWeight: 800, margin: '0 0 4px', letterSpacing: '-0.5px' }}>
            Tank<span style={{ color: '#7c6af7' }}>Maze</span>
          </h1>
          <p style={{ color: '#7c6af7', fontWeight: 600, fontSize: 13, margin: '0 0 20px', letterSpacing: '0.05em', textTransform: 'uppercase' }}>
            A strategic programming game
          </p>

          <p style={{ color: '#94a3b8', fontSize: 15, lineHeight: 1.7, margin: '0 0 8px' }}>
            Somewhere in the labyrinth, an enemy tank is moving. You don't know where — only that your sensors just spiked and you have one tick to decide: advance, turn, fire, or wait for better data.
          </p>
          <p style={{ color: '#64748b', fontSize: 14, lineHeight: 1.7, margin: '0 0 28px' }}>
            You don't drive a tank. You code one. Write the autonomous brain that drives your tank in Go, submit it, and watch your creation navigate fog-of-war, enemy contact, and the limits of its own hardware — without you.
          </p>

          <div style={{ display: 'flex', flexDirection: 'column', gap: 16, marginBottom: 32 }}>
            {feature('⚙️', 'Write a tank in Go', 'Implement a Tick(sensors) Action function. It runs every 100 ms. Package-level variables are your tank\'s memory.')}
            {feature('📊', 'Allocate stats', 'Distribute 15 points across speed, sensor range, damage, armor, and fire rate. Every build is a trade-off.')}
            {feature('🏆', 'Compete in Game Days', 'Round-robin groups of 8 followed by single-elimination. Placement awards global ranking points.')}
            {feature('🎬', 'Replay and debug', 'Every match recorded tick-by-tick. Replay at any speed, inspect sensor readings, memory, and console output.')}
          </div>

          <div style={{ marginTop: 4 }}>
            <p style={{ color: '#475569', fontSize: 12, margin: '0 0 6px' }}>Open source · free to inspect and fork</p>
            <a
              href="https://github.com/mauricio-camayo/tankmaze"
              target="_blank"
              rel="noopener noreferrer"
              style={{
                display: 'inline-flex', alignItems: 'center', gap: 6,
                color: '#a78bfa', textDecoration: 'none', fontSize: 13, fontWeight: 600,
                border: '1px solid #3d3060', borderRadius: 6, padding: '5px 10px',
                background: 'rgba(124,106,247,0.08)',
              }}
            >
              <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8a8 8 0 005.47 7.59c.4.07.55-.17.55-.38v-1.34C3.73 14.36 3.26 12.8 3.26 12.8c-.36-.91-.88-1.15-.88-1.15-.72-.49.05-.48.05-.48.8.06 1.22.82 1.22.82.71 1.22 1.87.87 2.33.66.07-.52.28-.87.5-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.22 2.2.82A7.68 7.68 0 018 4.07c.68 0 1.36.09 2 .27 1.52-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48v2.19c0 .21.15.46.55.38A8 8 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>
              View on GitHub
            </a>
          </div>
        </div>

        {/* Right — sign-in card */}
        <div style={{ flex: '0 0 320px' }}>
          <div style={{ background: '#1a1a2e', border: '1px solid #2d2d4e', borderRadius: 12, padding: '28px 24px' }}>
            <h2 style={{ margin: '0 0 20px', fontSize: 18, fontWeight: 700, color: '#e2e8f0' }}>Sign in</h2>

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
    </div>
  );
}
