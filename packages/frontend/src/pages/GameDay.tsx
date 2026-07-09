import React, { useEffect, useRef, useState } from 'react';
import { useParams, Link, useNavigate } from 'react-router-dom';
import Layout, { cardStyle, ghostButtonStyle, primaryButtonStyle } from '../components/Layout';
import { getGameDay, getTank, addRosterEntry, removeRosterEntry, listAiTanks, adminListTanks, listMaps } from '../services/api';
import { useAuthStore } from '../store/authStore';
import type { GameDay, BracketSlot, GameDayPhaseStatus, GameDayGroup, GroupMatchResult, Tank, TankVersion, GameMap } from '../types';

const BRACKET_LABELS: Record<string, string> = {
  r1: 'Elimination R1',
  r2: 'Elimination R2',
  r3: 'Elimination R3',
  final: 'Final',
};

function PhaseBadge({ status }: { status: GameDayPhaseStatus['status'] }) {
  const styles: Record<string, [string, string]> = {
    upcoming: ['#e8b339', 'rgba(251,191,36,0.1)'],
    running: ['#59e6c0', 'rgba(74,222,128,0.1)'],
    complete: ['#4a7291', 'rgba(71,85,105,0.1)'],
    cancelled: ['#ff8a75', 'rgba(248,113,113,0.1)'],
  };
  const [fg, bg] = styles[status] ?? ['#7fa2ba', 'transparent'];
  return (
    <span style={{
      color: fg, background: bg,
      border: `1px solid ${fg}`,
      borderRadius: 0, fontSize: 11, padding: '2px 8px',
      fontWeight: 600, textTransform: 'uppercase',
    }}>
      {status}
    </span>
  );
}

function PhaseRow({ label, phase, scheduledAt }: { label: string; phase: GameDayPhaseStatus; scheduledAt?: string }) {
  const timeLabel = phase.status === 'complete' && phase.endedAt
    ? `ended ${new Date(phase.endedAt * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
    : phase.status === 'running' && phase.startedAt
    ? `started ${new Date(phase.startedAt * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
    : phase.status === 'upcoming' && scheduledAt
    ? new Date(scheduledAt).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
    : '';
  return (
    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <PhaseBadge status={phase.status} />
        <span style={{ color: '#e7f1f7', fontSize: 14 }}>{label}</span>
      </div>
      {timeLabel && <span style={{ color: '#4a7291', fontSize: 12 }}>{timeLabel}</span>}
    </div>
  );
}

function wrapName(raw: string): React.ReactNode {
  if (raw.length <= 40) return raw;
  const line1 = raw.slice(0, 40);
  const rest = raw.slice(40, 80);
  const line2 = raw.length > 80 ? rest + '…' : rest;
  return <>{line1}<br />{line2}</>;
}

function SlotCell({ slot }: { slot: BracketSlot }) {
  const statusColor: Record<string, string> = {
    won: '#59e6c0', lost: '#4a7291', both_lose: '#ff8a75', playing: '#e8b339', bye: '#23577a',
  };
  const color = statusColor[slot.status] ?? '#7fa2ba';
  const rawName = slot.tankId ? (slot.tankName ?? slot.tankId) : null;
  const displayName = rawName ? wrapName(rawName) : null;

  return (
    <div style={{
      padding: '6px 10px', borderRadius: 0,
      border: `1px solid ${color}30`,
      background: `${color}08`,
      fontSize: 12, minWidth: 140,
      lineHeight: '16px',
    }}>
      {slot.tankId ? (
        <Link to={`/tanks/${slot.tankId}`} style={{ color, textDecoration: 'none' }}>
          {displayName}
          {slot.version && <span style={{ color: '#4a7291', marginLeft: 6 }}>@ {slot.version}</span>}
        </Link>
      ) : (
        <span style={{ color: '#4a7291' }}>bye</span>
      )}
      {slot.status !== 'playing' && slot.status !== 'bye' && (
        <span style={{ color, marginLeft: 8, fontSize: 10, fontWeight: 600 }}>
          {slot.status.replace('_', ' ').toUpperCase()}
        </span>
      )}
    </div>
  );
}

// Bracket layout constants — must match SlotCell's rendered height exactly.
// SlotCell: 6px padding-top + 16px line-height + 6px padding-bottom + 2px border = 30px
// "vs" row: 12px line-height
// pair height: SLOT_H + SLOT_GAP + VS_H + SLOT_GAP + SLOT_H
const B_SLOT_H = 30;
const B_VS_H = 12;
const B_SLOT_GAP = 2;
const B_PAIR_GAP = 16;
const B_PAIR_H = B_SLOT_H + B_SLOT_GAP + B_VS_H + B_SLOT_GAP + B_SLOT_H; // 76px

// Width of the gap between bracket columns (also the connector SVG width).
const B_CONN_W = 40;
// Approximate height of the round label div (fontSize:11 ~16px + marginBottom:10).
const B_LABEL_H = 26;

// Draws two-level "elbow" connector lines between adjacent bracket columns.
//
// Level 1 (inner, at x=spineX1): one elbow per from-pair connecting the pair's
//   two slot centres with a vertical spine; a horizontal arm exits from the midY.
// Level 2 (outer, at x=spineX2): one elbow per to-pair, combining two consecutive
//   inner exits; its midY aligns exactly with the to-pair's "vs" position.
//
// Only rendered when toPairs === fromPairs / 2 (standard halving rounds); the
// "final" key breaks this invariant and falls back to a plain spacer.
function BracketConnector({ fromRoundIndex, fromSlots, toSlots }: {
  fromRoundIndex: number;
  fromSlots: BracketSlot[];
  toSlots: BracketSlot[];
}) {
  const fromPairs = Math.floor(fromSlots.length / 2);
  const toPairs = Math.floor(toSlots.length / 2);

  if (fromPairs !== toPairs * 2) {
    return <div style={{ width: B_CONN_W, flexShrink: 0 }} />;
  }

  const spanH = Math.pow(2, fromRoundIndex) * B_PAIR_H + (Math.pow(2, fromRoundIndex) - 1) * B_PAIR_GAP;
  const pairPad = (spanH - B_PAIR_H) / 2;
  const totalH = fromPairs * spanH + (fromPairs - 1) * B_PAIR_GAP;
  const spineX1 = B_CONN_W / 2;       // inner spine x (midpoint of connector width)
  const spineX2 = B_CONN_W - 2;       // outer spine x (near right edge)

  const lines: React.ReactElement[] = [];
  const innerMidYs: number[] = [];

  // Level 1: inner elbow per from-pair
  for (let p = 0; p < fromPairs; p++) {
    const pY0 = p * (spanH + B_PAIR_GAP);
    const topY = pY0 + pairPad + B_SLOT_H / 2;
    const btmY = pY0 + pairPad + B_PAIR_H - B_SLOT_H / 2;
    const midY = (topY + btmY) / 2;
    innerMidYs.push(midY);
    lines.push(
      <line key={`ta${p}`} x1={0} y1={topY} x2={spineX1} y2={topY} />,
      <line key={`ba${p}`} x1={0} y1={btmY} x2={spineX1} y2={btmY} />,
      <line key={`va${p}`} x1={spineX1} y1={topY} x2={spineX1} y2={btmY} />,
      <line key={`ea${p}`} x1={spineX1} y1={midY} x2={spineX2} y2={midY} />,
    );
  }

  // Level 2: outer elbow per to-pair, joining consecutive inner exits
  for (let q = 0; q < toPairs; q++) {
    const mY0 = innerMidYs[2 * q];
    const mY1 = innerMidYs[2 * q + 1];
    const outerMidY = (mY0 + mY1) / 2;
    lines.push(
      <line key={`vb${q}`} x1={spineX2} y1={mY0} x2={spineX2} y2={mY1} />,
      // short exit cap from outer spine to the right edge of the connector
      <line key={`eb${q}`} x1={spineX2} y1={outerMidY} x2={B_CONN_W} y2={outerMidY} />,
    );
  }

  return (
    <div style={{ flexShrink: 0, paddingTop: B_LABEL_H }}>
      <svg width={B_CONN_W} height={totalH} style={{ display: 'block' }}>
        <g stroke="#1c4a63" strokeWidth={1} fill="none" strokeLinecap="round">
          {lines}
        </g>
      </svg>
    </div>
  );
}

function BracketRound({ name, slots, roundIndex }: { name: string; slots: BracketSlot[]; roundIndex: number }) {
  const pairs: [BracketSlot, BracketSlot][] = [];
  for (let i = 0; i + 1 < slots.length; i += 2) {
    pairs.push([slots[i], slots[i + 1]]);
  }

  // Each R(n) pair centers over 2^roundIndex R1 pairs (including their gaps).
  const spanH = Math.pow(2, roundIndex) * B_PAIR_H + (Math.pow(2, roundIndex) - 1) * B_PAIR_GAP;
  const pairPad = (spanH - B_PAIR_H) / 2;

  return (
    <div style={{ flexShrink: 0 }}>
      <div style={{ color: '#5b87a3', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 10 }}>
        {BRACKET_LABELS[name] ?? name}
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: B_PAIR_GAP }}>
        {pairs.map((pair, i) => (
          <div key={i} style={{ display: 'flex', flexDirection: 'column', gap: B_SLOT_GAP, paddingTop: pairPad, paddingBottom: pairPad }}>
            <SlotCell slot={pair[0]} />
            <div style={{ paddingLeft: 10, color: '#23577a', fontSize: 10, lineHeight: `${B_VS_H}px`, display: 'flex', alignItems: 'center', gap: 8 }}>
              vs
              {pair[0].matchId && (
                <Link to={`/watch?matchId=${pair[0].matchId}`} style={{ color: '#4fa8e0', fontSize: 10 }}>Watch</Link>
              )}
            </div>
            <SlotCell slot={pair[1]} />
          </div>
        ))}
      </div>
    </div>
  );
}

function RRStandingsTable({ group, gi }: {
  group: GameDayGroup; gi: number;
}) {
  const nameMap = new Map<string, string>();
  group.tanks.forEach(({ tankId, tankName }) => { if (tankName) nameMap.set(tankId, tankName); });
  group.standings?.forEach(({ tankId, tankName }) => { if (tankName) nameMap.set(tankId, tankName); });

  const label = `Group ${String.fromCharCode(65 + gi)}`;
  const groupLabel = (
    <div style={{ color: '#4a7291', fontSize: 11, marginBottom: 8 }}>{label}</div>
  );

  const hasStandings = (group.standings ?? []).length > 0;

  if (!hasStandings) {
    return (
      <div>
        {groupLabel}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          {group.tanks.map(({ tankId }) => (
            <Link key={tankId} to={`/tanks/${tankId}`} style={{
              color: '#7fa2ba', fontSize: 13, textDecoration: 'none',
              padding: '4px 8px', borderRadius: 0, background: '#0a3550',
            }}>
              {nameMap.get(tankId) ?? tankId}
            </Link>
          ))}
        </div>
      </div>
    );
  }

  // Tank order: sorted by standing rank (same as final display order).
  const rows = [...group.standings!].sort((a, b) => b.points - a.points || b.wins - a.wins);

  // Build a lookup: (rowTankId, colTankId) → GroupMatchResult
  const resultMap = new Map<string, GroupMatchResult>();
  (group.matchResults ?? []).forEach((r) => {
    resultMap.set(`${r.tankAId}:${r.tankBId}`, r);
    resultMap.set(`${r.tankBId}:${r.tankAId}`, { ...r, winner: r.winner === 'a' ? 'b' : r.winner === 'b' ? 'a' : r.winner });
  });

  const thStyle: React.CSSProperties = {
    color: '#5b87a3', fontSize: 10, fontWeight: 600, textTransform: 'uppercase',
    letterSpacing: '0.05em', padding: '0 6px 8px', borderBottom: '1px solid #23577a',
    textAlign: 'center',
  };
  const tdStyle: React.CSSProperties = {
    padding: '5px 6px', borderBottom: '1px solid #082e4a', verticalAlign: 'middle', textAlign: 'center',
  };

  return (
    <div>
      {groupLabel}
      <div style={{ overflowX: 'auto' }}>
        <table style={{ borderCollapse: 'collapse', fontSize: 12 }}>
          <thead>
            <tr>
              <th style={{ ...thStyle, textAlign: 'center', width: 24 }}>#</th>
              <th style={{ ...thStyle, textAlign: 'left', minWidth: 120 }}>Tank</th>
              {rows.map((_, ci) => (
                <th key={ci} style={{ ...thStyle, width: 32 }}>{ci + 1}</th>
              ))}
              <th style={{ ...thStyle, width: 36 }}>W</th>
              <th style={{ ...thStyle, width: 36 }}>L</th>
              <th style={{ ...thStyle, width: 36 }}>PTS</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((s, ri) => {
              const name = nameMap.get(s.tankId) ?? s.tankId;
              return (
                <tr key={s.tankId}>
                  <td style={{ ...tdStyle }}>
                    <span style={{ color: rankColor(ri + 1), fontWeight: 700 }}>{ri + 1}</span>
                  </td>
                  <td style={{ ...tdStyle, textAlign: 'left', padding: '5px 8px' }}>
                    <Link to={`/tanks/${s.tankId}`} style={{ color: '#e7f1f7', textDecoration: 'none' }}>
                      {wrapName(name)}
                    </Link>
                  </td>
                  {rows.map((opp, ci) => {
                    if (ri === ci) {
                      return (
                        <td key={ci} style={{ ...tdStyle, background: '#072943' }} />
                      );
                    }
                    const r = resultMap.get(`${s.tankId}:${opp.tankId}`);
                    if (!r || r.winner === '') {
                      return <td key={ci} style={{ ...tdStyle, color: '#4a7291' }}>—</td>;
                    }
                    // resultMap normalises winner so 'a' always means the row tank won.
                    const cellInfo =
                      r.winner === 'both_lose' ? { label: 'B', bg: '#3d2a10', fg: '#e8b339' } :
                      r.winner === 'a'          ? { label: 'W', bg: '#0f3d34', fg: '#59e6c0' } :
                                                  { label: 'L', bg: '#072943', fg: '#ff8a75' };
                    return (
                      <td key={ci} style={{ ...tdStyle, background: cellInfo.bg }}>
                        {r.matchId ? (
                          <Link to={`/watch?matchId=${r.matchId}`} style={{ color: cellInfo.fg, fontWeight: 700, textDecoration: 'none' }}>
                            {cellInfo.label}
                          </Link>
                        ) : (
                          <span style={{ color: cellInfo.fg, fontWeight: 700 }}>{cellInfo.label}</span>
                        )}
                      </td>
                    );
                  })}
                  <td style={{ ...tdStyle, color: '#59e6c0' }}>{s.wins}</td>
                  <td style={{ ...tdStyle, color: '#ff8a75' }}>{s.losses}</td>
                  <td style={{ ...tdStyle, color: '#ffab6b', fontWeight: 700 }}>{s.points}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function rankColor(rank: number): string {
  if (rank === 1) return '#e8b339';
  if (rank === 2) return '#7fa2ba';
  if (rank === 3) return '#b8892f';
  return '#4a7291';
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
    background: '#0a3550', border: '1px solid #23577a', borderRadius: 0,
    color: '#e7f1f7', padding: '4px 8px', fontSize: 12, width: '100%',
  };

  return (
    <div style={{ ...cardStyle, marginBottom: 20 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 14 }}>
        <span style={{ color: '#5b87a3', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em' }}>
          Registered Tanks{roster.length > 0 ? ` — ${roster.length}` : ''}
        </span>
        {isAdmin && (
          <button
            onClick={() => setShowPicker((v) => !v)}
            style={{ ...ghostButtonStyle, fontSize: 11, padding: '3px 10px', borderColor: '#ffab6b', color: '#ffab6b' }}
          >
            {showPicker ? 'Close' : '+ Add'}
          </button>
        )}
      </div>

      {err && <p style={{ color: '#ff8a75', fontSize: 12, margin: '0 0 10px' }}>{err}</p>}

      {roster.length === 0 ? (
        <p style={{ color: '#4a7291', fontSize: 13, margin: 0 }}>No tanks registered yet.</p>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {roster.map(({ tankId, version }, idx) => {
            const info = tankInfo.get(tankId);
            const name = info?.name ?? `…${tankId.slice(-8)}`;
            const author = info?.authorName ?? null;
            return (
              <div key={`${tankId}-${idx}`} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '4px 0', borderBottom: '1px solid #082e4a' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                  <Link to={`/tanks/${tankId}`} style={{ color: '#e7f1f7', fontSize: 13, textDecoration: 'none', fontWeight: 500 }}>
                    {name}
                  </Link>
                  {author && <span style={{ color: '#5b87a3', fontSize: 12 }}>by {author}</span>}
                  <span style={{ color: '#4a7291', fontSize: 11 }}>@ {version}</span>
                </div>
                {isAdmin && (
                  <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                    {confirmRemove === tankId ? (
                      <>
                        <button
                          onClick={() => handleRemove(tankId)}
                          disabled={busy === tankId}
                          style={{ ...ghostButtonStyle, fontSize: 11, padding: '2px 8px', color: '#ff8a75', borderColor: '#e0503a', background: 'rgba(220,38,38,0.08)' }}
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
                        style={{ ...ghostButtonStyle, fontSize: 11, padding: '2px 8px', color: '#ff8a75', borderColor: '#3a1a18' }}
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
        <div style={{ borderTop: '1px solid #23577a', marginTop: 14, paddingTop: 12 }}>
          {aiTanks.length > 0 && (
            <div style={{ marginBottom: 12 }}>
              <div style={{ color: '#5b87a3', fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 6 }}>AI Tanks</div>
              {aiTanks.map((t) => {
                const ver = t.versions[0]?.version ?? 'v0.1';
                return (
                  <div key={t.tankId} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 6 }}>
                    <span style={{ color: '#e7f1f7', fontSize: 13 }}>
                      {t.name} <span style={{ color: '#4a7291' }}>@ {ver}</span>
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

          <div style={{ color: '#5b87a3', fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 6 }}>Add by Name</div>
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
                background: '#082e4a', border: '1px solid #23577a', borderRadius: 0,
                boxShadow: '0 4px 16px rgba(0,0,0,0.4)',
              }}>
                {searchResults.length === 0 ? (
                  <div style={{ padding: '6px 10px', color: '#4a7291', fontSize: 12 }}>No tanks found</div>
                ) : (
                  searchResults.map((t) => (
                    <button
                      key={t.tankId}
                      onMouseDown={() => selectTank(t)}
                      style={{
                        display: 'flex', width: '100%', alignItems: 'center', justifyContent: 'space-between',
                        padding: '6px 10px', background: 'transparent', border: 'none',
                        borderBottom: '1px solid #0a3550', cursor: 'pointer', textAlign: 'left',
                      }}
                    >
                      <span style={{ color: '#e7f1f7', fontSize: 13 }}>{t.name}</span>
                      {t.authorName && (
                        <span style={{ color: '#5b87a3', fontSize: 11 }}>by {t.authorName}</span>
                      )}
                    </button>
                  ))
                )}
              </div>
            )}
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr auto', gap: 6, alignItems: 'end' }}>
            <div>
              <div style={{ color: '#5b87a3', fontSize: 10, marginBottom: 2 }}>Tank ID</div>
              <input
                value={manualId}
                onChange={(e) => { setManualId(e.target.value); setSearchQuery(''); setMajorVersions([]); setManualVer(''); }}
                onBlur={(e) => { const id = e.target.value.trim(); if (id && majorVersions.length === 0) fetchVersionsFor(id); }}
                placeholder="tank-uuid"
                style={inpStyle}
              />
            </div>
            <div>
              <div style={{ color: '#5b87a3', fontSize: 10, marginBottom: 2 }}>Version</div>
              {verLoading ? (
                <div style={{ ...inpStyle, color: '#4a7291' }}>…</div>
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
  const [bracketPage, setBracketPage] = useState(0);
  const [viewportWidth, setViewportWidth] = useState(() => window.innerWidth);
  const { user } = useAuthStore();

  useEffect(() => {
    function onResize() { setViewportWidth(window.innerWidth); }
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

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

  if (loading) return <Layout><div style={{ color: '#5b87a3', padding: '40px 0' }}>Loading…</div></Layout>;
  if (error || !gameDay) return <Layout><div style={{ color: '#ff8a75' }}>{error ?? 'Game day not found'}</div></Layout>;

  const bracketRounds = Object.entries(gameDay.bracket ?? {})
    .filter(([, slots]) => slots.length > 0)
    .sort(([a], [b]) => {
      const order = ['r1', 'r2', 'r3', 'r4', 'r5', 'final'];
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
          <h1 style={{ margin: '0 0 4px', color: '#e7f1f7', fontSize: 22, fontWeight: 700 }}>
            {gameDay.name ? `${gameDay.name}` : 'Game Day'}
          </h1>
          <div style={{ color: '#5b87a3', fontSize: 13 }}>
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
          <div style={{ color: '#ffab6b', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 10 }}>
            Admin Info
          </div>
          <div style={{ display: 'flex', gap: 20, flexWrap: 'wrap', fontSize: 13 }}>
            <div>
              <span style={{ color: '#5b87a3' }}>Auto-fill bracket: </span>
              <span style={{ color: gameDay.autofill ? '#59e6c0' : '#ff8a75', fontWeight: 600 }}>
                {gameDay.autofill ? 'ON' : 'OFF'}
              </span>
            </div>
            <div>
              <span style={{ color: '#5b87a3' }}>Maps: </span>
              <span style={{ color: '#e7f1f7' }}>
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
        <div style={{ color: '#5b87a3', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 14 }}>
          Schedule
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <span style={{ color: '#7fa2ba', fontSize: 14 }}>Registration closes</span>
            <span style={{ color: '#4a7291', fontSize: 12 }}>
              {new Date(schedule.registrationClose).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
            </span>
          </div>
          <PhaseRow label="Round Robin" phase={phases.roundRobin} scheduledAt={schedule.roundRobin} />
          {phases.roundRobin.status !== 'complete' ? (
            // Bracket not yet built — tank count unknown; skip pre-computed placeholder slots.
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <span style={{ color: '#5b87a3', fontSize: 13, fontStyle: 'italic' }}>
                Elimination rounds (TBD — based on tank count)
              </span>
            </div>
          ) : (
            (schedule.elimination ?? []).map((ts, i) => {
              const ps = phases.elimination?.[`r${i + 1}`];
              return ps ? (
                <PhaseRow key={i} label={`Elimination R${i + 1}`} phase={ps} scheduledAt={ts} />
              ) : (
                <div key={i} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                  <span style={{ color: '#7fa2ba', fontSize: 14 }}>Elimination R{i + 1}</span>
                  <span style={{ color: '#4a7291', fontSize: 12 }}>
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
            scheduledAt={schedule.final}
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
          <p style={{ margin: 0, color: '#e8b339', fontSize: 13, lineHeight: 1.5 }}>
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
          <p style={{ margin: 0, color: '#7fa2ba', fontSize: 13, lineHeight: 1.5 }}>
            Results not available — ranking data was not recorded for this tournament.
          </p>
        </div>
      )}

      {/* Groups cross-tables */}
      {showGroups && (
        <div style={{ ...cardStyle, marginBottom: 20 }}>
          <div style={{ color: '#5b87a3', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 14 }}>
            Groups
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
            {(gameDay.groups ?? []).map((group, gi) => (
              <RRStandingsTable key={group.groupId} group={group} gi={gi} />
            ))}
          </div>
        </div>
      )}

      {/* Bracket with 3-round pagination */}
      {showBracket && (() => {
        const totalRounds = bracketRounds.length;
        const PAGE_SIZE = viewportWidth < 640 ? 1 : viewportWidth < 1024 ? 2 : 3;
        const maxPage = Math.max(0, totalRounds - PAGE_SIZE + 1 - 1);
        const clampedPage = Math.min(bracketPage, maxPage);
        const pageStart = clampedPage;
        const pageEnd = Math.min(pageStart + PAGE_SIZE, totalRounds);
        const visibleRounds = bracketRounds.slice(pageStart, pageEnd);
        return (
          <div style={{ ...cardStyle, marginBottom: 20, overflowX: 'auto' }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
              <div style={{ color: '#5b87a3', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                Bracket
              </div>
              {totalRounds > PAGE_SIZE && (
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <button
                    onClick={() => setBracketPage(Math.max(0, clampedPage - 1))}
                    disabled={clampedPage === 0}
                    style={{ ...ghostButtonStyle, padding: '2px 10px', fontSize: 14, opacity: clampedPage === 0 ? 0.3 : 1 }}
                  >‹</button>
                  <span style={{ color: '#4a7291', fontSize: 11 }}>
                    R{pageStart + 1}–R{pageEnd}
                  </span>
                  <button
                    onClick={() => setBracketPage(Math.min(maxPage, clampedPage + 1))}
                    disabled={clampedPage >= maxPage}
                    style={{ ...ghostButtonStyle, padding: '2px 10px', fontSize: 14, opacity: clampedPage >= maxPage ? 0.3 : 1 }}
                  >›</button>
                </div>
              )}
            </div>
            <div style={{ display: 'flex', overflow: 'hidden', paddingBottom: 4 }}>
              {visibleRounds.map(([name, slots], vi) => {
                const globalIndex = pageStart + vi;
                return (
                  <React.Fragment key={name}>
                    {vi > 0 && (
                      <BracketConnector
                        fromRoundIndex={globalIndex - 1}
                        fromSlots={bracketRounds[globalIndex - 1][1]}
                        toSlots={slots}
                      />
                    )}
                    <BracketRound name={name} slots={slots} roundIndex={globalIndex} />
                  </React.Fragment>
                );
              })}
            </div>
          </div>
        );
      })()}

      {/* Final standings — below all group cross-tables and bracket */}
      {standings.length > 0 && (
        <div style={{ ...cardStyle, marginBottom: 20 }}>
          <div style={{ color: '#5b87a3', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 14 }}>
            Final standings
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {standings.map(([tankId, pts], i) => (
              <div key={tankId} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '2px 0' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ color: rankColor(i + 1), fontSize: 13, fontWeight: 700, width: 20 }}>{i + 1}</span>
                  <Link to={`/tanks/${tankId}`} style={{ color: '#7fa2ba', fontSize: 13, textDecoration: 'none' }}>
                    {tankNameMap.get(tankId) ?? tankId}
                  </Link>
                </div>
                <span style={{ color: '#ffab6b', fontWeight: 600, fontSize: 13 }}>+{pts} pts</span>
              </div>
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
