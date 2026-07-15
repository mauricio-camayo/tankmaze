import { Link } from 'react-router-dom';

const s = {
  page:    { maxWidth: 720, margin: '0 auto', padding: '48px 24px', color: '#e7f1f7', fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif', lineHeight: 1.7 } as React.CSSProperties,
  h1:      { fontSize: 28, fontWeight: 700, marginBottom: 8 } as React.CSSProperties,
  updated: { color: '#5b87a3', fontSize: 13, marginBottom: 40 } as React.CSSProperties,
  h2:      { fontSize: 18, fontWeight: 600, marginTop: 36, marginBottom: 10, color: '#ffab6b' } as React.CSSProperties,
  p:       { margin: '0 0 14px', color: '#7fa2ba' } as React.CSSProperties,
  ul:      { margin: '0 0 14px', paddingLeft: 20, color: '#7fa2ba' } as React.CSSProperties,
  a:       { color: '#ff7a29' } as React.CSSProperties,
  back:    { display: 'inline-block', marginBottom: 32, color: '#ff7a29', fontSize: 14 } as React.CSSProperties,
  divider: { border: 'none', borderTop: '1px solid #144a68', margin: '56px 0' } as React.CSSProperties,
  toc:     { color: '#7fa2ba', fontSize: 14, marginBottom: 40 } as React.CSSProperties,
};

export default function Legal() {
  return (
    <div style={{ background: '#0a3550', minHeight: '100vh' }}>
      <div style={s.page}>
        <Link to="/dashboard" style={s.back}>← Back to TankMaze</Link>

        <p style={s.toc}>
          <a href="#privacy" style={s.a}>Privacy Policy</a>
          {' · '}
          <a href="#terms" style={s.a}>Terms of Service</a>
        </p>

        <h1 id="privacy" style={s.h1}>Privacy Policy</h1>
        <p style={s.updated}>Last updated: July 15, 2026</p>

        <p style={s.p}>
          TankMaze ("we", "us", or "our") operates the game platform at tankmaze.org. This policy
          explains what data we collect, how we use it, and your rights.
        </p>

        <h2 style={s.h2}>Data We Collect</h2>
        <p style={s.p}>When you create an account or sign in, we collect:</p>
        <ul style={s.ul}>
          <li><strong>Email address</strong> — used for account identification and recovery.</li>
          <li><strong>Display name</strong> — shown on leaderboards and game day results.</li>
          <li><strong>Profile picture</strong> — displayed in the navigation bar (Google, GitHub, and Discord sign-in provide one; email/password accounts don't).</li>
        </ul>
        <p style={s.p}>
          When you sign in with Google, GitHub, or Discord, the above is provided by that provider via
          OAuth 2.0. We do not receive your password on any of these providers, or any data beyond what
          you explicitly authorize.
        </p>
        <p style={s.p}>
          We also store the tank code and WASM binaries you submit, your game results, your
          subscription tier, and any avatar image you upload for a tank.
        </p>
        <p style={s.p}>
          If you use <strong>Friends &amp; direct messaging</strong>, we store your friend relationships
          (requests, acceptances, blocks) and the content of messages you send to accepted friends.
          Messages are automatically deleted 30 days after being sent.
        </p>

        <h2 style={s.h2}>How We Use Your Data</h2>
        <ul style={s.ul}>
          <li>Authenticate you and associate tanks with your account.</li>
          <li>Display your name and ranking on public leaderboards.</li>
          <li>Compile and run your tank code in isolated AWS CodeBuild environments.</li>
          <li>Enforce per-tier usage limits (tanks created, compilations per month).</li>
          <li>Deliver friend requests and messages to the intended recipient.</li>
        </ul>
        <p style={s.p}>
          We do not sell your personal data to third parties.
        </p>

        <h2 style={s.h2}>Who Can Access Your Data</h2>
        <p style={s.p}>
          TankMaze administrators can view account details (email, sign-in provider, subscription
          tier, tanks owned) to provide support and enforce these terms, and can disable, adjust the
          tier of, or delete an account when necessary. Administrators cannot read your direct messages.
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
          logs expire after 7 days. Direct messages expire after 30 days. You may request deletion of
          your account and all associated data by contacting us at the email below.
        </p>

        <h2 style={s.h2}>Your Rights</h2>
        <p style={s.p}>
          Depending on your jurisdiction you may have rights to access, correct, or delete your
          personal data. To exercise these rights, email{' '}
          <a href="mailto:info@tankmaze.org" style={s.a}>info@tankmaze.org</a>.
        </p>

        <h2 style={s.h2}>Changes to This Policy</h2>
        <p style={s.p}>
          We may update this policy as the platform evolves. Significant changes will be noted by
          updating the "Last updated" date above. Continued use of TankMaze after changes constitutes
          acceptance of the revised policy.
        </p>

        <hr style={s.divider} />

        <h1 id="terms" style={s.h1}>Terms of Service</h1>
        <p style={s.updated}>Last updated: July 15, 2026</p>

        <p style={s.p}>
          These Terms of Service ("Terms") govern your use of TankMaze. By creating an account or
          otherwise using the platform, you agree to these Terms. If you do not agree, do not use
          TankMaze.
        </p>

        <h2 style={s.h2}>Accounts</h2>
        <p style={s.p}>
          You must maintain one account per person. You are responsible for the tank code, avatar
          images, and messages you submit under your account, and for keeping your sign-in credentials
          secure. Do not impersonate another person or entity.
        </p>

        <h2 style={s.h2}>Your Content & License to Us</h2>
        <p style={s.p}>
          You retain ownership of the tank source code, avatar images, and messages you submit
          ("Your Content"). By submitting Your Content, you grant TankMaze a worldwide, non-exclusive,
          royalty-free license to store, compile, execute, and display it as needed to operate the
          platform — for example, running your tank's WASM binary in matches, showing your avatar and
          match replays, and delivering your messages to their recipient.
        </p>

        <h2 style={s.h2}>Acceptable Use</h2>
        <p style={s.p}>You agree not to:</p>
        <ul style={s.ul}>
          <li>Submit tank code that attempts to exhaust shared infrastructure, escape the WASM sandbox, or otherwise attack the platform or other players.</li>
          <li>Harass, threaten, or abuse other players, including through Friends messaging.</li>
          <li>Create accounts through automated means or to circumvent tier limits or a ban.</li>
          <li>Use the platform for any unlawful purpose.</li>
        </ul>
        <p style={s.p}>
          Violating these terms may result in your account being disabled or deleted, at our discretion.
        </p>

        <h2 style={s.h2}>Subscription Tiers</h2>
        <p style={s.p}>
          TankMaze offers Free, Builder, and Pro tiers with different tank and compilation quotas.
          Tiers affect quotas only — gameplay rules, stats, and scoring are identical across all
          tiers. Paid tier billing is not yet automated; tier changes are currently applied manually.
          These Terms will be updated once self-service billing is available.
        </p>

        <h2 style={s.h2}>Termination</h2>
        <p style={s.p}>
          You may stop using TankMaze at any time and request account deletion as described in the
          Privacy Policy above. We may suspend or terminate your account for violating these Terms or
          to protect the platform and other players.
        </p>

        <h2 style={s.h2}>Disclaimer & Limitation of Liability</h2>
        <p style={s.p}>
          TankMaze is provided "as is" without warranties of any kind. We do not guarantee
          uninterrupted availability, and we are not liable for loss of tank code, rankings, match
          history, or other data, to the maximum extent permitted by law.
        </p>

        <h2 style={s.h2}>Governing Law</h2>
        <p style={s.p}>
          These Terms are governed by the laws of the Republic of Colombia, without regard to its
          conflict-of-law principles.
        </p>

        <h2 style={s.h2}>Changes to These Terms</h2>
        <p style={s.p}>
          We may update these Terms as the platform evolves. Significant changes will be noted by
          updating the "Last updated" date above. Continued use of TankMaze after changes constitutes
          acceptance of the revised Terms.
        </p>

        <h2 style={s.h2}>Contact</h2>
        <p style={s.p}>
          Questions? Email{' '}
          <a href="mailto:info@tankmaze.org" style={s.a}>info@tankmaze.org</a>.
        </p>
      </div>
    </div>
  );
}
