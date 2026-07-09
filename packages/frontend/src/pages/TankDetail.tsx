import { useEffect, useState } from 'react';
import { useParams, Link, useNavigate } from 'react-router-dom';
import Layout, { cardStyle, primaryButtonStyle, ghostButtonStyle } from '../components/Layout';
import { getTank, deleteTank, withdrawRegistration, getRankings, listGameDays, registerForGameDay, startMatch, listMaps, listTanks, getMySettings, type OpponentSpec } from '../services/api';
import type { Tank, TankVersion, GameDay, GameMap, RankingEntry } from '../types';
import ForkDialog from '../components/ForkDialog';
import { avatarSrc } from '../components/AvatarPicker';
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
    ready: '#59e6c0', pending: '#e8b339', compiling: '#4fa8e0', failed: '#ff8a75',
  };
  return (
    <span style={{
      display: 'inline-block', width: 8, height: 8, borderRadius: '50%',
      background: colors[status] ?? '#7fa2ba', flexShrink: 0,
    }} />
  );
}

function StatPips({ value }: { value: number }) {
  return (
    <div style={{ display: 'flex', gap: 2 }}>
      {Array.from({ length: 5 }).map((_, i) => (
        <div key={i} style={{
          width: 8, height: 8, borderRadius: 0,
          background: i < value ? '#ff7a29' : '#23577a',
        }} />
      ))}
    </div>
  );
}

type TestOpponent = 'scout' | 'bruiser' | 'ranger' | 'randy';

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
  const sorted = [...gameDays]
    .filter((gd) => new Date(gd.schedule.registrationClose) >= now && gd.phases.roundRobin.status === 'upcoming')
    .sort((a, b) => (a.schedule.registrationClose < b.schedule.registrationClose ? -1 : 1));
  return (
    <div style={overlay}>
      <div style={{ ...cardStyle, width: 440, maxHeight: '80vh', overflowY: 'auto' }}>
        <h3 style={{ margin: '0 0 16px', color: '#e7f1f7' }}>Select Game Day</h3>
        {loading ? (
          <p style={{ color: '#5b87a3', fontSize: 13, margin: '0 0 16px' }}>Loading game days…</p>
        ) : sorted.length === 0 ? (
          <p style={{ color: '#5b87a3', fontSize: 13, margin: '0 0 16px' }}>No open game days right now.</p>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 16 }}>
            {sorted.map((gd) => {
              const regClose = new Date(gd.schedule.registrationClose).toLocaleDateString(undefined, {
                year: 'numeric', month: 'short', day: 'numeric',
              });
              const final = new Date(gd.schedule.final).toLocaleDateString(undefined, {
                year: 'numeric', month: 'short', day: 'numeric',
              });
              return (
                <button
                  key={gd.gameDayId}
                  onClick={() => onSelect(gd.gameDayId)}
                  style={{
                    background: '#082e4a', border: '1px solid #23577a', borderRadius: 0,
                    color: '#e7f1f7', padding: '10px 14px', textAlign: 'left',
                    cursor: 'pointer', display: 'flex', flexDirection: 'column', gap: 4,
                  }}
                >
                  <span style={{ fontSize: 14, fontWeight: 600 }}>{gd.name ?? final}</span>
                  <span style={{ fontSize: 12, color: '#5b87a3' }}>
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
        <h3 style={{ margin: '0 0 16px', color: '#e7f1f7' }}>Test vs AI</h3>
        <div style={{ marginBottom: 16 }}>
          <p style={{ margin: '0 0 8px', fontSize: 13, color: '#7fa2ba' }}>Opponent</p>
          {(['scout', 'bruiser', 'ranger', 'randy'] as TestOpponent[]).map((op) => (
            <label key={op} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, cursor: 'pointer' }}>
              <input type="radio" name="td-opponent" value={op} checked={opponent === op} onChange={() => setOpponent(op)} />
              <span style={{ color: '#e7f1f7', textTransform: 'capitalize', fontSize: 14 }}>{op}</span>
            </label>
          ))}
        </div>
        <div style={{ marginBottom: 20 }}>
          <p style={{ margin: '0 0 8px', fontSize: 13, color: '#7fa2ba' }}>Map</p>
          {loadingMaps ? (
            <span style={{ color: '#5b87a3', fontSize: 13 }}>Loading maps…</span>
          ) : (
            <>
              <label style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, cursor: 'pointer' }}>
                <input type="radio" name="td-map" value="" checked={mapId === null} onChange={() => selectMap(null)} />
                <span style={{ color: '#e7f1f7', fontSize: 14 }}>Random (default)</span>
              </label>
              {maps.map((m) => (
                <label key={m.mapId} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, cursor: 'pointer' }}>
                  <input type="radio" name="td-map" value={m.mapId} checked={mapId === m.mapId} onChange={() => selectMap(m.mapId)} />
                  <span style={{ color: '#e7f1f7', fontSize: 14 }}>{m.name}</span>
                  <span style={{ color: '#5b87a3', fontSize: 12 }}>{m.description}</span>
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

// ChallengeDialog is the "Informal match" flow (item 37): search the global
// leaderboard for another author's tank and challenge it directly. No map
// picker here — Informal matches always use a randomly generated maze,
// same as Ranked (§6.4).
function ChallengeDialog({
  rankings, loadingRankings, ownTankId, onChallenge, onClose,
}: {
  rankings: RankingEntry[];
  loadingRankings: boolean;
  ownTankId: string;
  onChallenge: (opponentTankId: string) => void;
  onClose: () => void;
}) {
  const [query, setQuery] = useState('');
  const q = query.trim().toLowerCase();
  const results = rankings
    .filter((r) => r.tankId !== ownTankId)
    .filter((r) => !q || (r.tankName ?? '').toLowerCase().includes(q) || (r.authorUsername ?? '').toLowerCase().includes(q))
    .slice(0, 25);

  return (
    <div style={overlay}>
      <div style={{ ...cardStyle, width: 420, maxHeight: '80vh', display: 'flex', flexDirection: 'column' }}>
        <h3 style={{ margin: '0 0 4px', color: '#e7f1f7' }}>Challenge an author</h3>
        <p style={{ margin: '0 0 16px', color: '#7fa2ba', fontSize: 13 }}>
          Unranked — doesn't affect either tank's Global Score.
        </p>
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search by tank or author name…"
          style={{
            width: '100%', background: '#072943', border: '1px solid #23577a', borderRadius: 0,
            color: '#e7f1f7', padding: '8px 10px', fontSize: 14, boxSizing: 'border-box', marginBottom: 12,
          }}
        />
        <div style={{ overflowY: 'auto', flex: 1, marginBottom: 16 }}>
          {loadingRankings ? (
            <span style={{ color: '#5b87a3', fontSize: 13 }}>Loading tanks…</span>
          ) : results.length === 0 ? (
            <span style={{ color: '#5b87a3', fontSize: 13 }}>No tanks match.</span>
          ) : (
            results.map((r) => (
              <button
                key={r.tankId}
                onClick={() => onChallenge(r.tankId)}
                style={{
                  display: 'flex', justifyContent: 'space-between', alignItems: 'center', width: '100%',
                  background: 'none', border: '1px solid #23577a', borderRadius: 0, cursor: 'pointer',
                  padding: '8px 10px', marginBottom: 6, textAlign: 'left',
                }}
              >
                <span style={{ color: '#e7f1f7', fontSize: 14, fontWeight: 600 }}>{r.tankName ?? r.tankId}</span>
                <span style={{ color: '#7fa2ba', fontSize: 12 }}>{r.authorUsername ?? ''}</span>
              </button>
            ))
          )}
        </div>
        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <button onClick={onClose} style={ghostButtonStyle}>Cancel</button>
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
            <span style={{ color: '#ffab6b', fontWeight: 700, fontSize: 15 }}>{major.version}</span>
            {isOwner && <StatusDot status={major.compileStatus} />}
            {isOwner && <span style={{ color: '#5b87a3', fontSize: 12 }}>{major.compileStatus}</span>}
            {major.disqualified && (
              <span style={{ background: '#ff8a75', color: '#fff', fontSize: 10, padding: '1px 6px', borderRadius: 0, fontWeight: 600 }}>DQ</span>
            )}
            {(major.registeredForGameDays?.length ?? 0) > 0 && (
              <span style={{ background: '#59e6c0', color: '#0a3550', fontSize: 10, padding: '1px 6px', borderRadius: 0, fontWeight: 600 }}>
                REGISTERED{(major.registeredForGameDays!.length > 1) ? ` ×${major.registeredForGameDays!.length}` : ''}
              </span>
            )}
          </div>
          <div style={{ color: '#5b87a3', fontSize: 12 }}>
            {relativeTime(major.createdAt)} · {new Date(major.createdAt * 1000).toLocaleDateString()}
          </div>
        </div>

        <div style={{ display: 'grid', gridTemplateColumns: 'auto auto', gap: '2px 16px', fontSize: 13 }}>
            <span style={{ color: '#5b87a3' }}>Win rate</span>
            <span style={{ color: '#59e6c0', fontWeight: 600, textAlign: 'right' }}>{pct(major.winRate)}</span>
            <span style={{ color: '#5b87a3' }}>Matches</span>
            <span style={{ color: '#e7f1f7', textAlign: 'right' }}>{num(major.matchesPlayed)}</span>
            <span style={{ color: '#5b87a3' }}>Avg damage</span>
            <span style={{ color: '#e7f1f7', textAlign: 'right' }}>{num(major.avgDamageDealt, 1)}</span>
            <span style={{ color: '#5b87a3' }}>Avg survival</span>
            <span style={{ color: '#e7f1f7', textAlign: 'right' }}>{num(major.avgSurvivalTicks)} tks</span>
          </div>
      </div>

      <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' }}>
        {(['speed', 'sensorRange', 'damage', 'armor', 'fireRate'] as const).map((stat) => (
          <div key={stat} style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            <span style={{ color: '#5b87a3', fontSize: 10, textTransform: 'uppercase', letterSpacing: '0.05em' }}>
              {stat === 'sensorRange' ? 'Sensor' : stat === 'fireRate' ? 'Fire' : stat.charAt(0).toUpperCase() + stat.slice(1)}
            </span>
            <StatPips value={major.config[stat]} />
          </div>
        ))}
      </div>

      {minors.length > 0 && (
        <div style={{ marginTop: 14, borderTop: '1px solid #23577a', paddingTop: 12 }}>
          <button
            onClick={() => setExpanded((e) => !e)}
            style={{ background: 'none', border: 'none', color: '#5b87a3', cursor: 'pointer', fontSize: 12, padding: 0 }}
          >
            {expanded ? '▾' : '▸'} {minors.length} minor version{minors.length !== 1 ? 's' : ''}
          </button>
          {expanded && (
            <div style={{ marginTop: 8, display: 'flex', flexDirection: 'column', gap: 4 }}>
              {minors.map((mv) => (
                <div key={mv.version} style={{
                  display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                  padding: '5px 10px', borderRadius: 0, background: '#0a3550',
                }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    <StatusDot status={mv.compileStatus} />
                    <span style={{ color: '#7fa2ba', fontSize: 12, marginLeft: 4 }}>{mv.version}</span>
                    {mv.compileStatus === 'failed' && (
                      <span style={{ color: '#ff8a75', fontSize: 11 }}>compile failed</span>
                    )}
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                    {isOwner && (mv.testMatchCount ?? 0) > 0 && (
                      <span style={{ color: '#4a7291', fontSize: 11 }}>
                        {mv.testMatchCount} test{mv.testMatchCount !== 1 ? 's' : ''}
                      </span>
                    )}
                    <span style={{ color: '#4a7291', fontSize: 11 }}>
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
        style={{ background: 'none', border: 'none', color: '#7fa2ba', cursor: 'pointer', fontSize: 14, fontWeight: 600, padding: 0, width: '100%', textAlign: 'left', display: 'flex', alignItems: 'center', gap: 8 }}
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
            const resultColor = result === 'Winner' ? '#59e6c0' : result === 'Finalist' ? '#e8b339' : '#5b87a3';
            return (
              <div key={gd.gameDayId} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 0', borderBottom: '1px solid #23577a' }}>
                <div>
                  <Link to={`/gameday/${gd.gameDayId}`} style={{ color: '#ffab6b', textDecoration: 'none', fontSize: 14, fontWeight: 500 }}>
                    {gd.name ?? date}
                  </Link>
                  <div style={{ color: '#5b87a3', fontSize: 12, marginTop: 2 }}>{date}</div>
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
              <span style={{ color: '#5b87a3', fontSize: 12, alignSelf: 'center' }}>{page + 1} / {totalPages}</span>
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
  const [avatarUrl, setAvatarUrl] = useState<string | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [rank, setRank] = useState<number | null>(null);
  const [showFork, setShowFork] = useState(false);
  const [atForkLimit, setAtForkLimit] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [showDeregisterConfirm, setShowDeregisterConfirm] = useState(false);
  const [gameDayLabel, setGameDayLabel] = useState<string | null>(null);
  // Register for Game Day
  const [showGameDayPicker, setShowGameDayPicker] = useState(false);
  const [gameDays, setGameDays] = useState<GameDay[]>([]);
  const [loadingGameDays, setLoadingGameDays] = useState(false);
  const [gameDaysLoaded, setGameDaysLoaded] = useState(false);
  const [registering, setRegistering] = useState(false);
  const [registerError, setRegisterError] = useState<string | null>(null);
  // Test vs AI
  const [showTestDialog, setShowTestDialog] = useState(false);
  const [maps, setMaps] = useState<GameMap[]>([]);
  const [loadingMaps, setLoadingMaps] = useState(false);
  // Challenge another author (Informal match, item 37)
  const [showChallengeDialog, setShowChallengeDialog] = useState(false);
  const [rankings, setRankings] = useState<RankingEntry[]>([]);
  const [loadingRankings, setLoadingRankings] = useState(false);

  useEffect(() => {
    if (!tankId) return;
    getTank(tankId)
      .then((t) => { setTank(t); setAvatarUrl(t.avatarUrl); })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
    getRankings()
      .then((entries) => {
        const entry = entries.find((r) => r.tankId === tankId);
        if (entry) setRank(entry.rank);
      })
      .catch(() => { /* rank unavailable — leave null */ });
  }, [tankId]);

  // Whether the current user (not necessarily this tank's owner — forking
  // creates a new tank under the current user regardless of whose tank it is)
  // is already at their tier's tank limit, to proactively block Fork instead
  // of only reacting to the backend's 403 after a failed attempt.
  useEffect(() => {
    if (!currentUser) return;
    Promise.all([listTanks(), getMySettings()])
      .then(([myTanks, settings]) => setAtForkLimit(myTanks.length >= settings.tankLimit))
      .catch(() => { /* leave false — backend still enforces the real limit */ });
  }, [currentUser]);

  // Pre-fetch game days when the tank is already registered, so Withdraw button labels
  // can show the human-readable name instead of a UUID fragment.
  useEffect(() => {
    if (!tankId) return;
    const ids = tank?.versions.flatMap((v) => v.registeredForGameDays ?? []) ?? [];
    if (ids.length > 0 && gameDays.length === 0) {
      setLoadingGameDays(true);
      listGameDays()
        .then((days) => { setGameDays(days); setGameDaysLoaded(true); })
        .catch(() => { setGameDays([]); setGameDaysLoaded(true); })
        .finally(() => setLoadingGameDays(false));
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tank]);

  async function openRegisterPicker() {
    setShowGameDayPicker(true);
    if (gameDays.length === 0) {
      setLoadingGameDays(true);
      listGameDays()
        .then((days) => { setGameDays(days); setGameDaysLoaded(true); })
        .catch(() => { setGameDays([]); setGameDaysLoaded(true); })
        .finally(() => setLoadingGameDays(false));
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

  async function openChallengeDialog() {
    setShowChallengeDialog(true);
    if (rankings.length === 0) {
      setLoadingRankings(true);
      getRankings().then(setRankings).catch(() => setRankings([])).finally(() => setLoadingRankings(false));
    }
  }

  async function handleChallenge(opponentTankId: string) {
    if (!tankId || !latestReadyMajorForActions) return;
    setShowChallengeDialog(false);
    try {
      // Rankings doesn't carry a reliable challengeable version (it's shown
      // on the leaderboard, but never actually populated by either
      // backend — a separate pre-existing gap, items 209/213). Resolve it
      // instead from GET /tanks/{id}: tank-api's public view for a
      // non-owned tank already strips this down to a single latest-major
      // entry, but localserver's local-dev stand-in returns every version
      // unstripped (chronological order), so pick the latest major
      // explicitly rather than assuming index 0 is it.
      const opponentTank = await getTank(opponentTankId);
      const opponentVersion = opponentTank.versions
        .filter((v) => isMajor(v.version))
        .sort((a, b) => b.createdAt - a.createdAt)[0]?.version;
      if (!opponentVersion) {
        setRegisterError('That tank has no version ready to challenge yet.');
        return;
      }
      const spec: OpponentSpec = { type: 'informal', tankId: opponentTankId, version: opponentVersion };
      const match = await startMatch(tankId, latestReadyMajorForActions.version, spec);
      navigate(`/watch?matchId=${match.matchId}`);
    } catch (e) {
      setRegisterError(e instanceof Error ? e.message : 'Failed to start challenge');
    }
  }

  if (loading) {
    return <Layout><div style={{ color: '#5b87a3', padding: '40px 0' }}>Loading…</div></Layout>;
  }
  if (error || !tank) {
    return <Layout><div style={{ color: '#ff8a75' }}>{error ?? 'Tank not found'}</div></Layout>;
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
  const isDisqualified = !!latestReadyMajorForActions?.disqualified;
  const isRegistered = canRegister && (latestReadyMajorForActions?.registeredForGameDays?.length ?? 0) > 0;
  const canTest = isOwner && !!latestReadyMajorForActions;

  return (
    <Layout>
      <div style={{ marginBottom: 20 }}>
        <Link to={isOwner ? '/dashboard' : '/leaderboard'} style={{ color: '#5b87a3', fontSize: 13, textDecoration: 'none' }}>
          {isOwner ? '← My tanks' : '← Leaderboard'}
        </Link>
      </div>

      <div style={{ display: 'flex', flexWrap: 'wrap', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 28, gap: 16 }}>
        <div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 14, marginBottom: 6 }}>
            <img
              src={avatarSrc(tankId!, avatarUrl)}
              alt=""
              style={{ width: 52, height: 52, borderRadius: 0, imageRendering: 'pixelated', border: '2px solid #23577a', flexShrink: 0 }}
            />
            <h1 style={{ margin: 0, color: '#e7f1f7', fontSize: 26, fontWeight: 700 }}>
              {tank.name}
            </h1>
          </div>
          <div style={{ display: 'flex', gap: 16, alignItems: 'center', flexWrap: 'wrap', marginBottom: 10 }}>
            <span style={{ color: '#e8b339', fontSize: 13, fontWeight: 600 }}>
              {rank !== null ? `#${rank}` : '#—'}
            </span>
            <span style={{ color: '#ffab6b', fontSize: 20, fontWeight: 700 }}>
              {tank.globalScore.toLocaleString()} pts
            </span>
            {tank.bestFinish !== null && (
              <span style={{ color: '#7fa2ba', fontSize: 13 }}>Best: {ordinal(tank.bestFinish)}</span>
            )}
            <span style={{ color: '#5b87a3', fontSize: 13 }}>
              {tank.gameDaysCount} game {tank.gameDaysCount === 1 ? 'day' : 'days'}
            </span>
            {tank.createdAt > 0 && (
              <span style={{ color: '#5b87a3', fontSize: 13 }}
                title={new Date(tank.createdAt * 1000).toLocaleDateString()}>
                submitted {relativeTime(tank.createdAt)}
              </span>
            )}
          </div>

          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            {tank.forkedFromTankId && (
              <Link to={`/tanks/${tank.forkedFromTankId}`} style={{
                color: '#4fa8e0', fontSize: 12, textDecoration: 'none',
                border: '1px solid #072943', borderRadius: 0, padding: '2px 8px', background: '#072943',
              }}>
                ⑂ fork of {tank.forkedFromVersion ?? tank.forkedFromTankId.slice(-8)}
              </Link>
            )}
            {tank.scoreTransferredFrom && (
              <span style={{
                color: '#e8b339', fontSize: 12,
                border: '1px solid #3d2a10', borderRadius: 0, padding: '2px 8px', background: '#3d2a10',
              }}>
                score transferred in
              </span>
            )}
            {tank.scoreTransferredTo && (
              <Link to={`/tanks/${tank.scoreTransferredTo}`} style={{
                color: '#7fa2ba', fontSize: 12, textDecoration: 'none',
                border: '1px solid #23577a', borderRadius: 0, padding: '2px 8px',
              }}>
                score transferred out →
              </Link>
            )}
          </div>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, alignItems: 'flex-end' }}>
          {/* Primary action row */}
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', justifyContent: 'flex-end' }}>
            {canTest && (
              <button onClick={openTestDialog} style={ghostButtonStyle}>
                Test vs AI
              </button>
            )}
            {canTest && (
              <button onClick={openChallengeDialog} style={ghostButtonStyle}>
                Challenge
              </button>
            )}
            {canRegister && (
              <>
                {gameDaysLoaded && (latestReadyMajorForActions?.registeredForGameDays ?? []).length < 2 &&
                  (latestReadyMajorForActions?.registeredForGameDays ?? []).map((gdId) => {
                    const gd = gameDays.find((d) => d.gameDayId === gdId);
                    if (!gd || gd.phases.roundRobin.status !== 'upcoming') return null;
                    return (
                      <button key={gdId} onClick={() => handleWithdraw(gdId)} disabled={registering} style={ghostButtonStyle}>
                        {registering ? '…' : `Withdraw · ${gd.name}`}
                      </button>
                    );
                  })}
                <button
                  onClick={openRegisterPicker}
                  disabled={registering || isDisqualified}
                  title={isDisqualified ? 'This version is disqualified due to excessive tick violations and cannot register for Game Days.' : undefined}
                  style={{ ...ghostButtonStyle, opacity: isDisqualified ? 0.5 : 1, cursor: isDisqualified ? 'not-allowed' : 'pointer' }}
                >
                  {registering ? '…' : 'Register for Game Day'}
                </button>
              </>
            )}
            {latestReadyMajor && !tank.scoreTransferredTo && (
              <button
                onClick={() => setShowFork(true)}
                disabled={atForkLimit}
                title={atForkLimit ? 'You are at your tank limit — upgrade or delete a tank to fork this one.' : undefined}
                style={{ ...ghostButtonStyle, opacity: atForkLimit ? 0.5 : 1, cursor: atForkLimit ? 'not-allowed' : 'pointer' }}
              >
                Fork
              </button>
            )}
            {isOwner && (
              <button onClick={() => navigate(`/tanks/${tankId}/edit`)} style={primaryButtonStyle}>
                Edit
              </button>
            )}
            {isOwner && (confirmDelete ? (
              <>
                <span style={{ color: '#ff8a75', fontSize: 13 }}>Delete forever?</span>
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
                  style={{ ...ghostButtonStyle, borderColor: '#3a1a18', color: '#ff8a75' }}
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
                style={{ ...ghostButtonStyle, borderColor: '#3a1a18', color: '#ff8a75' }}
              >
                Delete
              </button>
            ))}
          </div>
          {/* Withdraw row — only shown when registered for 2+ game days to avoid crowding the header */}
          {gameDaysLoaded && canRegister && (latestReadyMajorForActions?.registeredForGameDays ?? []).length >= 2 && (
            <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', justifyContent: 'flex-end' }}>
              {(latestReadyMajorForActions!.registeredForGameDays!).map((gdId) => {
                const gd = gameDays.find((d) => d.gameDayId === gdId);
                if (!gd || gd.phases.roundRobin.status !== 'upcoming') return null;
                return (
                  <button key={gdId} onClick={() => handleWithdraw(gdId)} disabled={registering}
                    style={{ ...ghostButtonStyle, fontSize: 12, padding: '3px 10px' }}>
                    {registering ? '…' : `Withdraw · ${gd.name}`}
                  </button>
                );
              })}
            </div>
          )}
        </div>
      </div>


      {majors.length === 0 ? (
        <div style={{ ...cardStyle, color: '#5b87a3', textAlign: 'center', padding: '40px 24px' }}>
          No versions yet.{' '}
          {isOwner && (
            <button
              onClick={() => navigate(`/tanks/${tankId}/edit`)}
              style={{ ...primaryButtonStyle, padding: '4px 12px', fontSize: 13 }}
            >
              Open editor →
            </button>
          )}
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
        <p style={{ color: '#ff8a75', fontSize: 13, margin: '0 0 12px' }}>{registerError}</p>
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

      {showChallengeDialog && tankId && (
        <ChallengeDialog
          rankings={rankings}
          loadingRankings={loadingRankings}
          ownTankId={tankId}
          onChallenge={handleChallenge}
          onClose={() => setShowChallengeDialog(false)}
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
              <h3 style={{ margin: '0 0 12px', color: '#ff8a75', fontSize: 17 }}>Tank is registered</h3>
              <p style={{ margin: '0 0 20px', color: '#7fa2ba', fontSize: 14, lineHeight: 1.5 }}>
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
                  style={{ ...ghostButtonStyle, borderColor: '#3a1a18', color: '#ff8a75' }}
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
