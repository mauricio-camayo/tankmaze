import { useEffect, useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import Layout, { cardStyle } from '../components/Layout';
import { getGameDay } from '../services/api';
import type { GameDay, BracketSlot, GameDayPhaseStatus } from '../types';

const PHASE_LABELS: Record<string, string> = {
  roundRobin: 'Round Robin',
  eliminationR1: 'Elimination R1',
  eliminationR2: 'Elimination R2',
  final: 'Final',
};

const PHASE_ORDER = ['roundRobin', 'eliminationR1', 'eliminationR2', 'final'];

function PhaseBadge({ status }: { status: GameDayPhaseStatus['status'] }) {
  const styles: Record<string, [string, string]> = {
    upcoming: ['#fbbf24', 'rgba(251,191,36,0.1)'],
    running: ['#4ade80', 'rgba(74,222,128,0.1)'],
    complete: ['#475569', 'rgba(71,85,105,0.1)'],
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

function SlotCell({ slot }: { slot: BracketSlot }) {
  const statusColor: Record<string, string> = {
    won: '#4ade80', lost: '#475569', both_lose: '#f87171', playing: '#fbbf24', bye: '#2d2d4e',
  };
  const color = statusColor[slot.status] ?? '#94a3b8';

  return (
    <div style={{
      padding: '6px 10px', borderRadius: 6,
      border: `1px solid ${color}30`,
      background: `${color}08`,
      fontSize: 12, minWidth: 140,
    }}>
      {slot.tankId ? (
        <Link to={`/tanks/${slot.tankId}`} style={{ color, textDecoration: 'none' }}>
          …{slot.tankId.slice(-8)}
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
        {PHASE_LABELS[name] ?? name}
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

function rankColor(rank: number): string {
  if (rank === 1) return '#fbbf24';
  if (rank === 2) return '#94a3b8';
  if (rank === 3) return '#c2773d';
  return '#475569';
}

export default function GameDayPage() {
  const { gameDayId } = useParams<{ gameDayId: string }>();
  const [gameDay, setGameDay] = useState<GameDay | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!gameDayId) return;
    getGameDay(gameDayId)
      .then(setGameDay)
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }, [gameDayId]);

  if (loading) return <Layout><div style={{ color: '#64748b', padding: '40px 0' }}>Loading…</div></Layout>;
  if (error || !gameDay) return <Layout><div style={{ color: '#f87171' }}>{error ?? 'Game day not found'}</div></Layout>;

  const phases = PHASE_ORDER.filter((p) => p in gameDay.phases);

  const bracketRounds = Object.entries(gameDay.bracket)
    .filter(([, slots]) => slots.length > 0)
    .sort(([a], [b]) => PHASE_ORDER.indexOf(a) - PHASE_ORDER.indexOf(b));

  const standings = Object.entries(gameDay.placementPoints)
    .sort(([, a], [, b]) => b - a);

  const showGroups = gameDay.groups.length > 0;
  const showBracket = bracketRounds.length > 0;
  const showRegistered = !showGroups && !showBracket && gameDay.registeredTanks.length > 0;

  return (
    <Layout>
      <div style={{ marginBottom: 24 }}>
        <h1 style={{ margin: '0 0 4px', color: '#e2e8f0', fontSize: 22, fontWeight: 700 }}>
          Game Day
        </h1>
        <div style={{ color: '#64748b', fontSize: 13 }}>
          {new Date(gameDay.createdAt * 1000).toLocaleDateString(undefined, {
            weekday: 'long', year: 'numeric', month: 'long', day: 'numeric',
          })}
        </div>
      </div>

      {/* Phase timeline */}
      {phases.length > 0 && (
        <div style={{ ...cardStyle, marginBottom: 20 }}>
          <div style={{ color: '#64748b', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 14 }}>
            Phases
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {phases.map((phase) => {
              const ps = gameDay.phases[phase];
              const timeLabel = ps.status === 'complete' && ps.endedAt
                ? `ended ${new Date(ps.endedAt * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
                : ps.status === 'running' && ps.startedAt
                ? `started ${new Date(ps.startedAt * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
                : '';
              return (
                <div key={phase} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                    <PhaseBadge status={ps.status} />
                    <span style={{ color: '#e2e8f0', fontSize: 14 }}>{PHASE_LABELS[phase] ?? phase}</span>
                  </div>
                  {timeLabel && <span style={{ color: '#475569', fontSize: 12 }}>{timeLabel}</span>}
                </div>
              );
            })}
          </div>
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
                {gameDay.groups.map((group, gi) => (
                  <div key={gi}>
                    <div style={{ color: '#475569', fontSize: 11, marginBottom: 6 }}>
                      Group {String.fromCharCode(65 + gi)}
                    </div>
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                      {group.map((tankId) => (
                        <Link key={tankId} to={`/tanks/${tankId}`} style={{
                          color: '#94a3b8', fontSize: 13, textDecoration: 'none',
                          padding: '4px 8px', borderRadius: 4, background: '#0f0f1a',
                          display: 'flex', justifyContent: 'space-between', alignItems: 'center',
                        }}>
                          <span>…{tankId.slice(-8)}</span>
                          {gameDay.placementPoints[tankId] !== undefined && (
                            <span style={{ color: '#a78bfa', fontSize: 12, fontWeight: 600 }}>
                              +{gameDay.placementPoints[tankId]} pts
                            </span>
                          )}
                        </Link>
                      ))}
                    </div>
                  </div>
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
                        …{tankId.slice(-8)}
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

      {/* Pre-tournament: just show registered tanks */}
      {showRegistered && (
        <div style={cardStyle}>
          <div style={{ color: '#64748b', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 14 }}>
            Registered — {gameDay.registeredTanks.length} tank{gameDay.registeredTanks.length !== 1 ? 's' : ''}
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
            {gameDay.registeredTanks.map(({ tankId, version }) => (
              <Link key={tankId} to={`/tanks/${tankId}`} style={{
                color: '#94a3b8', fontSize: 12, textDecoration: 'none',
                padding: '4px 10px', borderRadius: 6,
                background: '#0f0f1a', border: '1px solid #2d2d4e',
              }}>
                …{tankId.slice(-8)} <span style={{ color: '#475569' }}>@ {version}</span>
              </Link>
            ))}
          </div>
        </div>
      )}
    </Layout>
  );
}
