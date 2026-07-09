import { useState, useEffect } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { signIn, signInWithGoogle, signInWithFacebook, signUpWithEmail, confirmEmailSignUp, resendConfirmationCode, confirmPasswordReset } from '../services/auth';
import { requestPasswordReset } from '../services/api';
import { useAuthStore } from '../store/authStore';
import './Landing.css';

// Facebook IdP: CDK, backend, and CI wiring all done; FACEBOOK_APP_ID/
// FACEBOOK_APP_SECRET are set as GitHub secrets. See item 189.
const FACEBOOK_LOGIN_ENABLED = true;

const spec = (tag: string, body: React.ReactNode) => (
  <div key={tag} className="tm-bp-spec">
    <span className="tm-bp-spec-tag">{tag}</span>
    <div className="tm-bp-spec-body">{body}</div>
  </div>
);

type AuthMode = 'signin' | 'signup' | 'verify' | 'forgot' | 'reset';

export default function Landing() {
  const navigate = useNavigate();
  const setUser = useAuthStore((s) => s.setUser);
  const [mode, setMode] = useState<AuthMode>('signin');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [signupPassword, setSignupPassword] = useState('');
  const [signupConfirmPassword, setSignupConfirmPassword] = useState('');
  const [pendingEmail, setPendingEmail] = useState('');
  const [code, setCode] = useState('');
  const [forgotEmail, setForgotEmail] = useState('');
  const [resetCode, setResetCode] = useState('');
  const [resetNewPassword, setResetNewPassword] = useState('');
  const [resetConfirmPassword, setResetConfirmPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  // Form is injected by JS only — crawlers see product copy without a login wall
  const [formMounted, setFormMounted] = useState(false);

  useEffect(() => {
    document.title = 'TankMaze — the strategic tank programming game';
    const meta = document.querySelector('meta[name="description"]') ?? (() => {
      const m = document.createElement('meta');
      m.setAttribute('name', 'description');
      document.head.appendChild(m);
      return m;
    })();
    meta.setAttribute('content', 'TankMaze is a strategic programming game where you write Go code to control an autonomous tank. Compete in tournaments, climb the global leaderboard, and debug your AI with tick-by-tick replays.');
    setFormMounted(true);
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

  async function handleFacebookSignIn() {
    if (import.meta.env.VITE_LOCAL_DEV === 'true') {
      setUser({ userId: 'local-user', username: 'local' });
      navigate('/dashboard');
      return;
    }
    setLoading(true);
    try {
      await signInWithFacebook();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Facebook sign-in failed');
      setLoading(false);
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setInfo(null);
    setLoading(true);
    try {
      const result = await signIn(username, password);
      if (result.isSignedIn) {
        setUser({ userId: '', username });
        navigate('/dashboard');
      } else if (result.nextStep?.signInStep === 'CONFIRM_SIGN_UP') {
        // Amplify v6 doesn't throw for unconfirmed users — it resolves with this step instead.
        setPendingEmail(username);
        setMode('verify');
      }
    } catch (err) {
      if (err instanceof Error && err.name === 'UserNotConfirmedException') {
        // Defensive fallback in case some path still throws (older Amplify behavior).
        setPendingEmail(username);
        setMode('verify');
      } else {
        setError(err instanceof Error ? err.message : 'Sign in failed');
      }
    } finally {
      setLoading(false);
    }
  }

  async function handleSignUp(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setInfo(null);
    if (signupPassword !== signupConfirmPassword) {
      setError('Passwords do not match');
      return;
    }
    setLoading(true);
    try {
      await signUpWithEmail(username, signupPassword);
      setPendingEmail(username);
      setMode('verify');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sign up failed');
    } finally {
      setLoading(false);
    }
  }

  async function handleVerify(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setInfo(null);
    setLoading(true);
    try {
      await confirmEmailSignUp(pendingEmail, code);
      setUsername(pendingEmail);
      setPassword('');
      setCode('');
      setMode('signin');
      setInfo('Email verified — you can sign in now.');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Verification failed');
    } finally {
      setLoading(false);
    }
  }

  // Response text is fixed regardless of outcome (item 217) — never reveals
  // whether the email exists or which auth method the account (if any) uses.
  const FORGOT_PASSWORD_GENERIC_MESSAGE = 'If that email is in our system, a link to recover your password will be sent.';

  async function handleForgotSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setInfo(null);
    setLoading(true);
    try {
      await requestPasswordReset(forgotEmail);
      setInfo(FORGOT_PASSWORD_GENERIC_MESSAGE);
      setMode('reset');
    } catch {
      // A thrown error here means the request itself failed (network/backend
      // issue), not that the email doesn't exist — the backend always 202s
      // before any lookup happens. Safe to surface distinctly.
      setError('Something went wrong — please try again.');
    } finally {
      setLoading(false);
    }
  }

  async function handleResetSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setInfo(null);
    if (resetNewPassword !== resetConfirmPassword) {
      setError('Passwords do not match');
      return;
    }
    setLoading(true);
    try {
      await confirmPasswordReset(forgotEmail, resetCode, resetNewPassword);
      setUsername(forgotEmail);
      setPassword('');
      setResetCode('');
      setResetNewPassword('');
      setResetConfirmPassword('');
      setMode('signin');
      setInfo('Password reset — you can sign in now.');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Reset failed');
    } finally {
      setLoading(false);
    }
  }

  async function handleResendCode() {
    setError(null);
    setInfo(null);
    try {
      await resendConfirmationCode(pendingEmail);
      setInfo('Code resent — check your inbox.');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to resend code');
    }
  }

  const panelTitle = mode === 'signin' ? 'Sign in'
    : mode === 'signup' ? 'Create account'
    : mode === 'verify' ? 'Verify email'
    : mode === 'forgot' ? 'Reset password'
    : 'Enter reset code';

  return (
    <div className="tm-bp">
      <div className="tm-bp-sheet">
        <span className="tm-bp-tick tm-bp-tick-tl" />
        <span className="tm-bp-tick tm-bp-tick-tr" />
        <span className="tm-bp-tick tm-bp-tick-bl" />
        <span className="tm-bp-tick tm-bp-tick-br" />

        {/* Left — product description (always rendered, visible to crawlers) */}
        <div className="tm-bp-left">
          <div className="tm-bp-wordmark-row">
            <img src="/logo.png" alt="TankMaze logo" width={66} height={86} />
            <h1 className="tm-bp-wordmark">
              <span>Tank</span>
              <span className="tm-bp-wordmark-accent">Maze</span>
            </h1>
          </div>
          <p className="tm-bp-kicker">A strategic programming game</p>

          <svg className="tm-bp-trace" viewBox="0 0 400 34" width="100%" height="34" aria-hidden="true">
            <path d="M2,17 L80,17 L80,4 L200,4 L200,30 L320,30 L320,17 L398,17" />
            <circle cx="2" cy="17" r="3" />
            <circle cx="398" cy="17" r="3" />
          </svg>

          <p className="tm-bp-lede">
            Somewhere in the labyrinth, an enemy tank is moving. You don't know where — only that your sensors just spiked and you have one tick to decide: advance, turn, fire, or wait for better data.
          </p>
          <p className="tm-bp-sub">
            You don't drive a tank. You code one. Write the autonomous brain that drives your tank in Go, submit it, and watch your creation navigate fog-of-war, enemy contact, and the limits of its own hardware — without you.
          </p>

          <div className="tm-bp-specs">
            {spec('BRAIN', <>Implement a <strong>Tick(sensors) Action</strong> function. It runs every 100&nbsp;ms. Package-level variables are your tank's memory.</>)}
            {spec('LOADOUT', <>Distribute <strong>15 points</strong> across speed, sensor range, damage, armor, and fire rate. Every build is a trade-off.</>)}
            {spec('GAME DAYS', <>Round-robin groups of 8 followed by <strong>single-elimination</strong>. Placement awards global ranking points.</>)}
            {spec('REPLAY', <>Every match recorded <strong>tick-by-tick</strong>. Replay at any speed, inspect sensor readings, memory, and console output.</>)}
          </div>

          <p className="tm-bp-repo-label">OPEN SOURCE — FREE TO INSPECT AND FORK</p>
          <a href="https://github.com/mauricio-camayo/tankmaze" target="_blank" rel="noopener noreferrer" className="tm-bp-repo-link">
            <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8a8 8 0 005.47 7.59c.4.07.55-.17.55-.38v-1.34C3.73 14.36 3.26 12.8 3.26 12.8c-.36-.91-.88-1.15-.88-1.15-.72-.49.05-.48.05-.48.8.06 1.22.82 1.22.82.71 1.22 1.87.87 2.33.66.07-.52.28-.87.5-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.22 2.2.82A7.68 7.68 0 018 4.07c.68 0 1.36.09 2 .27 1.52-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48v2.19c0 .21.15.46.55.38A8 8 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>
            View on GitHub
          </a>
        </div>

        {/* Right — sign-in panel, styled as a drafting title block; injected by JS only */}
        {formMounted && (
          <div className="tm-bp-panel">
            <div className="tm-bp-panel-meta">
              <div className="tm-bp-panel-meta-cell">
                <span className="tm-bp-panel-meta-k">SHEET</span>
                <span className="tm-bp-panel-meta-v">AUTH-01</span>
              </div>
              <div className="tm-bp-panel-meta-cell">
                <span className="tm-bp-panel-meta-k">REV</span>
                <span className="tm-bp-panel-meta-v">v{import.meta.env.VITE_APP_VERSION}</span>
              </div>
            </div>

            <div className="tm-bp-panel-body">
              <h2 className="tm-bp-panel-title">{panelTitle}</h2>

              {(mode === 'signin' || mode === 'signup') && (
                <>
                  <button type="button" onClick={handleGoogleSignIn} disabled={loading} className="tm-bp-btn-google">
                    <svg width="18" height="18" viewBox="0 0 48 48">
                      <path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z"/>
                      <path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z"/>
                      <path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z"/>
                      <path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.18 1.48-4.97 2.31-8.16 2.31-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z"/>
                    </svg>
                    {mode === 'signin' ? 'Sign in with Google' : 'Sign up with Google'}
                  </button>

                  {FACEBOOK_LOGIN_ENABLED && (
                    <button type="button" onClick={handleFacebookSignIn} disabled={loading} className="tm-bp-btn-facebook">
                      <svg width="18" height="18" viewBox="0 0 24 24" fill="#fff">
                        <path d="M22 12.06C22 6.51 17.52 2 12 2S2 6.51 2 12.06c0 5.02 3.66 9.18 8.44 9.94v-7.03H7.9v-2.91h2.54V9.85c0-2.51 1.49-3.9 3.77-3.9 1.09 0 2.23.2 2.23.2v2.46h-1.26c-1.24 0-1.63.77-1.63 1.56v1.89h2.78l-.44 2.91h-2.34V22c4.78-.76 8.44-4.92 8.44-9.94z"/>
                      </svg>
                      {mode === 'signin' ? 'Sign in with Facebook' : 'Sign up with Facebook'}
                    </button>
                  )}

                  <div className="tm-bp-divider">
                    <hr /><span>OR</span><hr />
                  </div>
                </>
              )}

              {mode === 'signin' && (
                <form onSubmit={handleSubmit}>
                  <div className="tm-bp-field">
                    <label className="tm-bp-label">Email</label>
                    <input type="email" value={username} onChange={(e) => setUsername(e.target.value)} autoFocus required className="tm-bp-input" />
                  </div>
                  <div className="tm-bp-field">
                    <div className="tm-bp-field-row">
                      <label className="tm-bp-label">Password</label>
                      <button type="button" onClick={() => { setForgotEmail(username); setMode('forgot'); setError(null); setInfo(null); }} className="tm-bp-link-btn">
                        Forgot password?
                      </button>
                    </div>
                    <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} required className="tm-bp-input" />
                  </div>
                  {error && <p className="tm-bp-error">{error}</p>}
                  {info && <p className="tm-bp-info">{info}</p>}
                  <button type="submit" disabled={loading} className="tm-bp-submit">
                    {loading ? 'Signing in…' : 'Sign in'}
                  </button>
                  <p className="tm-bp-switch">
                    Don't have an account?{' '}
                    <button type="button" onClick={() => { setMode('signup'); setError(null); setInfo(null); }}>Create one</button>
                  </p>
                </form>
              )}

              {mode === 'signup' && (
                <form onSubmit={handleSignUp}>
                  <div className="tm-bp-field">
                    <label className="tm-bp-label">Email</label>
                    <input type="email" value={username} onChange={(e) => setUsername(e.target.value)} autoFocus required className="tm-bp-input" />
                  </div>
                  <div className="tm-bp-field">
                    <label className="tm-bp-label">Password</label>
                    <input type="password" value={signupPassword} onChange={(e) => setSignupPassword(e.target.value)} required minLength={8} className="tm-bp-input" />
                    <p className="tm-bp-hint">MIN. 8 CHARS — UPPER, LOWER, DIGIT</p>
                  </div>
                  <div className="tm-bp-field">
                    <label className="tm-bp-label">Confirm password</label>
                    <input type="password" value={signupConfirmPassword} onChange={(e) => setSignupConfirmPassword(e.target.value)} required minLength={8} className="tm-bp-input" />
                  </div>
                  {error && <p className="tm-bp-error">{error}</p>}
                  <button type="submit" disabled={loading} className="tm-bp-submit">
                    {loading ? 'Creating account…' : 'Create account'}
                  </button>
                  <p className="tm-bp-switch">
                    Already have an account?{' '}
                    <button type="button" onClick={() => { setMode('signin'); setError(null); setInfo(null); }}>Sign in</button>
                  </p>
                </form>
              )}

              {mode === 'verify' && (
                <form onSubmit={handleVerify}>
                  <p className="tm-bp-hint" style={{ fontSize: 12.5, marginBottom: 16, lineHeight: 1.6, color: 'var(--bp-steel)' }}>
                    We sent a verification code to <strong style={{ color: 'var(--bp-line)' }}>{pendingEmail}</strong>. Enter it below to activate your account.
                  </p>
                  <div className="tm-bp-field">
                    <label className="tm-bp-label">Verification code</label>
                    <input value={code} onChange={(e) => setCode(e.target.value)} autoFocus required inputMode="numeric" className="tm-bp-input" />
                  </div>
                  {error && <p className="tm-bp-error">{error}</p>}
                  {info && <p className="tm-bp-info">{info}</p>}
                  <button type="submit" disabled={loading} className="tm-bp-submit">
                    {loading ? 'Verifying…' : 'Verify'}
                  </button>
                  <p className="tm-bp-switch">
                    <button type="button" onClick={handleResendCode}>Resend code</button>
                    {' · '}
                    <button type="button" className="muted" onClick={() => { setMode('signin'); setError(null); setInfo(null); }}>Back to sign in</button>
                  </p>
                </form>
              )}

              {mode === 'forgot' && (
                <form onSubmit={handleForgotSubmit}>
                  <p className="tm-bp-hint" style={{ fontSize: 12.5, marginBottom: 16, lineHeight: 1.6, color: 'var(--bp-steel)' }}>
                    Enter your account email and we'll send you a way to get back in.
                  </p>
                  <div className="tm-bp-field">
                    <label className="tm-bp-label">Email</label>
                    <input type="email" value={forgotEmail} onChange={(e) => setForgotEmail(e.target.value)} autoFocus required className="tm-bp-input" />
                  </div>
                  {error && <p className="tm-bp-error">{error}</p>}
                  {info && <p className="tm-bp-info">{info}</p>}
                  <button type="submit" disabled={loading} className="tm-bp-submit">
                    {loading ? 'Sending…' : 'Send reset link'}
                  </button>
                  <p className="tm-bp-switch">
                    <button type="button" onClick={() => { setMode('reset'); setError(null); setInfo(null); }}>I already have a code</button>
                    {' · '}
                    <button type="button" className="muted" onClick={() => { setMode('signin'); setError(null); setInfo(null); }}>Back to sign in</button>
                  </p>
                </form>
              )}

              {mode === 'reset' && (
                <form onSubmit={handleResetSubmit}>
                  <div className="tm-bp-field">
                    <label className="tm-bp-label">Email</label>
                    <input type="email" value={forgotEmail} onChange={(e) => setForgotEmail(e.target.value)} autoFocus required className="tm-bp-input" />
                  </div>
                  <div className="tm-bp-field">
                    <label className="tm-bp-label">Reset code</label>
                    <input value={resetCode} onChange={(e) => setResetCode(e.target.value)} required inputMode="numeric" className="tm-bp-input" />
                  </div>
                  <div className="tm-bp-field">
                    <label className="tm-bp-label">New password</label>
                    <input type="password" value={resetNewPassword} onChange={(e) => setResetNewPassword(e.target.value)} required minLength={8} className="tm-bp-input" />
                  </div>
                  <div className="tm-bp-field">
                    <label className="tm-bp-label">Confirm new password</label>
                    <input type="password" value={resetConfirmPassword} onChange={(e) => setResetConfirmPassword(e.target.value)} required minLength={8} className="tm-bp-input" />
                  </div>
                  {error && <p className="tm-bp-error">{error}</p>}
                  {info && <p className="tm-bp-info">{info}</p>}
                  <button type="submit" disabled={loading} className="tm-bp-submit">
                    {loading ? 'Resetting…' : 'Reset password'}
                  </button>
                  <p className="tm-bp-switch">
                    <button type="button" className="muted" onClick={() => { setMode('signin'); setError(null); setInfo(null); }}>Back to sign in</button>
                  </p>
                </form>
              )}

              <p className="tm-bp-legal">
                By continuing you agree to our <Link to="/privacy">Privacy Policy</Link>.
              </p>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
