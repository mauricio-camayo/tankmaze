import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
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
    <div style={{ maxWidth: 360, margin: '80px auto', padding: '0 16px' }}>
      <h1>TankMaze</h1>

      <button
        type="button"
        onClick={handleGoogleSignIn}
        disabled={loading}
        style={{ width: '100%', padding: '10px 0', marginBottom: 4 }}
      >
        Sign in with Google
      </button>

      <div style={{ display: 'flex', alignItems: 'center', margin: '16px 0' }}>
        <hr style={{ flex: 1, border: 'none', borderTop: '1px solid #ccc' }} />
        <span style={{ padding: '0 10px', color: '#888', fontSize: 12 }}>or</span>
        <hr style={{ flex: 1, border: 'none', borderTop: '1px solid #ccc' }} />
      </div>

      <form onSubmit={handleSubmit}>
        <div style={{ marginBottom: 12 }}>
          <label>Username</label>
          <br />
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoFocus
            required
            style={{ width: '100%', marginTop: 4 }}
          />
        </div>
        <div style={{ marginBottom: 12 }}>
          <label>Password</label>
          <br />
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            style={{ width: '100%', marginTop: 4 }}
          />
        </div>
        {error && <p style={{ color: 'red' }}>{error}</p>}
        <button type="submit" disabled={loading} style={{ width: '100%' }}>
          {loading ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  );
}
