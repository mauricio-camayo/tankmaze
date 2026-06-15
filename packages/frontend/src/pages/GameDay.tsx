import { useEffect, useRef, useState } from 'react';
import { useParams, Link, useNavigate } from 'react-router-dom';
import Layout, { cardStyle, ghostButtonStyle, primaryButtonStyle } from '../components/Layout';
import { getGameDay, getTank, addRosterEntry, removeRosterEntry, listAiTanks, adminListTanks, listMaps } from '../services/api';
import { useAuthStore } from '../store/authStore';
import { useGameDayStore } from '../store/gameDayStore';
import type { GameDay, BracketSlot, GameDayPhaseStatus, GameDayGroup, Tank, TankVersion, GameMap } from '../types';

const BRACKET_LABELS: Record<string, string> = {
  r1: 'Elimination R1',
  r2: 'Elimination R2',
  r3: 'Elimination R3',
  final: 'Final',
};

function PhaseBadge({ status }: { status: GameDayPhaseStatus['status'] }) {
  const styles: Record<string, [string, string]> = {
    upcoming: ['#fbbf24', 'rgba(251,191,36,0.1)'],
    running: ['#4ade80', 'rgba(74,222,128,0.1)'],
    complete: ['#475569', 'rgba(71,85,105,0.1)'],
    cancelled: ['#f87171', 'rgba(248,113,113,0.1)'],
  };
  const [fg, bg] = styles[status] ?? ['#94a3b8', 'transparent'];
  return (
    <span style={{
      color: fg, background: bg,
      border: `1px solid ${fg}`,
      borderRadius: 4, fontSize: 11, padding: '2px 8px',
      fontWeight: 600, textTransform: 'uppercase',
    }}>
      {status}
    </span>
  );
}

function PhaseRow({ label, phase }: { label: string; phase: GameDayPhaseStatus }) {
  const timeLabel = phase.status === 'complete' && phase.endedAt
    ? `ended ${new Date(phase.endedAt * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
    : phase.status === 'running' && phase.startedAt
    ? `started ${new Date(phase.startedAt * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
    : '';
  return (
    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <PhaseBadge status={phase.status} />
        <span style={{ color: '#e2e8f0', fontSize: 14 }}>{label}</span>
      </div>
      {timeLabel && <span style={{ color: '#475569', fontSize: 12 }}>{timeLabel}</span>}
    </div>
  );
}

function SlotCell({ slot }: { slot: BracketSlot }) {
  const statusColor: Record<string, string> = {
    won: '#4ade80', lost: '#475569', both_lose: '#f87171', playing: '#fbbf24', bye: '#2d2d4e',
  };
  const color = statusColor[slot.status] ?? '#94a3b8';
  const displayName = slot.tankId ? (slot.tankName ?? slot.tankId) : null;

  return (
    <div style={{
      padding: '6px 10px', borderRadius: 6,
      border: `1px solid ${color}30`,
      background: `${color}08`,
      fontSize: 12, minWidth: 140,
    }}>
      {slot.tankId ? (
        <Link to={`/tanks/${slot.tankId}`} style={{ color, textDecoration: 'none' }}>
          {displayName}
          {slot.version && <span style={{ color: '#475569', marginLeft: 6 }}>@ {slot.version}</span>}
        </Link>
      ) : (
        <span style={{ color: '#475569' }}>bye</span>
      )}
      {slot.status !== 'playing' && slot.status !== 'bye' && (
        <span style={{ color, marginLeft: 8, fontSize: 10, fontWeight: 600 }}>
          {slot.status.replace('_', ' ').toUpperCase()}
        </span>
      )}
    </div>
  );
}

function BracketRound({ name, slots }: { name: string; slots: BracketSlot[] }) {
  const pairs: [BracketSlot, BracketSlot][] = [];
  for (let i = 0; i + 1 < slots.length; i += 2) {
    pairs.push([slots[i], slots[i + 1]]);
  }

  return (
    <div style={{ flexShrink: 0 }}>
      <div style={{ color: '#64748b', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 10 }}>
        {BRACKET_LABELS[name] ?? name}
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        {pairs.map((pair, i) => (
          <div key={i} style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            <SlotCell slot={pair[0]} />
            <div style={{ paddingLeft: 10, color: '#2d2d4e', fontSize: 10 }}>vs</div>
            <SlotCell slot={pair[1]} />
          </div>
        ))}
      </div>
    </div>
  );
}

function RRStandingsTable({ group, gi, placementPoints }: {
  group: GameDayGroup; gi: number; placementPoints: Record<string, number>;
}) {
  const nameMap = new Map<string, string>();
  group.tanks.forEach(({ tankId, tankName }) => { if (tankName) nameMap.set(tankId, tankName); });
  group.standings?.forEach(({ tankId, tankName }) => { if (tankName) nameMap.set(tankId, tankName); });

  const label = `Group ${String.fromCharCode(65 + gi)}`;
  const groupLabel = (
    <div style={{ color: '#475569', fontSize: 11, marginBottom: 8 }}>{label}</div>
  );

  const hasStandings = (group.standings ?? []).length > 0;

  if (!hasStandings) {
    return (
      <div>
        {groupLabel}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          {group.tanks.map(({ tankId }) => (
            <Link key={tankId} to={`/tanks/${tankId}`} style={{
              color: '#94a3b8', fontSize: 13, textDecoration: 'none',
              padding: '4px 8px', borderRadius: 4, background: '#0f0f1a',
            }}>
              {nameMap.get(tankId) ?? tankId}
            </Link>
          ))}
        </div>
      </div>
    );
  }

  const rows = [...group.standings!].sort((a, b) => b.points - a.points || b.wins - a.wins);

  const thStyle: React.CSSProperties = {
    color: '#64748b', fontSize: 10, fontWeight: 600, textTransform: 'uppercase',
    letterSpacing: '0.05em', padding: '0 8px 8px', borderBottom: '1px solid #2d2d4e',
  };
  const tdStyle: React.CSSProperties = {
    padding: '6px 8px', borderBottom: '1px solid #1a1a2e', verticalAlign: 'middle',
  };

  return (
    <div>
      {groupLabel}
      <table style={{ width: '100%', borderCollapse: 'collapse' }}>
        <thead>
          <tr>
            <th style={{ ...thStyle, textAlign: 'center', width: 28 }}>#</th>
            <th style={{ ...thStyle, textAlign: 'left' }}>Tank</th>
            <th style={{ ...thStyle, textAlign: 'center', width: 40 }}>Pts</th>
            <th style={{ ...thStyle, textAlign: 'center', width: 32 }}>W</th>
            <th style={{ ...thStyle, textAlign: 'center', width: 32 }}>L</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((s, i) => {
            const name = nameMap.get(s.tankId) ?? s.tankId;
            const placement = placementPoints[s.tankId];
            return (
              <tr key={s.tankId}>
                <td style={{ ...tdStyle, textAlign: 'center' }}>
                  <span style={{ color: rankColor(i + 1), fontWeight: 700, fontSize: 13 }}>{i + 1}</span>
                </td>
                <td style={tdStyle}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <Link to={`/tanks/${s.tankId}`} style={{ color: '#e2e8f0', fontSize: 13, textDecoration: 'none' }}>
                      {name}
                    </Link>
                    {placement !== undefined && (
                      <span style={{ color: '#a78bfa', fontSize: 11, fontWeight: 600 }}>+{placement}</span>
                    )}
                  </div>
                </td>
                <td style={{ ...tdStyle, textAlign: 'center', color: '#a78bfa', fontWeight: 700, fontSize: 14 }}>
                  {s.points}
                </td>
                <td style={{ ...tdStyle, textAlign: 'center', color: '#4ade80', fontSize: 13 }}>
                  {s.wins}
                </td>
                <td style={{ ...tdStyle, textAlign: 'center', color: '#f87171', fontSize: 13 }}>
                  {s.losses}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function rankColor(rank: number): string {
  if (rank === 1) return '#fbbf24';
  if (rank === 2) return '#94a3b8';
  if (rank === 3) return '#c2773d';
  return '#475569';
}

function isUsable(v: TankVersion): boolean {
  // Public API strips compileStatus to ''; treat '' as usable (published version).
  return v.compileStatus === 'ready' || v.compileStatus === '';
}

function latestMajorVersion(versions: TankVersion[]): string {
  const majors = versions.filter((v) => v.versionType === 'major' && isUsable(v));
  if (majors.length === 0) {
    const anyUsable = versions.find(isUsable);
    return anyUsable?.version ?? versions[0]?.version ?? 'v1';
  }
  // Versions are returned newest-first from the API; pick the first usable major.
  return majors[0].version;
}

function RosterSection({ gameDayId, roster, isAdmin, onChanged }: {
  gameDayId: string;
  roster: Array<{ tankId: string; version: string }>;
  isAdmin: boolean;
  onChanged: () => void;
}) {
  const [tankInfo, setTankInfo] = useState<Map<string, Tank & { versions: TankVersion[] }>>(new Map());
  const [confirmRemove, setConfirmRemove] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [showPicker, setShowPicker] = useState(false);
  const [aiTanks, setAiTanks] = useState<(Tank & { versions: TankVersion[] })[]>([]);
  // Name-search state
  const [allTanks, setAllTanks] = useState<Tank[]>([]);
  const [searchQuery, setSearchQuery] = useState('');
  const [searchResults, setSearchResults] = useState<Tank[]>([]);
  const [showDropdown, setShowDropdown] = useState(false);
  const [manualId, setManualId] = useState('');
  const [manualVer, setManualVer] = useState('');
  const [majorVersions, setMajorVersions] = useState<string[]>([]);
  const [verLoading, setVerLoading] = useState(false);
  const searchRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const rosterKey = roster.map((r) => r.tankId).join(',');
  useEffect(() => {
    if (roster.length === 0) return;
    Promise.all(roster.map((r) => getTank(r.tankId).catch(() => null))).then((results) => {
      const map = new Map<string, Tank & { versions: TankVersion[] }>();
      results.forEach((t, i) => { if (t) map.set(roster[i].tankId, t); });
      setTankInfo(map);
    });
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rosterKey]);

  useEffect(() => {
    if (!showPicker || !isAdmin) return;
    listAiTanks().then(setAiTanks).catch(() => {});
    // Load all tanks for name search (first 50; typical tournament sizes are well within this)
    adminListTanks().then((res) => setAllTanks(res.tanks)).catch(() => {});
  }, [showPicker, isAdmin]);

  function handleSearchChange(q: string) {
    setSearchQuery(q);
    if (searchRef.current) clearTimeout(searchRef.current);
    if (q.length < 4) {
      setSearchResults([]);
      setShowDropdown(false);
      return;
    }
    searchRef.current = setTimeout(() => {
      const lower = q.toLowerCase();
      const hits = allTanks
        .filter((t) =>
          t.name.toLowerCase().includes(lower) ||
          (t.authorName ?? '').toLowerCase().includes(lower),
        )
        .slice(0, 5);
      setSearchResults(hits);
      setShowDropdown(true);
    }, 200);
  }

  async function fetchVersionsFor(tankId: string, displayName?: string) {
    setVerLoading(true);
    setManualVer('');
    setMajorVersions([]);
    setErr(null);
    try {
      const full = await getTank(tankId);
      const majors = full.versions
        .filter((v) => v.versionType === 'major' && isUsable(v))
        .map((v) => v.version);
      if (majors.length === 0) {
        const label = displayName ?? tankId;
        setErr(`No promoted version found for ${label} — promote a version before adding to roster.`);
      } else {
        setMajorVersions(majors);
        setManualVer(majors[majors.length - 1]); // last entry is the highest major version
      }
    } catch (e) {
      setErr(`Could not fetch versions: ${e instanceof Error ? e.message : 'unknown error'}`);
    } finally {
      setVerLoading(false);
    }
  }

  async function selectTank(tank: Tank) {
    setSearchQuery(tank.name);
    setManualId(tank.tankId);
    setShowDropdown(false);
    await fetchVersionsFor(tank.tankId, tank.name);
  }

  async function handleRemove(tankId: string) {
    if (confirmRemove !== tankId) { setConfirmRemove(tankId); return; }
    setBusy(tankId);
    setErr(null);
    try {
      await removeRosterEntry(gameDayId, tankId);
      setConfirmRemove(null);
      onChanged();
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Failed');
    } finally { setBusy(null); }
  }

  async function addAi(tankId: string, version: string) {
    setBusy(tankId);
    setErr(null);
    try {
      await addRosterEntry(gameDayId, tankId, version);
      onChanged();
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Failed');
    } finally { setBusy(null); }
  }

  async function addManual() {
    if (!manualId.trim() || !manualVer.trim()) return;
    if (!/^v\d+$/.test(manualVer.trim())) {
      setErr('Version must be a major version (e.g. v1) — minor versions like v1.2 are not allowed.');
      return;
    }
    // AI tanks (builtin-* in production, __*__ in localserver) may appear
    // more than once for bracket padding — skip the duplicate guard for them.
    const isAI = manualId.trim().startsWith('builtin-') || /^__\w+__$/.test(manualId.trim());
    if (!isAI && roster.some((r) => r.tankId === manualId.trim())) {
      setErr('Tank is already registered for this game day.');
      return;
    }
    setBusy('manual');
    setErr(null);
    try {
      await addRosterEntry(gameDayId, manualId.trim(), manualVer.trim());
      setSearchQuery('');
      setManualId('');
      setManualVer('');
      setMajorVersions([]);
      onChanged();
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Failed');
    } finally { setBusy(null); }
  }

  const inpStyle: React.CSSProperties = {
    background: '#0f0f1a', border: '1px solid #2d2d4e', borderRadius: 4,
    color: '#e2e8f0', padding: '4px 8px', fontSize: 12, width: '100%',
  };

  return (
    <div style={{ ...cardStyle, marginBottom: 20 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 14 }}>
        <span style={{ color: '#64748b', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em' }}>
          Registered Tanks{roster.length > 0 ? ` — ${roster.length}` : ''}
        </span>
        {isAdmin && (
          <button
            onClick={() => setShowPicker((v) => !v)}
            style={{ ...ghostButtonStyle, fontSize: 11, padding: '3px 10px', borderColor: '#a78bfa', color: '#a78bfa' }}
          >
            {showPicker ? 'Close' : '+ Add'}
          </button>
        )}
      </div>

      {err && <p style={{ color: '#f87171', fontSize: 12, margin: '0 0 10px' }}>{err}</p>}

      {roster.length === 0 ? (
        <p style={{ color: '#475569', fontSize: 13, margin: 0 }}>No tanks registered yet.</p>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {roster.map(({ tankId, version }, idx) => {
            const info = tankInfo.get(tankId);
            const name = info?.name ?? `…${tankId.slice(-8)}`;
            const author = info?.authorName ?? null;
            return (
              <div key={`${tankId}-${idx}`} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '4px 0', borderBottom: '1px solid #1a1a2e' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                  <Link to={`/tanks/${tankId}`} style={{ color: '#e2e8f0', fontSize: 13, textDecoration: 'none', fontWeight: 500 }}>
                    {name}
                  </Link>
                  {author && <span style={{ color: '#64748b', fontSize: 12 }}>by {author}</span>}
                  <span style={{ color: '#475569', fontSize: 11 }}>@ {version}</span>
                </div>
                {isAdmin && (
                  <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                    {confirmRemove === tankId ? (
                      <>
                        <button
                          onClick={() => handleRemove(tankId)}
                          disabled={busy === tankId}
                          style={{ ...ghostButtonStyle, fontSize: 11, padding: '2px 8px', color: '#f87171', borderColor: '#dc2626', background: 'rgba(220,38,38,0.08)' }}
                        >
                          Confirm remove
                        </button>
                        <button
                          onClick={() => setConfirmRemove(null)}
                          style={{ ...ghostButtonStyle, fontSize: 11, padding: '2px 8px' }}
                        >
                          Cancel
                        </button>
                      </>
                    ) : (
                      <button
                        onClick={() => handleRemove(tankId)}
                        style={{ ...ghostButtonStyle, fontSize: 11, padding: '2px 8px', color: '#f87171', borderColor: '#7f1d1d' }}
                      >
                        Remove
                      </button>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      {isAdmin && showPicker && (
        <div style={{ borderTop: '1px solid #2d2d4e', marginTop: 14, paddingTop: 12 }}>
          {aiTanks.length > 0 && (
            <div style={{ marginBottom: 12 }}>
              <div style={{ color: '#64748b', fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 6 }}>AI Tanks</div>
              {aiTanks.map((t) => {
                const ver = t.versions[0]?.version ?? 'v0.1';
                return (
                  <div key={t.tankId} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 6 }}>
                    <span style={{ color: '#e2e8f0', fontSize: 13 }}>
                      {t.name} <span style={{ color: '#475569' }}>@ {ver}</span>
                    </span>
                    <button
                      onClick={() => addAi(t.tankId, ver)}
                      disabled={busy === t.tankId}
                      style={{ ...primaryButtonStyle, fontSize: 11, padding: '2px 10px' }}
                    >
                      {busy === t.tankId ? '…' : 'Add'}
                    </button>
                  </div>
                );
              })}
            </div>
          )}

          <div style={{ color: '#64748b', fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 6 }}>Add by Name</div>
          <div style={{ position: 'relative', marginBottom: 8 }}>
            <input
              value={searchQuery}
              onChange={(e) => handleSearchChange(e.target.value)}
              onBlur={() => setTimeout(() => setShowDropdown(false), 150)}
              onFocus={() => searchQuery.length >= 4 && searchResults.length > 0 && setShowDropdown(true)}
              placeholder="Type 4+ characters to search by tank or owner name…"
              style={inpStyle}
            />
            {showDropdown && (
              <div style={{
                position: 'absolute', top: '100%', left: 0, right: 0, zIndex: 10,
                background: '#1a1a2e', border: '1px solid #2d2d4e', borderRadius: 4,
                boxShadow: '0 4px 16px rgba(0,0,0,0.4)',
              }}>
                {searchResults.length === 0 ? (
                  <div style={{ padding: '6px 10px', color: '#475569', fontSize: 12 }}>No tanks found</div>
                ) : (
                  searchResults.map((t) => (
                    <button
                      key={t.tankId}
                      onMouseDown={() => selectTank(t)}
                      style={{
                        display: 'flex', width: '100%', alignItems: 'center', justifyContent: 'space-between',
                        padding: '6px 10px', background: 'transparent', border: 'none',
                        borderBottom: '1px solid #0f0f1a', cursor: 'pointer', textAlign: 'left',
                      }}
                    >
                      <span style={{ color: '#e2e8f0', fontSize: 13 }}>{t.name}</span>
                      {t.authorName && (
                        <span style={{ color: '#64748b', fontSize: 11 }}>by {t.authorName}</span>
                      )}
                    </button>
                  ))
                )}
              </div>
            )}
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr auto', gap: 6, alignItems: 'end' }}>
            <div>
              <div style={{ color: '#64748b', fontSize: 10, marginBottom: 2 }}>Tank ID</div>
              <input
                value={manualId}
                onChange={(e) => { setManualId(e.target.value); setSearchQuery(''); setMajorVersions([]); setManualVer(''); }}
                onBlur={(e) => { const id = e.target.value.trim(); if (id && majorVersions.length === 0) fetchVersionsFor(id); }}
                placeholder="tank-uuid"
                style={inpStyle}
              />
            </div>
            <div>
              <div style={{ color: '#64748b', fontSize: 10, marginBottom: 2 }}>Version</div>
              {verLoading ? (
                <div style={{ ...inpStyle, color: '#475569' }}>…</div>
              ) : majorVersions.length > 0 ? (
                <select
                  value={manualVer}
                  onChange={(e) => setManualVer(e.target.value)}
                  style={{ ...inpStyle, cursor: 'pointer' }}
                >
                  {[...majorVersions].reverse().map((v) => (
                    <option key={v} value={v}>{v}</option>
                  ))}
                </select>
              ) : (
                <input
                  value={manualVer}
                  disabled
                  placeholder="select a tank first"
                  style={{ ...inpStyle, opacity: 0.4, cursor: 'not-allowed' }}
                />
              )}
            </div>
            <button
              onClick={addManual}
              disabled={busy === 'manual' || !manualId.trim() || !manualVer.trim() || verLoading || majorVersions.length === 0}
              style={{ ...primaryButtonStyle, fontSize: 11, padding: '4px 12px' }}
            >
              {busy === 'manual' ? '…' : 'Add'}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

export default function GameDayPage() {
  const { gameDayId } = useParams<{ gameDayId: string }>();
  const navigate = useNavigate();
  const [gameDay, setGameDay] = useState<GameDay | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [maps, setMaps] = useState<GameMap[]>([]);
  const { user } = useAuthStore();
  const { setActiveGameDayLabel } = useGameDayStore();

  function reload() {
    if (!gameDayId) return;
    getGameDay(gameDayId).then(setGameDay).catch((e: Error) => setError(e.message));
  }

  useEffect(() => {
    if (!gameDayId) return;
    getGameDay(gameDayId)
      .then(setGameDay)
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
    listMaps().then(setMaps).catch(() => {});
  }, [gameDayId]);

  // Poll every 10 s while round-robin is running so the standings table updates live.
  const rrStatus = gameDay?.phases.roundRobin.status;
  useEffect(() => {
    if (!gameDayId || rrStatus !== 'running') return;
    const id = setInterval(() => {
      getGameDay(gameDayId).then(setGameDay).catch(() => {});
    }, 10000);
    return () => clearInterval(id);
  }, [gameDayId, rrStatus]);

  // Sync the header label whenever the loaded game day changes; clear on unmount.
  useEffect(() => {
    if (!gameDay) return;
    const { phases, schedule } = gameDay;
    const isUpcoming = phases.roundRobin.status === 'upcoming';
    const ts = isUpcoming ? schedule.roundRobin : (phases.final.endedAt ? phases.final.endedAt * 1000 : schedule.final);
    const d = new Date(ts);
    const label = `${d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} · ${d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`;
    setActiveGameDayLabel(label);
    return () => setActiveGameDayLabel(null);
  }, [gameDay, setActiveGameDayLabel]);

  if (loading) return <Layout><div style={{ color: '#64748b', padding: '40px 0' }}>Loading…</div></Layout>;
  if (error || !gameDay) return <Layout><div style={{ color: '#f87171' }}>{error ?? 'Game day not found'}</div></Layout>;

  const bracketRounds = Object.entries(gameDay.bracket ?? {})
    .filter(([, slots]) => slots.length > 0)
    .sort(([a], [b]) => {
      const order = ['r1', 'r2', 'r3', 'final'];
      return order.indexOf(a) - order.indexOf(b);
    });

  // Build a tank-name lookup from group membership (populated at registration_close).
  const tankNameMap = new Map<string, string>();
  (gameDay.registeredTanks ?? []).forEach(({ tankId, tankName }) => {
    if (tankName) tankNameMap.set(tankId, tankName);
  });
  (gameDay.groups ?? []).forEach((g) => {
    g.tanks.forEach(({ tankId, tankName }) => { if (tankName) tankNameMap.set(tankId, tankName); });
    g.standings?.forEach(({ tankId, tankName }) => { if (tankName) tankNameMap.set(tankId, tankName); });
  });
  Object.values(gameDay.bracket ?? {}).forEach((slots) => {
    slots.forEach(({ tankId, tankName }) => { if (tankId && tankName) tankNameMap.set(tankId, tankName); });
  });

  const standings = Object.entries(gameDay.placementPoints ?? {}).sort(([, a], [, b]) => b - a);
  const showGroups = (gameDay.groups ?? []).length > 0;
  const showBracket = bracketRounds.length > 0;
  const showRoster = gameDay.phases.roundRobin.status === 'upcoming';

  const { phases, schedule } = gameDay;

  // Detect silent skip: tournament was cancelled or all phases stuck at "upcoming"
  // after registration already closed (e.g. no tanks registered).
  const allPhasesCancelled =
    phases.roundRobin.status === 'cancelled' && phases.final.status === 'cancelled';
  const allPhasesUpcoming =
    phases.roundRobin.status === 'upcoming' &&
    phases.final.status === 'upcoming' &&
    (!phases.elimination || Object.values(phases.elimination).every((p) => p.status === 'upcoming'));
  const registrationClosed = Date.now() > new Date(schedule.registrationClose).getTime();
  const noRegisteredTanks = (gameDay.registeredTanks ?? []).length === 0;
  // Only show the "no tanks" banner when the roster was genuinely empty — not just because
  // the scheduler cancelled after an earlier run with real participants.
  const silentlySkipped = (allPhasesCancelled && noRegisteredTanks) || (allPhasesUpcoming && registrationClosed && noRegisteredTanks);

  // Detect completed tournament with no ranking data.
  const finalComplete = phases.final.status === 'complete';
  const noResults = finalComplete && standings.length === 0;

  const isAdmin = user?.isAdmin ?? false;

  // Build a map name lookup for the admin panel.
  const mapNameMap = new Map<string, string>(maps.map((m) => [m.mapId, m.name]));

  return (
    <Layout>
      <div style={{ marginBottom: 24, display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div>
          <h1 style={{ margin: '0 0 4px', color: '#e2e8f0', fontSize: 22, fontWeight: 700 }}>
            {gameDay.name ? `${gameDay.name}` : 'Game Day'}
          </h1>
          <div style={{ color: '#64748b', fontSize: 13 }}>
            {new Date(schedule.roundRobin).toLocaleDateString(undefined, {
              weekday: 'long', year: 'numeric', month: 'long', day: 'numeric',
            })}
          </div>
        </div>
        {isAdmin && phases.roundRobin.status === 'upcoming' && (
          <button
            onClick={() => navigate('/gamedays', { state: { editId: gameDay.gameDayId } })}
            style={{ ...ghostButtonStyle, fontSize: 13 }}
          >
            Edit
          </button>
        )}
      </div>

      {/* Admin-only config panel */}
      {isAdmin && (
        <div style={{
          ...cardStyle, marginBottom: 20,
          border: '1px solid rgba(167,139,250,0.3)',
          background: 'rgba(167,139,250,0.04)',
        }}>
          <div style={{ color: '#a78bfa', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 10 }}>
            Admin Info
          </div>
          <div style={{ display: 'flex', gap: 20, flexWrap: 'wrap', fontSize: 13 }}>
            <div>
              <span style={{ color: '#64748b' }}>Auto-fill bracket: </span>
              <span style={{ color: gameDay.autofill ? '#4ade80' : '#f87171', fontWeight: 600 }}>
                {gameDay.autofill ? 'ON' : 'OFF'}
              </span>
            </div>
            <div>
              <span style={{ color: '#64748b' }}>Maps: </span>
              <span style={{ color: '#e2e8f0' }}>
                {gameDay.randomMaps
                  ? 'Random maze per match'
                  : (gameDay.forcedMapIds ?? []).length > 0
                    ? (gameDay.forcedMapIds ?? []).map((id) => mapNameMap.get(id) ?? id).join(', ')
                    : 'Random maze per match'}
              </span>
            </div>
          </div>
        </div>
      )}

      {/* Phase timeline */}
      <div style={{ ...cardStyle, marginBottom: 20 }}>
        <div style={{ color: '#64748b', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 14 }}>
          Schedule
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <span style={{ color: '#94a3b8', fontSize: 14 }}>Registration closes</span>
            <span style={{ color: '#475569', fontSize: 12 }}>
              {new Date(schedule.registrationClose).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
            </span>
          </div>
          <PhaseRow label="Round Robin" phase={phases.roundRobin} />
          {phases.roundRobin.status !== 'complete' ? (
            // Bracket not yet built — tank count unknown; skip pre-computed placeholder slots.
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <span style={{ color: '#64748b', fontSize: 13, fontStyle: 'italic' }}>
                Elimination rounds (TBD — based on tank count)
              </span>
            </div>
          ) : (
            (schedule.elimination ?? []).map((ts, i) => {
              const ps = phases.elimination?.[`r${i + 1}`];
              return ps ? (
                <PhaseRow key={i} label={`Elimination R${i + 1}`} phase={ps} />
              ) : (
                <div key={i} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                  <span style={{ color: '#94a3b8', fontSize: 14 }}>Elimination R{i + 1}</span>
                  <span style={{ color: '#475569', fontSize: 12 }}>
                    {new Date(ts).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
                  </span>
                </div>
              );
            })
          )}
          <PhaseRow
            label="Final"
            phase={
              (phases.roundRobin.status === 'running' ||
                Object.values(phases.elimination ?? {}).some((p) => p.status === 'running'))
                ? { ...phases.final, status: 'upcoming' }
                : phases.final
            }
          />
        </div>
      </div>

      {/* Warning: registration closed but tournament never ran */}
      {silentlySkipped && (
        <div style={{
          ...cardStyle, marginBottom: 20,
          border: '1px solid rgba(251,191,36,0.4)',
          background: 'rgba(251,191,36,0.06)',
        }}>
          <p style={{ margin: 0, color: '#fbbf24', fontSize: 13, lineHeight: 1.5 }}>
            Registration closed with no registered tanks — tournament did not run.
          </p>
        </div>
      )}

      {/* Warning: final complete but no ranking data recorded */}
      {noResults && (
        <div style={{
          ...cardStyle, marginBottom: 20,
          border: '1px solid rgba(148,163,184,0.3)',
          background: 'rgba(148,163,184,0.05)',
        }}>
          <p style={{ margin: 0, color: '#94a3b8', fontSize: 13, lineHeight: 1.5 }}>
            Results not available — ranking data was not recorded for this tournament.
          </p>
        </div>
      )}

      {/* Groups + standings side-by-side */}
      {(showGroups || standings.length > 0) && (
        <div style={{
          display: 'grid',
          gridTemplateColumns: showGroups && standings.length > 0 ? '1fr 1fr' : '1fr',
          gap: 20, marginBottom: 20,
        }}>
          {showGroups && (
            <div style={cardStyle}>
              <div style={{ color: '#64748b', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 14 }}>
                Groups
              </div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
                {(gameDay.groups ?? []).map((group, gi) => (
                  <RRStandingsTable key={group.groupId} group={group} gi={gi} placementPoints={gameDay.placementPoints ?? {}} />
                ))}
              </div>
            </div>
          )}

          {standings.length > 0 && (
            <div style={cardStyle}>
              <div style={{ color: '#64748b', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 14 }}>
                Final standings
              </div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                {standings.map(([tankId, pts], i) => (
                  <div key={tankId} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '2px 0' }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <span style={{ color: rankColor(i + 1), fontSize: 13, fontWeight: 700, width: 20 }}>{i + 1}</span>
                      <Link to={`/tanks/${tankId}`} style={{ color: '#94a3b8', fontSize: 13, textDecoration: 'none' }}>
                        {tankNameMap.get(tankId) ?? tankId}
                      </Link>
                    </div>
                    <span style={{ color: '#a78bfa', fontWeight: 600, fontSize: 13 }}>+{pts} pts</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}

      {/* Bracket */}
      {showBracket && (
        <div style={{ ...cardStyle, marginBottom: 20 }}>
          <div style={{ color: '#64748b', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 16 }}>
            Bracket
          </div>
          <div style={{ display: 'flex', gap: 40, overflowX: 'auto', paddingBottom: 4 }}>
            {bracketRounds.map(([name, slots]) => (
              <BracketRound key={name} name={name} slots={slots} />
            ))}
          </div>
        </div>
      )}

      {/* Registered tanks — visible to all users during upcoming phase */}
      {showRoster && (
        <RosterSection
          gameDayId={gameDay.gameDayId}
          roster={gameDay.registeredTanks ?? []}
          isAdmin={isAdmin}
          onChanged={reload}
        />
      )}
    </Layout>
  );
}
