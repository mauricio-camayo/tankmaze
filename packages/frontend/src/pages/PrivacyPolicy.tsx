import { Link } from 'react-router-dom';

const s = {
  page:    { maxWidth: 720, margin: '0 auto', padding: '48px 24px', color: '#e2e8f0', fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif', lineHeight: 1.7 } as React.CSSProperties,
  h1:      { fontSize: 28, fontWeight: 700, marginBottom: 8 } as React.CSSProperties,
  updated: { color: '#64748b', fontSize: 13, marginBottom: 40 } as React.CSSProperties,
  h2:      { fontSize: 18, fontWeight: 600, marginTop: 36, marginBottom: 10, color: '#a78bfa' } as React.CSSProperties,
  p:       { margin: '0 0 14px', color: '#94a3b8' } as React.CSSProperties,
  ul:      { margin: '0 0 14px', paddingLeft: 20, color: '#94a3b8' } as React.CSSProperties,
  a:       { color: '#7c6af7' } as React.CSSProperties,
  back:    { display: 'inline-block', marginBottom: 32, color: '#7c6af7', fontSize: 14 } as React.CSSProperties,
};

export default function PrivacyPolicy() {
  return (
    <div style={{ background: '#0f0f1a', minHeight: '100vh' }}>
      <div style={s.page}>
        <Link to="/dashboard" style={s.back}>← Back to TankMaze</Link>

        <h1 style={s.h1}>Privacy Policy</h1>
        <p style={s.updated}>Last updated: July 1, 2026</p>

        <p style={s.p}>
          TankMaze ("we", "us", or "our") operates the game platform at tankmaze.org. This policy
          explains what data we collect, how we use it, and your rights.
        </p>

        <h2 style={s.h2}>Data We Collect</h2>
        <p style={s.p}>When you create an account or sign in, we collect:</p>
        <ul style={s.ul}>
          <li><strong>Email address</strong> — used for account identification and recovery.</li>
          <li><strong>Display name</strong> — shown on leaderboards and game day results.</li>
          <li><strong>Profile picture</strong> — displayed in the navigation bar (Google sign-in only).</li>
        </ul>
        <p style={s.p}>
          When you sign in with Google, the above is provided by Google via OAuth 2.0. We do not
          receive your Google password or any data beyond what you explicitly authorize.
        </p>
        <p style={s.p}>
          We also store the tank code and WASM binaries you submit, your game results, and your
          subscription tier.
        </p>

        <h2 style={s.h2}>How We Use Your Data</h2>
        <ul style={s.ul}>
          <li>Authenticate you and associate tanks with your account.</li>
          <li>Display your name and ranking on public leaderboards.</li>
          <li>Compile and run your tank code in isolated AWS CodeBuild environments.</li>
          <li>Enforce per-tier usage limits (tanks created, compilations per month).</li>
        </ul>
        <p style={s.p}>
          We do not sell your personal data to third parties.
        </p>

        <h2 style={s.h2}>Google AdSense & Cookies</h2>
        <p style={s.p}>
          TankMaze uses Google AdSense to display advertisements to free-tier users. Google AdSense
          may set cookies and use device identifiers to serve personalized ads based on your browsing
          history and interests. This is governed by{' '}
          <a href="https://policies.google.com/privacy" style={s.a} target="_blank" rel="noopener noreferrer">
            Google's Privacy Policy
          </a>.
        </p>
        <p style={s.p}>
          You can opt out of personalized advertising via{' '}
          <a href="https://adssettings.google.com" style={s.a} target="_blank" rel="noopener noreferrer">
            Google Ads Settings
          </a>{' '}
          or by using a browser extension such as the{' '}
          <a href="https://optout.aboutads.info" style={s.a} target="_blank" rel="noopener noreferrer">
            Digital Advertising Alliance opt-out tool
          </a>.
        </p>
        <p style={s.p}>
          Builder and Pro tier subscribers do not see ads and Google AdSense scripts are not loaded
          for their sessions.
        </p>

        <h2 style={s.h2}>Data Storage & Security</h2>
        <p style={s.p}>
          All data is stored in AWS (us-east-1 region) using DynamoDB and S3. Access is restricted
          via AWS IAM; data in transit is encrypted with TLS. Tank source code is stored in S3 with
          private bucket policies.
        </p>

        <h2 style={s.h2}>Data Retention</h2>
        <p style={s.p}>
          Your account data and tanks are retained for as long as your account is active. Test match
          logs expire after 7 days. You may request deletion of your account and all associated data
          by contacting us at the email below.
        </p>

        <h2 style={s.h2}>Your Rights</h2>
        <p style={s.p}>
          Depending on your jurisdiction you may have rights to access, correct, or delete your
          personal data. To exercise these rights, email{' '}
          <a href="mailto:mauricio.camayo@gmail.com" style={s.a}>mauricio.camayo@gmail.com</a>.
        </p>

        <h2 style={s.h2}>Changes to This Policy</h2>
        <p style={s.p}>
          We may update this policy as the platform evolves. Significant changes will be noted by
          updating the "Last updated" date above. Continued use of TankMaze after changes constitutes
          acceptance of the revised policy.
        </p>

        <h2 style={s.h2}>Contact</h2>
        <p style={s.p}>
          Questions? Email{' '}
          <a href="mailto:mauricio.camayo@gmail.com" style={s.a}>mauricio.camayo@gmail.com</a>.
        </p>
      </div>
    </div>
  );
}
