import { useEffect, useState } from 'react';
import { useParams, Link, useNavigate } from 'react-router-dom';
import Layout, { cardStyle, primaryButtonStyle, ghostButtonStyle } from '../components/Layout';
import { getTank, deleteTank, withdrawRegistration, getRankings, listGameDays, registerForGameDay, startMatch, listMaps, type OpponentSpec } from '../services/api';
import type { Tank, TankVersion, GameDay, GameMap } from '../types';
import ForkDialog from '../components/ForkDialog';
import { useAuthStore } from '../store/authStore';

function majorOf(version: string): string {
  const m = version.match(/^(v\d+)/);
  return m ? m[1] : version;
}

function isMajor(v: string): boolean {
  return /^v\d+$/.test(v);
}

function pct(n: number | null): string {
  if (n === null) return '0%';
  return `${Math.round(n * 100)}%`;
}

function num(n: number | null, decimals = 0): string {
  if (n === null) return '0';
  return decimals > 0 ? n.toFixed(decimals) : String(Math.round(n));
}

function ordinal(n: number): string {
  const s = ['th', 'st', 'nd', 'rd'];
  const v = n % 100;
  return `${n}${s[(v - 20) % 10] ?? s[v] ?? s[0]}`;
}

function relativeTime(ts: number): string {
  const diff = Date.now() / 1000 - ts;
  if (diff < 60) return 'just now';
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}

function StatusDot({ status }: { status: TankVersion['compileStatus'] }) {
  const colors: Record<string, string> = {
    ready: '#4ade80', pending: '#fbbf24', compiling: '#60a5fa', failed: '#f87171',
  };
  return (
    <span style={{
      display: 'inline-block', width: 8, height: 8, borderRadius: '50%',
      background: colors[status] ?? '#94a3b8', flexShrink: 0,
    }} />
  );
}

function StatPips({ value }: { value: number }) {
  return (
    <div style={{ display: 'flex', gap: 2 }}>
      {Array.from({ length: 5 }).map((_, i) => (
        <div key={i} style={{
          width: 8, height: 8, borderRadius: 2,
          background: i < value ? '#7c6af7' : '#2d2d4e',
        }} />
      ))}
    </div>
  );
}

type TestOpponent = 'scout' | 'bruiser';

const overlay: React.CSSProperties = {
  position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)',
  display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 100,
};

function GameDayPickerModal({
  gameDays, loading, onSelect, onClose,
}: {
  gameDays: GameDay[];
  loading: boolean;
  onSelect: (gameDayId: string) => void;
  onClose: () => void;
}) {
  const now = new Date();
  const sorted = [...gameDays].sort((a, b) => {
    return a.schedule.registrationClose < b.schedule.registrationClose ? -1 : 1;
  });
  return (
    <div style={overlay}>
      <div style={{ ...cardStyle, width: 440, maxHeight: '80vh', overflowY: 'auto' }}>
        <h3 style={{ margin: '0 0 16px', color: '#e2e8f0' }}>Select Game Day</h3>
        {loading ? (
          <p style={{ color: '#64748b', fontSize: 13, margin: '0 0 16px' }}>Loading game days…</p>
        ) : sorted.length === 0 ? (
          <p style={{ color: '#64748b', fontSize: 13, margin: '0 0 16px' }}>No game days programmed yet.</p>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 16 }}>
            {sorted.map((gd) => {
              const isExpired = new Date(gd.schedule.registrationClose) < now || gd.phases.roundRobin.status !== 'upcoming';
              const regClose = new Date(gd.schedule.registrationClose).toLocaleDateString(undefined, {
                year: 'numeric', month: 'short', day: 'numeric',
              });
              const final = new Date(gd.schedule.final).toLocaleDateString(undefined, {
                year: 'numeric', month: 'short', day: 'numeric',
              });
              return (
                <button
                  key={gd.gameDayId}
                  onClick={isExpired ? undefined : () => onSelect(gd.gameDayId)}
                  disabled={isExpired}
                  style={{
                    background: '#1a1a2e', border: '1px solid #2d2d4e', borderRadius: 6,
                    color: isExpired ? '#4a5568' : '#e2e8f0',
                    padding: '10px 14px', textAlign: 'left',
                    cursor: isExpired ? 'default' : 'pointer',
                    display: 'flex', flexDirection: 'column', gap: 4,
                    opacity: isExpired ? 0.6 : 1,
                  }}
                >
                  <span style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 14, fontWeight: 600 }}>
                    {gd.name ?? final}
                    {isExpired && (
                      <span style={{ fontSize: 11, fontWeight: 400, color: '#64748b', background: '#2d2d4e', borderRadius: 4, padding: '1px 6px' }}>
                        Completed
                      </span>
                    )}
                  </span>
                  <span style={{ fontSize: 12, color: '#64748b' }}>
                    Registration closes {regClose} · Final {final}
                  </span>
                </button>
              );
            })}
          </div>
        )}
        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <button onClick={onClose} style={ghostButtonStyle}>Cancel</button>
        </div>
      </div>
    </div>
  );
}

function TestDialog({
  maps, loadingMaps, onTest, onClose,
}: {
  maps: GameMap[];
  loadingMaps: boolean;
  onTest: (opponent: TestOpponent, mapId: string | null) => void;
  onClose: () => void;
}) {
  const [opponent, setOpponent] = useState<TestOpponent>('scout');
  const [mapId, setMapId] = useState<string | null>(() => localStorage.getItem('tankmaze:lastMapId') ?? null);

  function selectMap(id: string | null) {
    setMapId(id);
    if (id === null) localStorage.removeItem('tankmaze:lastMapId');
    else localStorage.setItem('tankmaze:lastMapId', id);
  }

  return (
    <div style={overlay}>
      <div style={{ ...cardStyle, width: 420, maxHeight: '80vh', overflowY: 'auto' }}>
        <h3 style={{ margin: '0 0 16px', color: '#e2e8f0' }}>Test vs AI</h3>
        <div style={{ marginBottom: 16 }}>
          <p style={{ margin: '0 0 8px', fontSize: 13, color: '#94a3b8' }}>Opponent</p>
          {(['scout', 'bruiser'] as TestOpponent[]).map((op) => (
            <label key={op} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, cursor: 'pointer' }}>
              <input type="radio" name="td-opponent" value={op} checked={opponent === op} onChange={() => setOpponent(op)} />
              <span style={{ color: '#e2e8f0', textTransform: 'capitalize', fontSize: 14 }}>{op}</span>
            </label>
          ))}
        </div>
        <div style={{ marginBottom: 20 }}>
          <p style={{ margin: '0 0 8px', fontSize: 13, color: '#94a3b8' }}>Map</p>
          {loadingMaps ? (
            <span style={{ color: '#64748b', fontSize: 13 }}>Loading maps…</span>
          ) : (
            <>
              <label style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, cursor: 'pointer' }}>
                <input type="radio" name="td-map" value="" checked={mapId === null} onChange={() => selectMap(null)} />
                <span style={{ color: '#e2e8f0', fontSize: 14 }}>Random (default)</span>
              </label>
              {maps.map((m) => (
                <label key={m.mapId} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, cursor: 'pointer' }}>
                  <input type="radio" name="td-map" value={m.mapId} checked={mapId === m.mapId} onChange={() => selectMap(m.mapId)} />
                  <span style={{ color: '#e2e8f0', fontSize: 14 }}>{m.name}</span>
                  <span style={{ color: '#64748b', fontSize: 12 }}>{m.description}</span>
                </label>
              ))}
            </>
          )}
        </div>
        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <button onClick={onClose} style={ghostButtonStyle}>Cancel</button>
          <button onClick={() => onTest(opponent, mapId)} style={primaryButtonStyle}>Launch Match</button>
        </div>
      </div>
    </div>
  );
}

function MajorVersionCard({ major, minors, isOwner }: { major: TankVersion; minors: TankVersion[]; isOwner: boolean }) {
  const [expanded, setExpanded] = useState(false);

  return (
    <div style={{ ...cardStyle, marginBottom: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 14 }}>
        <div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
            <span style={{ color: '#a78bfa', fontWeight: 700, fontSize: 15 }}>{major.version}</span>
            {isOwner && <StatusDot status={major.compileStatus} />}
            {isOwner && <span style={{ color: '#64748b', fontSize: 12 }}>{major.compileStatus}</span>}
            {major.disqualified && (
              <span style={{ background: '#f87171', color: '#fff', fontSize: 10, padding: '1px 6px', borderRadius: 4, fontWeight: 600 }}>DQ</span>
            )}
            {(major.registeredForGameDays?.length ?? 0) > 0 && (
              <span style={{ background: '#4ade80', color: '#0f0f1a', fontSize: 10, padding: '1px 6px', borderRadius: 4, fontWeight: 600 }}>
                REGISTERED{(major.registeredForGameDays!.length > 1) ? ` ×${major.registeredForGameDays!.length}` : ''}
              </span>
            )}
          </div>
          <div style={{ color: '#64748b', fontSize: 12 }}>
            {relativeTime(major.createdAt)} · {new Date(major.createdAt * 1000).toLocaleDateString()}
          </div>
        </div>

        <div style={{ display: 'grid', gridTemplateColumns: 'auto auto', gap: '2px 16px', fontSize: 13 }}>
            <span style={{ color: '#64748b' }}>Win rate</span>
            <span style={{ color: '#4ade80', fontWeight: 600, textAlign: 'right' }}>{pct(major.winRate)}</span>
            <span style={{ color: '#64748b' }}>Matches</span>
            <span style={{ color: '#e2e8f0', textAlign: 'right' }}>{num(major.matchesPlayed)}</span>
            <span style={{ color: '#64748b' }}>Avg damage</span>
            <span style={{ color: '#e2e8f0', textAlign: 'right' }}>{num(major.avgDamageDealt, 1)}</span>
            <span style={{ color: '#64748b' }}>Avg survival</span>
            <span style={{ color: '#e2e8f0', textAlign: 'right' }}>{num(major.avgSurvivalTicks)} tks</span>
          </div>
      </div>

      <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' }}>
        {(['speed', 'sensorRange', 'damage', 'armor', 'fireRate'] as const).map((stat) => (
          <div key={stat} style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            <span style={{ color: '#64748b', fontSize: 10, textTransform: 'uppercase', letterSpacing: '0.05em' }}>
              {stat === 'sensorRange' ? 'Sensor' : stat === 'fireRate' ? 'Fire' : stat.charAt(0).toUpperCase() + stat.slice(1)}
            </span>
            <StatPips value={major.config[stat]} />
          </div>
        ))}
      </div>

      {minors.length > 0 && (
        <div style={{ marginTop: 14, borderTop: '1px solid #2d2d4e', paddingTop: 12 }}>
          <button
            onClick={() => setExpanded((e) => !e)}
            style={{ background: 'none', border: 'none', color: '#64748b', cursor: 'pointer', fontSize: 12, padding: 0 }}
          >
            {expanded ? '▾' : '▸'} {minors.length} minor version{minors.length !== 1 ? 's' : ''}
          </button>
          {expanded && (
            <div style={{ marginTop: 8, display: 'flex', flexDirection: 'column', gap: 4 }}>
              {minors.map((mv) => (
                <div key={mv.version} style={{
                  display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                  padding: '5px 10px', borderRadius: 6, background: '#0f0f1a',
                }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    <StatusDot status={mv.compileStatus} />
                    <span style={{ color: '#94a3b8', fontSize: 12, marginLeft: 4 }}>{mv.version}</span>
                    {mv.compileStatus === 'failed' && (
                      <span style={{ color: '#f87171', fontSize: 11 }}>compile failed</span>
                    )}
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                    {isOwner && (mv.testMatchCount ?? 0) > 0 && (
                      <span style={{ color: '#475569', fontSize: 11 }}>
                        {mv.testMatchCount} test{mv.testMatchCount !== 1 ? 's' : ''}
                      </span>
                    )}
                    <span style={{ color: '#475569', fontSize: 11 }}>
                      {new Date(mv.createdAt * 1000).toLocaleDateString()}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

const PAGE_SIZE = 10;

function gameDayResult(gd: GameDay, tankId: string): string {
  if (gd.phases.final.status === 'complete') {
    const finalSlots = gd.bracket['final'] ?? [];
    if (finalSlots.find((s) => s.tankId === tankId && s.status === 'won')) return 'Winner';
    if (finalSlots.find((s) => s.tankId === tankId)) return 'Finalist';
    const elimKeys = Object.keys(gd.bracket)
      .filter((k) => /^r\d+$/.test(k))
      .sort((a, b) => parseInt(b.slice(1)) - parseInt(a.slice(1)));
    for (const k of elimKeys) {
      const slot = (gd.bracket[k] ?? []).find((s) => s.tankId === tankId);
      if (slot && (slot.status === 'lost' || slot.status === 'both_lose')) {
        return `Eliminated in ${k.toUpperCase()}`;
      }
    }
  }
  if (gd.phases.roundRobin.status === 'complete') return 'Round Robin only';
  return '—';
}

function GameDayHistory({ allGameDays, tankId }: { allGameDays: GameDay[]; tankId: string }) {
  const [expanded, setExpanded] = useState(false);
  const [page, setPage] = useState(0);

  const pastGds = allGameDays.filter(
    (gd) => gd.phases.roundRobin.status !== 'upcoming',
  ).sort((a, b) => new Date(b.schedule.roundRobin).getTime() - new Date(a.schedule.roundRobin).getTime());

  if (pastGds.length === 0) return null;

  const totalPages = Math.ceil(pastGds.length / PAGE_SIZE);
  const pageItems = pastGds.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE);

  return (
    <div style={{ ...cardStyle, marginTop: 24 }}>
      <button
        onClick={() => setExpanded((e) => !e)}
        style={{ background: 'none', border: 'none', color: '#94a3b8', cursor: 'pointer', fontSize: 14, fontWeight: 600, padding: 0, width: '100%', textAlign: 'left', display: 'flex', alignItems: 'center', gap: 8 }}
      >
        {expanded ? '▾' : '▸'} Game Day History ({pastGds.length})
      </button>
      {expanded && (
        <div style={{ marginTop: 14 }}>
          {pageItems.map((gd) => {
            const date = new Date(gd.schedule.roundRobin).toLocaleDateString(undefined, {
              month: 'short', day: 'numeric', year: 'numeric',
            });
            const result = gameDayResult(gd, tankId);
            const resultColor = result === 'Winner' ? '#4ade80' : result === 'Finalist' ? '#fbbf24' : '#64748b';
            return (
              <div key={gd.gameDayId} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 0', borderBottom: '1px solid #2d2d4e' }}>
                <div>
                  <Link to={`/gameday/${gd.gameDayId}`} style={{ color: '#a78bfa', textDecoration: 'none', fontSize: 14, fontWeight: 500 }}>
                    {gd.name ?? date}
                  </Link>
                  <div style={{ color: '#64748b', fontSize: 12, marginTop: 2 }}>{date}</div>
                </div>
                <span style={{ color: resultColor, fontSize: 13, fontWeight: result === 'Winner' || result === 'Finalist' ? 600 : 400 }}>
                  {result}
                </span>
              </div>
            );
          })}
          {totalPages > 1 && (
            <div style={{ display: 'flex', gap: 8, justifyContent: 'center', marginTop: 12 }}>
              <button
                onClick={() => setPage((p) => p - 1)}
                disabled={page === 0}
                style={{ ...ghostButtonStyle, padding: '3px 10px', fontSize: 12, opacity: page === 0 ? 0.4 : 1 }}
              >
                Prev
              </button>
              <span style={{ color: '#64748b', fontSize: 12, alignSelf: 'center' }}>{page + 1} / {totalPages}</span>
              <button
                onClick={() => setPage((p) => p + 1)}
                disabled={page >= totalPages - 1}
                style={{ ...ghostButtonStyle, padding: '3px 10px', fontSize: 12, opacity: page >= totalPages - 1 ? 0.4 : 1 }}
              >
                Next
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

export default function TankDetail() {
  const { tankId } = useParams<{ tankId: string }>();
  const navigate = useNavigate();
  const currentUser = useAuthStore((s) => s.user);
  const [tank, setTank] = useState<(Tank & { versions: TankVersion[] }) | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [rank, setRank] = useState<number | null>(null);
  const [showFork, setShowFork] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [showDeregisterConfirm, setShowDeregisterConfirm] = useState(false);
  const [gameDayLabel, setGameDayLabel] = useState<string | null>(null);
  // Register for Game Day
  const [showGameDayPicker, setShowGameDayPicker] = useState(false);
  const [gameDays, setGameDays] = useState<GameDay[]>([]);
  const [loadingGameDays, setLoadingGameDays] = useState(false);
  const [registering, setRegistering] = useState(false);
  const [registerError, setRegisterError] = useState<string | null>(null);
  // Test vs AI
  const [showTestDialog, setShowTestDialog] = useState(false);
  const [maps, setMaps] = useState<GameMap[]>([]);
  const [loadingMaps, setLoadingMaps] = useState(false);

  useEffect(() => {
    if (!tankId) return;
    getTank(tankId)
      .then(setTank)
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
    getRankings()
      .then((entries) => {
        const entry = entries.find((r) => r.tankId === tankId);
        if (entry) setRank(entry.rank);
      })
      .catch(() => { /* rank unavailable — leave null */ });
  }, [tankId]);

  // Pre-fetch game days when the tank is already registered, so Withdraw button labels
  // can show the human-readable name instead of a UUID fragment.
  useEffect(() => {
    if (!tankId) return;
    const ids = tank?.versions.flatMap((v) => v.registeredForGameDays ?? []) ?? [];
    if (ids.length > 0 && gameDays.length === 0) {
      setLoadingGameDays(true);
      listGameDays().then(setGameDays).catch(() => setGameDays([])).finally(() => setLoadingGameDays(false));
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tank]);

  async function openRegisterPicker() {
    setShowGameDayPicker(true);
    if (gameDays.length === 0) {
      setLoadingGameDays(true);
      listGameDays().then(setGameDays).catch(() => setGameDays([])).finally(() => setLoadingGameDays(false));
    }
  }

  async function handleRegister(gameDayId: string) {
    if (!tankId || !latestReadyMajorForActions) return;
    setShowGameDayPicker(false);
    setRegistering(true);
    setRegisterError(null);
    try {
      await registerForGameDay(tankId, latestReadyMajorForActions.version, gameDayId);
      const updated = await getTank(tankId);
      setTank(updated);
    } catch (e) {
      setRegisterError(e instanceof Error ? e.message : 'Registration failed');
    } finally {
      setRegistering(false);
    }
  }

  async function handleWithdraw(gameDayId: string) {
    if (!tankId || !latestReadyMajorForActions) return;
    setRegistering(true);
    setRegisterError(null);
    try {
      await withdrawRegistration(tankId, latestReadyMajorForActions.version, gameDayId);
      const updated = await getTank(tankId);
      setTank(updated);
    } catch (e) {
      setRegisterError(e instanceof Error ? e.message : 'Withdraw failed');
    } finally {
      setRegistering(false);
    }
  }

  async function openTestDialog() {
    setShowTestDialog(true);
    if (maps.length === 0) {
      setLoadingMaps(true);
      listMaps().then(setMaps).finally(() => setLoadingMaps(false));
    }
  }

  async function handleTest(opponent: TestOpponent, mapId: string | null) {
    if (!tankId || !latestReadyMajorForActions) return;
    try {
      const spec: OpponentSpec = { type: 'ai', name: opponent };
      const match = await startMatch(tankId, latestReadyMajorForActions.version, spec, mapId ?? undefined);
      navigate(`/watch?matchId=${match.matchId}`);
    } catch (e) {
      setRegisterError(e instanceof Error ? e.message : 'Failed to start match');
      setShowTestDialog(false);
    }
  }

  if (loading) {
    return <Layout><div style={{ color: '#64748b', padding: '40px 0' }}>Loading…</div></Layout>;
  }
  if (error || !tank) {
    return <Layout><div style={{ color: '#f87171' }}>{error ?? 'Tank not found'}</div></Layout>;
  }

  const isOwner = tank.userId === currentUser?.userId;

  const majors = tank.versions
    .filter((v) => isMajor(v.version))
    .sort((a, b) => b.createdAt - a.createdAt);

  const minorsByMajor: Record<string, TankVersion[]> = {};
  tank.versions
    .filter((v) => !isMajor(v.version))
    .sort((a, b) => b.createdAt - a.createdAt)
    .forEach((v) => {
      const parent = majorOf(v.version);
      (minorsByMajor[parent] ??= []).push(v);
    });

  const latestReadyMajor = majors.find((v) => v.compileStatus === 'ready');
  // For owner actions: a ready major version (compileStatus 'ready' or '' for public view).
  const latestReadyMajorForActions = majors.find((v) => v.compileStatus === 'ready' || v.compileStatus === '');
  const canRegister = isOwner && !!latestReadyMajorForActions;
  const isRegistered = canRegister && (latestReadyMajorForActions?.registeredForGameDays?.length ?? 0) > 0;
  const canTest = isOwner && !!latestReadyMajorForActions;

  return (
    <Layout>
      <div style={{ marginBottom: 20 }}>
        <Link to={isOwner ? '/dashboard' : '/leaderboard'} style={{ color: '#64748b', fontSize: 13, textDecoration: 'none' }}>
          {isOwner ? '← My tanks' : '← Leaderboard'}
        </Link>
      </div>

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 28 }}>
        <div>
          <h1 style={{ margin: '0 0 6px', color: '#e2e8f0', fontSize: 26, fontWeight: 700 }}>
            {tank.name}
          </h1>
          <div style={{ display: 'flex', gap: 16, alignItems: 'center', flexWrap: 'wrap', marginBottom: 10 }}>
            <span style={{ color: '#fbbf24', fontSize: 13, fontWeight: 600 }}>
              {rank !== null ? `#${rank}` : '#—'}
            </span>
            <span style={{ color: '#a78bfa', fontSize: 20, fontWeight: 700 }}>
              {tank.globalScore.toLocaleString()} pts
            </span>
            {tank.bestFinish !== null && (
              <span style={{ color: '#94a3b8', fontSize: 13 }}>Best: {ordinal(tank.bestFinish)}</span>
            )}
            <span style={{ color: '#64748b', fontSize: 13 }}>
              {tank.gameDaysCount} game {tank.gameDaysCount === 1 ? 'day' : 'days'}
            </span>
            {tank.createdAt > 0 && (
              <span style={{ color: '#64748b', fontSize: 13 }}
                title={new Date(tank.createdAt * 1000).toLocaleDateString()}>
                submitted {relativeTime(tank.createdAt)}
              </span>
            )}
          </div>

          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            {tank.forkedFromTankId && (
              <Link to={`/tanks/${tank.forkedFromTankId}`} style={{
                color: '#60a5fa', fontSize: 12, textDecoration: 'none',
                border: '1px solid #1d3855', borderRadius: 4, padding: '2px 8px', background: '#0d1f33',
              }}>
                ⑂ fork of {tank.forkedFromVersion ?? tank.forkedFromTankId.slice(-8)}
              </Link>
            )}
            {tank.scoreTransferredFrom && (
              <span style={{
                color: '#fbbf24', fontSize: 12,
                border: '1px solid #3b2f0a', borderRadius: 4, padding: '2px 8px', background: '#1c1506',
              }}>
                score transferred in
              </span>
            )}
            {tank.scoreTransferredTo && (
              <Link to={`/tanks/${tank.scoreTransferredTo}`} style={{
                color: '#94a3b8', fontSize: 12, textDecoration: 'none',
                border: '1px solid #2d2d4e', borderRadius: 4, padding: '2px 8px',
              }}>
                score transferred out →
              </Link>
            )}
          </div>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, flexShrink: 0, marginLeft: 16, alignItems: 'flex-end' }}>
          {/* Primary action row */}
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', justifyContent: 'flex-end' }}>
            {canTest && (
              <button onClick={openTestDialog} style={ghostButtonStyle}>
                Test vs AI
              </button>
            )}
            {canRegister && (
              <>
                {(latestReadyMajorForActions?.registeredForGameDays ?? []).length < 2 &&
                  (latestReadyMajorForActions?.registeredForGameDays ?? []).map((gdId) => {
                    const gd = gameDays.find((d) => d.gameDayId === gdId);
                    if (gd && gd.phases.roundRobin.status !== 'upcoming') return null;
                    const label = gd?.name ?? gdId.slice(-6);
                    return (
                      <button key={gdId} onClick={() => handleWithdraw(gdId)} disabled={registering} style={ghostButtonStyle}>
                        {registering ? '…' : `Withdraw · ${label}`}
                      </button>
                    );
                  })}
                <button onClick={openRegisterPicker} disabled={registering} style={ghostButtonStyle}>
                  {registering ? '…' : 'Register for Game Day'}
                </button>
              </>
            )}
            {latestReadyMajor && !tank.scoreTransferredTo && (
              <button onClick={() => setShowFork(true)} style={ghostButtonStyle}>Fork</button>
            )}
            {isOwner && (
              <button onClick={() => navigate(`/tanks/${tankId}/edit`)} style={primaryButtonStyle}>
                Edit
              </button>
            )}
            {isOwner && (confirmDelete ? (
              <>
                <span style={{ color: '#f87171', fontSize: 13 }}>Delete forever?</span>
                <button
                  onClick={async () => {
                    setDeleting(true);
                    try {
                      await deleteTank(tankId!);
                      navigate('/dashboard');
                    } catch (e) {
                      setDeleting(false);
                      setConfirmDelete(false);
                      if (e instanceof Error && e.message.startsWith('409')) {
                        setShowDeregisterConfirm(true);
                        const registeredGdIds = tank?.versions.flatMap(
                          (v) => v.registeredForGameDays ?? [],
                        ) ?? [];
                        if (registeredGdIds.length === 1) {
                          listGameDays().then((days) => {
                            const gd = days.find((d) => d.gameDayId === registeredGdIds[0]);
                            if (gd) {
                              const date = new Date(gd.schedule.registrationClose);
                              setGameDayLabel(date.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' }));
                            }
                          }).catch(() => { /* label stays null */ });
                        } else if (registeredGdIds.length > 1) {
                          setGameDayLabel(`${registeredGdIds.length} game days`);
                        }
                      } else {
                        alert(e instanceof Error ? e.message : 'Delete failed');
                      }
                    }
                  }}
                  disabled={deleting}
                  style={{ ...ghostButtonStyle, borderColor: '#7f1d1d', color: '#f87171' }}
                >
                  {deleting ? 'Deleting…' : 'Yes, delete'}
                </button>
                <button onClick={() => setConfirmDelete(false)} style={ghostButtonStyle}>
                  Cancel
                </button>
              </>
            ) : (
              <button
                onClick={() => setConfirmDelete(true)}
                style={{ ...ghostButtonStyle, borderColor: '#7f1d1d', color: '#f87171' }}
              >
                Delete
              </button>
            ))}
          </div>
          {/* Withdraw row — only shown when registered for 2+ game days to avoid crowding the header */}
          {canRegister && (latestReadyMajorForActions?.registeredForGameDays ?? []).length >= 2 && (
            <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', justifyContent: 'flex-end' }}>
              {(latestReadyMajorForActions!.registeredForGameDays!).map((gdId) => {
                const gd = gameDays.find((d) => d.gameDayId === gdId);
                if (gd && gd.phases.roundRobin.status !== 'upcoming') return null;
                const label = gd?.name ?? gdId.slice(-6);
                return (
                  <button key={gdId} onClick={() => handleWithdraw(gdId)} disabled={registering}
                    style={{ ...ghostButtonStyle, fontSize: 12, padding: '3px 10px' }}>
                    {registering ? '…' : `Withdraw · ${label}`}
                  </button>
                );
              })}
            </div>
          )}
        </div>
      </div>

      {majors.length === 0 ? (
        <div style={{ ...cardStyle, color: '#64748b', textAlign: 'center', padding: '40px 24px' }}>
          No versions yet.{' '}
          <button
            onClick={() => navigate(`/tanks/${tankId}/edit`)}
            style={{ ...primaryButtonStyle, padding: '4px 12px', fontSize: 13 }}
          >
            Open editor →
          </button>
        </div>
      ) : (
        majors.map((major) => (
          <MajorVersionCard key={major.version} major={major} minors={minorsByMajor[major.version] ?? []} isOwner={isOwner} />
        ))
      )}

      {(() => {
        const allGdIds = new Set(tank.versions.flatMap((v) => v.registeredForGameDays ?? []));
        const tankGameDays = gameDays.filter((gd) => allGdIds.has(gd.gameDayId));
        return tankGameDays.length > 0 ? <GameDayHistory allGameDays={tankGameDays} tankId={tank.tankId} /> : null;
      })()}

      {registerError && (
        <p style={{ color: '#f87171', fontSize: 13, margin: '0 0 12px' }}>{registerError}</p>
      )}

      {showFork && tank && tankId && latestReadyMajor && (
        <ForkDialog
          tank={tank}
          version={latestReadyMajor.version}
          onClose={() => setShowFork(false)}
          onForked={(newTankId) => {
            setShowFork(false);
            navigate(`/tanks/${newTankId}`);
          }}
        />
      )}

      {showGameDayPicker && (
        <GameDayPickerModal
          gameDays={gameDays}
          loading={loadingGameDays}
          onSelect={handleRegister}
          onClose={() => setShowGameDayPicker(false)}
        />
      )}

      {showTestDialog && (
        <TestDialog
          maps={maps}
          loadingMaps={loadingMaps}
          onTest={(opponent, mapId) => { handleTest(opponent, mapId); setShowTestDialog(false); }}
          onClose={() => setShowTestDialog(false)}
        />
      )}

      {showDeregisterConfirm && tank && tankId && (() => {
        const registeredVersions = tank.versions.filter(
          (v) => (v.registeredForGameDays?.length ?? 0) > 0,
        );
        return (
          <div style={{
            position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.7)',
            display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000,
          }}>
            <div style={{ ...cardStyle, maxWidth: 420, width: '100%', padding: 28 }}>
              <h3 style={{ margin: '0 0 12px', color: '#f87171', fontSize: 17 }}>Tank is registered</h3>
              <p style={{ margin: '0 0 20px', color: '#94a3b8', fontSize: 14, lineHeight: 1.5 }}>
                This tank is currently registered for
                {gameDayLabel ? ` ${gameDayLabel}` : ' a game day'}.
                To delete it, all registrations must be withdrawn first.
                Proceed with de-registering and deleting the tank?
              </p>
              <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                <button
                  onClick={() => { setShowDeregisterConfirm(false); setGameDayLabel(null); }}
                  style={ghostButtonStyle}
                >
                  Cancel
                </button>
                <button
                  onClick={async () => {
                    if (registeredVersions.length === 0) return;
                    setDeleting(true);
                    setShowDeregisterConfirm(false);
                    setGameDayLabel(null);
                    try {
                      for (const rv of registeredVersions) {
                        for (const gdId of rv.registeredForGameDays ?? []) {
                          await withdrawRegistration(tankId, rv.version, gdId);
                        }
                      }
                      await deleteTank(tankId);
                      navigate('/dashboard');
                    } catch (e) {
                      setDeleting(false);
                      alert(e instanceof Error ? e.message : 'Delete failed');
                    }
                  }}
                  style={{ ...ghostButtonStyle, borderColor: '#7f1d1d', color: '#f87171' }}
                >
                  De-register &amp; delete
                </button>
              </div>
            </div>
          </div>
        );
      })()}
    </Layout>
  );
}
