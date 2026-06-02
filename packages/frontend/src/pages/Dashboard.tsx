import { useEffect, useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import Layout from '../components/Layout';
import { listTanks, createTank } from '../services/api';
import type { Tank } from '../types';
import { cardStyle, primaryButtonStyle, ghostButtonStyle } from '../components/Layout';

function relativeTime(ts: number | null): string {
  if (!ts) return '—';
  const diffMs = Date.now() - ts * 1000;
  const days = Math.floor(diffMs / 86400000);
  if (days === 0) return 'today';
  if (days === 1) return '1 day ago';
  if (days < 30) return `${days} days ago`;
  const months = Math.floor(days / 30);
  if (months === 1) return '1 month ago';
  if (months < 12) return `${months} months ago`;
  const years = Math.floor(months / 12);
  return years === 1 ? '1 year ago' : `${years} years ago`;
}

function formatDate(ts: number): string {
  return new Date(ts * 1000).toLocaleDateString('en-US', {
    month: 'short', day: 'numeric', year: 'numeric',
  });
}

function ordinal(n: number): string {
  const s = ['th', 'st', 'nd', 'rd'];
  const v = n % 100;
  return n + (s[(v - 20) % 10] ?? s[v] ?? s[0]);
}

interface TankCardProps {
  tank: Tank;
}

function TankCard({ tank }: TankCardProps) {
  const hasStats = tank.gameDaysCount > 0;

  return (
    <div style={{ ...cardStyle, display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, marginBottom: 6 }}>
          <h3 style={{ margin: 0, fontSize: 17, color: '#e2e8f0' }}>
            {tank.name || <span style={{ color: '#64748b' }}>Unnamed Tank</span>}
          </h3>
          {tank.scoreTransferredFrom && (
            <span style={badgeStyle('#4a3f8a', '#a78bfa')}>score transferred</span>
          )}
          {tank.forkedFromTankId && (
            <span style={badgeStyle('#2d3748', '#94a3b8')}>fork</span>
          )}
        </div>

        {hasStats ? (
          <div style={{ display: 'flex', gap: 24, flexWrap: 'wrap', marginBottom: 8 }}>
            <Stat label="Score" value={String(tank.globalScore)} highlight />
            <Stat label="Best finish" value={tank.bestFinish != null ? ordinal(tank.bestFinish) : '—'} />
            <Stat label="Game Days" value={String(tank.gameDaysCount)} />
            <Stat label="Last active" value={relativeTime(tank.lastActiveAt)} />
          </div>
        ) : (
          <p style={{ margin: '0 0 8px', color: '#64748b', fontSize: 13 }}>
            No ranked matches yet
          </p>
        )}

        <span style={{ fontSize: 12, color: '#475569' }}>
          Created {formatDate(tank.createdAt)}
          {tank.forkedFromTankId && ' · forked'}
        </span>
      </div>

      <div style={{ display: 'flex', gap: 8, marginLeft: 16, flexShrink: 0 }}>
        <Link to={`/tanks/${tank.tankId}`} style={{ ...ghostButtonStyle, textDecoration: 'none', display: 'inline-block' }}>
          Details
        </Link>
        <Link to={`/tanks/${tank.tankId}/edit`} style={{ ...primaryButtonStyle, textDecoration: 'none', display: 'inline-block' }}>
          Edit
        </Link>
      </div>
    </div>
  );
}

function Stat({ label, value, highlight }: { label: string; value: string; highlight?: boolean }) {
  return (
    <div>
      <div style={{ fontSize: 11, color: '#64748b', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 2 }}>
        {label}
      </div>
      <div style={{ fontSize: 15, fontWeight: 600, color: highlight ? '#a78bfa' : '#cbd5e1' }}>
        {value}
      </div>
    </div>
  );
}

function badgeStyle(bg: string, color: string): React.CSSProperties {
  return { background: bg, color, fontSize: 11, padding: '2px 7px', borderRadius: 4, fontWeight: 500 };
}

export default function Dashboard() {
  const navigate = useNavigate();
  const [tanks, setTanks] = useState<Tank[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  useEffect(() => {
    listTanks()
      .then(setTanks)
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }, []);

  async function handleNewTank() {
    setCreating(true);
    try {
      const tank = await createTank();
      navigate(`/tanks/${tank.tankId}/edit`);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to create tank');
      setCreating(false);
    }
  }

  return (
    <Layout>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 28 }}>
        <h2 style={{ margin: 0, fontSize: 22, color: '#e2e8f0' }}>My Tanks</h2>
        <button onClick={handleNewTank} disabled={creating} style={primaryButtonStyle}>
          {creating ? 'Creating…' : '+ New Tank'}
        </button>
      </div>

      {loading && (
        <p style={{ color: '#64748b' }}>Loading…</p>
      )}

      {error && (
        <div style={{ ...cardStyle, borderColor: '#7f1d1d', color: '#fca5a5', marginBottom: 16 }}>
          {error}
        </div>
      )}

      {!loading && !error && tanks.length === 0 && (
        <div style={{ ...cardStyle, textAlign: 'center', padding: '48px 24px' }}>
          <p style={{ color: '#64748b', marginBottom: 16 }}>
            You haven't created any tanks yet.
          </p>
          <button onClick={handleNewTank} disabled={creating} style={primaryButtonStyle}>
            {creating ? 'Creating…' : '+ New Tank'}
          </button>
        </div>
      )}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        {tanks.map((tank) => (
          <TankCard key={tank.tankId} tank={tank} />
        ))}
      </div>
    </Layout>
  );
}
