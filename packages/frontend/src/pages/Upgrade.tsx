import { useEffect, useState } from 'react';
import Layout, { cardStyle, primaryButtonStyle, ghostButtonStyle } from '../components/Layout';
import { getMySettings } from '../services/api';
import type { UserSettings } from '../types';

const tierColors: Record<string, string> = {
  free: '#64748b',
  builder: '#7c6af7',
  pro: '#f59e0b',
};

// Mirrors db.TierLimits in packages/backend/internal/db/usersettings.go —
// keep in sync if the backend limits ever change.
interface TierInfo {
  id: 'free' | 'builder' | 'pro';
  label: string;
  price: string;
  tankLimit: number;
  compileLimit: number;
  ads: boolean;
  blurb: string;
}

const TIERS: TierInfo[] = [
  {
    id: 'free',
    label: 'Free',
    price: '$0',
    tankLimit: 2,
    compileLimit: 10,
    ads: true,
    blurb: 'Everything you need to try TankMaze and compete in Game Days.',
  },
  {
    id: 'builder',
    label: 'Builder',
    price: 'Coming soon',
    tankLimit: 5,
    compileLimit: 50,
    ads: false,
    blurb: 'For authors iterating on multiple tanks and shipping more versions.',
  },
  {
    id: 'pro',
    label: 'Pro',
    price: 'Coming soon',
    tankLimit: 15,
    compileLimit: 200,
    ads: false,
    blurb: 'For serious tank builders running a full roster of strategies.',
  },
];

function Check({ ok }: { ok: boolean }) {
  return (
    <span style={{ color: ok ? '#4ade80' : '#475569', fontWeight: 700 }}>
      {ok ? '✓' : '—'}
    </span>
  );
}

export default function Upgrade() {
  const [settings, setSettings] = useState<UserSettings | null>(null);

  useEffect(() => {
    getMySettings().then(setSettings).catch(() => setSettings(null));
  }, []);

  const currentTier = settings?.tier ?? 'free';

  return (
    <Layout>
      <div style={{ textAlign: 'center', maxWidth: 640, margin: '0 auto 32px' }}>
        <h1 style={{ fontSize: 28, fontWeight: 700, color: '#e2e8f0', margin: '0 0 12px' }}>
          Upgrade your plan
        </h1>
        <p style={{ color: '#94a3b8', fontSize: 15, lineHeight: 1.6, margin: 0 }}>
          TankMaze tiers exist to cover the cost of compiling your tank's code, not to give anyone an
          edge in the maze — every tier plays by the exact same rules. Paying just gets you more tanks,
          more compilations per month, and an ad-free experience.
        </p>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 20 }}>
        {TIERS.map((tier) => {
          const isCurrent = tier.id === currentTier;
          return (
            <div
              key={tier.id}
              style={{
                ...cardStyle,
                display: 'flex',
                flexDirection: 'column',
                border: isCurrent ? `1px solid ${tierColors[tier.id]}` : cardStyle.border,
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 4 }}>
                <span style={{ color: tierColors[tier.id], fontWeight: 700, fontSize: 18, textTransform: 'uppercase', letterSpacing: 0.5 }}>
                  {tier.label}
                </span>
                {isCurrent && (
                  <span style={{ background: tierColors[tier.id], color: '#0f0f1a', fontSize: 11, fontWeight: 700, padding: '2px 8px', borderRadius: 12 }}>
                    Current
                  </span>
                )}
              </div>
              <div style={{ fontSize: 22, fontWeight: 700, color: '#e2e8f0', marginBottom: 12 }}>
                {tier.price}
              </div>
              <p style={{ color: '#64748b', fontSize: 13, lineHeight: 1.5, margin: '0 0 20px', minHeight: 40 }}>
                {tier.blurb}
              </p>

              <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginBottom: 24, flex: 1 }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 14 }}>
                  <span style={{ color: '#94a3b8' }}>Tanks</span>
                  <span style={{ color: '#e2e8f0', fontWeight: 600 }}>{tier.tankLimit}</span>
                </div>
                <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 14 }}>
                  <span style={{ color: '#94a3b8' }}>Compilations / month</span>
                  <span style={{ color: '#e2e8f0', fontWeight: 600 }}>{tier.compileLimit}</span>
                </div>
                <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 14 }}>
                  <span style={{ color: '#94a3b8' }}>Ad-free</span>
                  <Check ok={!tier.ads} />
                </div>
              </div>

              {isCurrent ? (
                <button disabled style={{ ...ghostButtonStyle, opacity: 0.5, cursor: 'default' }}>
                  Your current plan
                </button>
              ) : tier.id === 'free' ? (
                <button disabled style={{ ...ghostButtonStyle, opacity: 0.5, cursor: 'default' }}>
                  Downgrade — contact support
                </button>
              ) : (
                <button
                  disabled
                  title="Paid plans aren't available yet — check back soon."
                  style={{ ...primaryButtonStyle, opacity: 0.5, cursor: 'not-allowed' }}
                >
                  Coming soon
                </button>
              )}
            </div>
          );
        })}
      </div>

      <p style={{ textAlign: 'center', color: '#475569', fontSize: 12, marginTop: 28 }}>
        Paid plans are not yet available for purchase. This page shows what each tier will include —
        check back later, or watch the GitHub repo for updates.
      </p>
    </Layout>
  );
}
