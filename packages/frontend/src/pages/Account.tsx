import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import Layout, { cardStyle, primaryButtonStyle } from '../components/Layout';
import { getMySettings } from '../services/api';
import { listTanks } from '../services/api';
import type { UserSettings } from '../types';

const tierColors: Record<string, string> = {
  free: '#64748b',
  builder: '#7c6af7',
  pro: '#f59e0b',
};

const tierLabels: Record<string, string> = {
  free: 'Free',
  builder: 'Builder',
  pro: 'Pro',
};

function ProgressBar({ value, max, color }: { value: number; max: number; color: string }) {
  const pct = Math.min(100, (value / Math.max(1, max)) * 100);
  return (
    <div style={{ background: '#2d2d4e', borderRadius: 4, height: 8, overflow: 'hidden' }}>
      <div style={{ width: `${pct}%`, background: color, height: '100%', borderRadius: 4, transition: 'width 0.3s' }} />
    </div>
  );
}

function windowResetLabel(windowStart: string): string {
  if (!windowStart) return '';
  const start = new Date(windowStart);
  const resets = new Date(start.getTime() + 30 * 24 * 60 * 60 * 1000);
  const now = new Date();
  const diffMs = resets.getTime() - now.getTime();
  if (diffMs <= 0) return 'resets now';
  const days = Math.ceil(diffMs / (1000 * 60 * 60 * 24));
  return `resets in ${days} day${days === 1 ? '' : 's'}`;
}

export default function Account() {
  const [settings, setSettings] = useState<UserSettings | null>(null);
  const [tankCount, setTankCount] = useState<number>(0);
  const [error, setError] = useState('');

  useEffect(() => {
    Promise.all([getMySettings(), listTanks()])
      .then(([s, tanks]) => {
        setSettings(s);
        setTankCount(tanks.length);
      })
      .catch((e) => setError(e.message));
  }, []);

  const tier = settings?.tier ?? 'free';
  const color = tierColors[tier] ?? '#7c6af7';

  return (
    <Layout>
      <h1 style={{ fontSize: 28, fontWeight: 700, color: '#e2e8f0', marginBottom: 8 }}>Account</h1>

      {error && (
        <div style={{ ...cardStyle, borderColor: '#7f1d1d', color: '#fca5a5', marginBottom: 16 }}>{error}</div>
      )}

      {settings && (
        <>
          {/* Tier badge */}
          <div style={{ ...cardStyle, marginBottom: 20 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 16 }}>
              <span style={{
                background: color,
                color: '#fff',
                borderRadius: 20,
                padding: '4px 16px',
                fontWeight: 700,
                fontSize: 16,
                letterSpacing: 1,
                textTransform: 'uppercase',
              }}>
                {tierLabels[tier] ?? tier}
              </span>
              <span style={{ color: '#64748b', fontSize: 14 }}>Current plan</span>
            </div>

            {/* Tank usage */}
            <div style={{ marginBottom: 16 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 6, fontSize: 14 }}>
                <span style={{ color: '#94a3b8' }}>Tanks</span>
                <span style={{ color: '#e2e8f0', fontWeight: 600 }}>
                  {tankCount} / {settings.tankLimit}
                </span>
              </div>
              <ProgressBar value={tankCount} max={settings.tankLimit} color={color} />
            </div>

            {/* Compilation usage */}
            <div>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 6, fontSize: 14 }}>
                <span style={{ color: '#94a3b8' }}>
                  Compilations
                  {settings.windowStart && (
                    <span style={{ color: '#64748b', marginLeft: 8, fontSize: 12 }}>
                      ({windowResetLabel(settings.windowStart)})
                    </span>
                  )}
                </span>
                <span style={{ color: '#e2e8f0', fontWeight: 600 }}>
                  {settings.compilationsThisWindow} / {settings.compilationLimit}
                </span>
              </div>
              <ProgressBar value={settings.compilationsThisWindow} max={settings.compilationLimit} color={color} />
            </div>
          </div>

          {/* Upgrade CTA */}
          {tier !== 'pro' && (
            <div style={{ ...cardStyle, textAlign: 'center', padding: '28px 24px' }}>
              <p style={{ color: '#94a3b8', marginBottom: 16, fontSize: 15 }}>
                {tier === 'free'
                  ? 'Upgrade to Builder for 5 tanks and 50 compilations per month.'
                  : 'Upgrade to Pro for 15 tanks and 200 compilations per month.'}
              </p>
              <Link to="/upgrade">
                <button style={{ ...primaryButtonStyle, fontSize: 15, padding: '10px 28px' }}>
                  Upgrade Plan
                </button>
              </Link>
            </div>
          )}
        </>
      )}
    </Layout>
  );
}
