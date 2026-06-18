import { useEffect, useState } from 'react';
import { useNavigate, Link, useSearchParams } from 'react-router-dom';
import Layout from '../components/Layout';
import { listTanks, listAiTanks, forkTank, listGameDays } from '../services/api';
import { useAuthStore } from '../store/authStore';
import type { Tank, TankVersion, GameDay } from '../types';
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

function nextPhaseTime(gd: GameDay): string {
  const { phases, schedule } = gd;
  if (phases.roundRobin.status === 'upcoming') {
    return `Round Robin ${new Date(schedule.roundRobin).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}`;
  }
  if (phases.roundRobin.status === 'running') return 'Round Robin in progress';
  if (phases.final.status === 'upcoming') {
    return `Final ${new Date(schedule.final).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}`;
  }
  if (phases.final.status === 'running') return 'Final in progress';
  return 'Wrapping up';
}

function GameDayCard({ gd }: { gd: GameDay }) {
  const isFinal = gd.phases.final.status === 'complete';
  const isActive =
    gd.phases.roundRobin.status === 'running' ||
    gd.phases.final.status === 'running' ||
    Object.values(gd.phases.elimination ?? {}).some((p) => p.status === 'running');

  return (
    <div style={{ ...cardStyle, marginBottom: 24, borderColor: isActive ? '#4ade8040' : '#2d2d4e' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
            <span style={{
              fontSize: 11, fontWeight: 600, textTransform: 'uppercase', padding: '2px 8px', borderRadius: 4,
              color: isActive ? '#4ade80' : '#fbbf24',
              background: isActive ? 'rgba(74,222,128,0.1)' : 'rgba(251,191,36,0.1)',
              border: `1px solid ${isActive ? '#4ade80' : '#fbbf24'}`,
            }}>
              {isFinal ? 'complete' : isActive ? 'active' : 'upcoming'}
            </span>
            <span style={{ color: '#e2e8f0', fontSize: 15, fontWeight: 600 }}>
              {gd.name ? `${gd.name}` : 'Game Day'} — {new Date(gd.schedule.roundRobin).toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' })}
            </span>
          </div>
          {!isFinal && (
            <div style={{ fontSize: 12, color: '#64748b' }}>{nextPhaseTime(gd)}</div>
          )}
          {(gd.registeredTanks ?? []).length > 0 && (
            <div style={{ fontSize: 12, color: '#a78bfa', marginTop: 4 }}>
              {(gd.registeredTanks ?? []).length} tank{(gd.registeredTanks ?? []).length !== 1 ? 's' : ''} registered
            </div>
          )}
        </div>
        <Link
          to={`/gameday/${gd.gameDayId}`}
          style={{ ...ghostButtonStyle, textDecoration: 'none', display: 'inline-block', flexShrink: 0 }}
        >
          View
        </Link>
      </div>
    </div>
  );
}

type AiTank = Tank & { versions: TankVersion[] };

const AI_DESCRIPTIONS: Record<string, string> = {
  Scout:   'High speed, medium sensor range. Great starting point for mobility-focused builds.',
  Bruiser: 'High damage and armor, slow speed. Start here for a tanky, close-range strategy.',
  Ranger:  'Long sensor range, medium speed. Patrols the arena and fires with precision from a distance.',
  Randy:   'Balanced stats, unpredictable movement. Wanders randomly until an opponent is spotted, then pursues.',
};

function AiTankCard({ aiTank, onForked }: { aiTank: AiTank; onForked: () => void }) {
  const navigate = useNavigate();
  const [forking, setForking] = useState(false);
  const readyVersion = aiTank.versions.find((v) => v.compileStatus === 'ready');

  async function handleFork() {
    if (!readyVersion) return;
    setForking(true);
    try {
      const newTank = await forkTank(aiTank.tankId, readyVersion.version);
      onForked();
      navigate(`/tanks/${newTank.tankId}/edit`);
    } catch {
      setForking(false);
    }
  }

  return (
    <div style={{ ...cardStyle, flex: 1, minWidth: 220 }}>
      <div style={{ fontWeight: 700, fontSize: 15, color: '#e2e8f0', marginBottom: 6 }}>{aiTank.name}</div>
      <p style={{ margin: '0 0 14px', fontSize: 13, color: '#64748b', lineHeight: 1.5 }}>
        {AI_DESCRIPTIONS[aiTank.name] ?? ''}
      </p>
      <button
        onClick={handleFork}
        disabled={forking || !readyVersion}
        style={{ ...primaryButtonStyle, width: '100%' }}
      >
        {forking ? 'Forking…' : `Fork ${aiTank.name}`}
      </button>
    </div>
  );
}

export default function Dashboard() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const currentUser = useAuthStore((s) => s.user);
  // Admin-only: view another user's tanks via ?userId=
  const viewUserId = currentUser?.isAdmin ? (searchParams.get('userId') ?? undefined) : undefined;

  const [tanks, setTanks] = useState<Tank[]>([]);
  const [aiTanks, setAiTanks] = useState<AiTank[]>([]);
  const [runningGameDay, setRunningGameDay] = useState<GameDay | null>(null);
  const [upcomingGameDay, setUpcomingGameDay] = useState<GameDay | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  function reload() {
    listTanks(viewUserId)
      .then((data) => setTanks(data ?? []))
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }

  useEffect(() => {
    reload();
    listAiTanks().then((data) => setAiTanks(data ?? [])).catch(() => {});
    listGameDays()
      .then((days) => {
        const now = Date.now();
        const future = (days ?? []).filter(
          (d) => d.phases.final.status !== 'complete' && new Date(d.schedule.final).getTime() > now
        );
        const isRunning = (d: GameDay) =>
          d.phases.roundRobin.status === 'running' ||
          d.phases.final.status === 'running' ||
          Object.values(d.phases.elimination ?? {}).some((p) => p.status === 'running');
        setRunningGameDay(future.find(isRunning) ?? null);
        const upcoming = future
          .filter((d) => !isRunning(d))
          .sort((a, b) => new Date(a.schedule.roundRobin).getTime() - new Date(b.schedule.roundRobin).getTime());
        setUpcomingGameDay(upcoming[0] ?? null);
      })
      .catch(() => {});
  }, []);

  function handleNewTank() {
    navigate('/tanks/new/edit');
  }

  return (
    <Layout>
      {viewUserId && (
        <div style={{ ...cardStyle, marginBottom: 20, display: 'flex', alignItems: 'center', justifyContent: 'space-between', background: 'rgba(124,106,247,0.08)', border: '1px solid rgba(124,106,247,0.3)' }}>
          <span style={{ color: '#a78bfa', fontSize: 13 }}>
            Viewing tanks for user <code style={{ background: 'rgba(124,106,247,0.15)', padding: '2px 6px', borderRadius: 4 }}>{viewUserId}</code>
          </span>
          <Link to="/admin/users" style={{ color: '#7c6af7', fontSize: 13 }}>← Back to Users</Link>
        </div>
      )}
      {!viewUserId && runningGameDay && <GameDayCard gd={runningGameDay} />}
      {!viewUserId && upcomingGameDay && <GameDayCard gd={upcomingGameDay} />}
      {!viewUserId && aiTanks.length > 0 && (
        <div style={{ marginBottom: 36 }}>
          <h3 style={{ margin: '0 0 12px', fontSize: 14, fontWeight: 600, color: '#64748b', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
            Start from a template
          </h3>
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
            {aiTanks.map((ai) => (
              <AiTankCard key={ai.tankId} aiTank={ai} onForked={reload} />
            ))}
          </div>
        </div>
      )}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 28 }}>
        <h2 style={{ margin: 0, fontSize: 22, color: '#e2e8f0' }}>{viewUserId ? 'Tanks' : 'My Tanks'}</h2>
        {!viewUserId && (
          <button onClick={handleNewTank} style={primaryButtonStyle}>
            + New Tank
          </button>
        )}
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
            {viewUserId ? 'This user has no tanks.' : "You haven't created any tanks yet."}
          </p>
          {!viewUserId && (
            <button onClick={handleNewTank} style={primaryButtonStyle}>
              + New Tank
            </button>
          )}
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
