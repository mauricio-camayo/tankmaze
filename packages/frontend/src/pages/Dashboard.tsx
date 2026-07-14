import { useEffect, useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import Layout from '../components/Layout';
import { listTanks, listAiTanks, forkTank, listGameDays, getMySettings } from '../services/api';
import { useAuthStore } from '../store/authStore';
import type { Tank, TankVersion, GameDay, UserSettings } from '../types';
import { cardStyle, primaryButtonStyle, ghostButtonStyle } from '../components/Layout';
import { avatarSrc } from '../components/AvatarPicker';

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
      <img
        src={avatarSrc(tank.tankId, tank.avatarUrl)}
        alt=""
        style={{ width: 40, height: 40, borderRadius: 0, imageRendering: 'pixelated', border: '2px solid #23577a', flexShrink: 0, marginRight: 12 }}
      />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, marginBottom: 6 }}>
          <h3 style={{ margin: 0, fontSize: 17, color: '#e7f1f7' }}>
            {tank.name || <span style={{ color: '#5b87a3' }}>Unnamed Tank</span>}
          </h3>
          {tank.scoreTransferredFrom && (
            <span style={badgeStyle('#4a2a12', '#ffab6b')}>score transferred</span>
          )}
          {tank.forkedFromTankId && (
            <span style={badgeStyle('#1c4a63', '#7fa2ba')}>fork</span>
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
          <p style={{ margin: '0 0 8px', color: '#5b87a3', fontSize: 13 }}>
            No ranked matches yet
          </p>
        )}

        <span style={{ fontSize: 12, color: '#4a7291' }}>
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
      <div style={{ fontSize: 11, color: '#5b87a3', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 2 }}>
        {label}
      </div>
      <div style={{ fontSize: 15, fontWeight: 600, color: highlight ? '#ffab6b' : '#a8c4d6' }}>
        {value}
      </div>
    </div>
  );
}

function badgeStyle(bg: string, color: string): React.CSSProperties {
  return { background: bg, color, fontSize: 11, padding: '2px 7px', borderRadius: 0, fontWeight: 500 };
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
    <div style={{ ...cardStyle, marginBottom: 24, borderColor: isActive ? '#59e6c040' : '#23577a' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
            <span style={{
              fontSize: 11, fontWeight: 600, textTransform: 'uppercase', padding: '2px 8px', borderRadius: 0,
              color: isActive ? '#59e6c0' : '#e8b339',
              background: isActive ? 'rgba(74,222,128,0.1)' : 'rgba(251,191,36,0.1)',
              border: `1px solid ${isActive ? '#59e6c0' : '#e8b339'}`,
            }}>
              {isFinal ? 'complete' : isActive ? 'active' : 'upcoming'}
            </span>
            <span style={{ color: '#e7f1f7', fontSize: 15, fontWeight: 600 }}>
              {gd.name ? `${gd.name}` : 'Game Day'} — {new Date(gd.schedule.roundRobin).toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' })}
            </span>
          </div>
          {!isFinal && (
            <div style={{ fontSize: 12, color: '#5b87a3' }}>{nextPhaseTime(gd)}</div>
          )}
          {(gd.registeredTanks ?? []).length > 0 && (
            <div style={{ fontSize: 12, color: '#ffab6b', marginTop: 4 }}>
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

const STAT_LABELS: Array<{ key: keyof TankVersion['config']; label: string }> = [
  { key: 'speed',       label: 'Speed'    },
  { key: 'sensorRange', label: 'Sensor'   },
  { key: 'damage',      label: 'Damage'   },
  { key: 'armor',       label: 'Armor'    },
  { key: 'fireRate',    label: 'Fire Rate'},
];

function StatPips({ value, max = 5 }: { value: number; max?: number }) {
  return (
    <div style={{ display: 'flex', gap: 3 }}>
      {Array.from({ length: max }).map((_, i) => (
        <div key={i} style={{
          width: 10, height: 10, borderRadius: 0,
          background: i < value ? '#ff7a29' : '#23577a',
        }} />
      ))}
    </div>
  );
}

function AiTankInfoModal({ aiTank, onFork, onClose, forking }: {
  aiTank: AiTank;
  onFork: () => void;
  onClose: () => void;
  forking: boolean;
}) {
  const readyVersion = aiTank.versions.find((v) => v.compileStatus === 'ready');
  return (
    <div style={{
      position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)',
      display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 100,
    }} onClick={onClose}>
      <div style={{ ...cardStyle, width: 340, maxWidth: '90vw' }} onClick={(e) => e.stopPropagation()}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 12 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <img
              src={avatarSrc(aiTank.tankId, aiTank.avatarUrl)}
              alt=""
              style={{ width: 40, height: 40, borderRadius: 0, imageRendering: 'pixelated', border: '1px solid #23577a', flexShrink: 0 }}
            />
            <div style={{ fontWeight: 700, fontSize: 17, color: '#e7f1f7' }}>{aiTank.name}</div>
          </div>
          <button onClick={onClose} style={{ background: 'none', border: 'none', color: '#5b87a3', cursor: 'pointer', fontSize: 18, lineHeight: 1 }}>×</button>
        </div>
        <p style={{ margin: '0 0 16px', fontSize: 13, color: '#7fa2ba', lineHeight: 1.5 }}>
          {AI_DESCRIPTIONS[aiTank.name] ?? ''}
        </p>
        {readyVersion && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 20 }}>
            {STAT_LABELS.map(({ key, label }) => (
              <div key={key} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                <span style={{ fontSize: 12, color: '#5b87a3', width: 70 }}>{label}</span>
                <StatPips value={readyVersion.config[key] as number} />
              </div>
            ))}
          </div>
        )}
        <button
          onClick={onFork}
          disabled={forking || !readyVersion}
          style={{ ...primaryButtonStyle, width: '100%' }}
        >
          {forking ? 'Forking…' : `Fork ${aiTank.name}`}
        </button>
      </div>
    </div>
  );
}

function AiTemplateRow({ aiTanks, onForked }: { aiTanks: AiTank[]; onForked: () => void }) {
  const navigate = useNavigate();
  const [selected, setSelected] = useState<string | null>(null);
  const [modal, setModal]       = useState<string | null>(null);
  const [forking, setForking]   = useState(false);

  const selectedTank = aiTanks.find((t) => t.tankId === selected);
  const modalTank    = aiTanks.find((t) => t.tankId === modal);

  async function handleFork(aiTank: AiTank) {
    const readyVersion = aiTank.versions.find((v) => v.compileStatus === 'ready');
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
    <>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        {aiTanks.map((ai) => {
          const isSelected = selected === ai.tankId;
          return (
            <div key={ai.tankId} style={{ display: 'flex', alignItems: 'center', gap: 0 }}>
              <button
                onClick={() => setSelected(isSelected ? null : ai.tankId)}
                style={{
                  background: isSelected ? 'rgba(124,106,247,0.15)' : '#082e4a',
                  border: `1px solid ${isSelected ? '#ff7a29' : '#23577a'}`,
                  borderRadius: '6px 0 0 6px',
                  color: isSelected ? '#ffab6b' : '#a8c4d6',
                  padding: '4px 12px 4px 6px',
                  fontSize: 13, fontWeight: 600, cursor: 'pointer',
                  display: 'flex', alignItems: 'center', gap: 6,
                }}
              >
                <img
                  src={avatarSrc(ai.tankId, ai.avatarUrl)}
                  alt=""
                  style={{ width: 22, height: 22, borderRadius: 0, imageRendering: 'pixelated', flexShrink: 0 }}
                />
                {ai.name}
              </button>
              <button
                onClick={() => setModal(ai.tankId)}
                title={`About ${ai.name}`}
                style={{
                  background: isSelected ? 'rgba(124,106,247,0.10)' : '#082e4a',
                  border: `1px solid ${isSelected ? '#ff7a29' : '#23577a'}`,
                  borderLeft: 'none',
                  borderRadius: '0 6px 6px 0',
                  color: '#5b87a3', padding: '6px 8px',
                  fontSize: 12, cursor: 'pointer',
                }}
              >
                ⓘ
              </button>
            </div>
          );
        })}
        <button
          onClick={() => selectedTank && handleFork(selectedTank)}
          disabled={!selected || forking}
          style={{
            ...primaryButtonStyle,
            opacity: !selected || forking ? 0.45 : 1,
            cursor: !selected || forking ? 'not-allowed' : 'pointer',
          }}
        >
          {forking ? 'Forking…' : 'Fork selected template'}
        </button>
      </div>

      {modalTank && (
        <AiTankInfoModal
          aiTank={modalTank}
          forking={forking}
          onFork={() => { handleFork(modalTank); setModal(null); }}
          onClose={() => setModal(null)}
        />
      )}
    </>
  );
}

export default function Dashboard() {
  const navigate = useNavigate();
  const currentUser = useAuthStore((s) => s.user);

  const [tanks, setTanks] = useState<Tank[]>([]);
  const [aiTanks, setAiTanks] = useState<AiTank[]>([]);
  const [runningGameDay, setRunningGameDay] = useState<GameDay | null>(null);
  const [upcomingGameDay, setUpcomingGameDay] = useState<GameDay | null>(null);
  const [settings, setSettings] = useState<UserSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  function reload() {
    listTanks()
      .then((data) => setTanks(data ?? []))
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }

  useEffect(() => {
    reload();
    getMySettings().then(setSettings).catch(() => {});
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

  const atTankLimit = settings !== null && tanks.length >= settings.tankLimit;

  function handleNewTank() {
    navigate('/tanks/new/edit');
  }

  return (
    <Layout>
      {runningGameDay && <GameDayCard gd={runningGameDay} />}
      {upcomingGameDay && <GameDayCard gd={upcomingGameDay} />}
      {aiTanks.length > 0 && (
        <div style={{ marginBottom: 36 }}>
          <h3 style={{ margin: '0 0 12px', fontSize: 14, fontWeight: 600, color: '#5b87a3', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
            Start from a template
          </h3>
          <AiTemplateRow aiTanks={aiTanks} onForked={reload} />
        </div>
      )}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 28 }}>
        <h2 style={{ margin: 0, fontSize: 22, color: '#e7f1f7' }}>My Tanks</h2>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          {atTankLimit && (
            <span style={{ fontSize: 13, color: '#e8b339' }}>
              Tank limit reached ({tanks.length}/{settings?.tankLimit})
            </span>
          )}
          <button onClick={handleNewTank} disabled={atTankLimit} style={{ ...primaryButtonStyle, opacity: atTankLimit ? 0.5 : 1, cursor: atTankLimit ? 'not-allowed' : 'pointer' }}>
            + New Tank
          </button>
        </div>
      </div>

      {loading && (
        <p style={{ color: '#5b87a3' }}>Loading…</p>
      )}

      {error && (
        <div style={{ ...cardStyle, borderColor: '#3a1a18', color: '#ffb8a3', marginBottom: 16 }}>
          {error}
        </div>
      )}

      {!loading && !error && tanks.length === 0 && (
        <div style={{ ...cardStyle, textAlign: 'center', padding: '48px 24px' }}>
          <p style={{ color: '#5b87a3', marginBottom: 16 }}>
            You haven't created any tanks yet.
          </p>
          <button onClick={handleNewTank} disabled={atTankLimit} style={{ ...primaryButtonStyle, opacity: atTankLimit ? 0.5 : 1, cursor: atTankLimit ? 'not-allowed' : 'pointer' }}>
            + New Tank
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
