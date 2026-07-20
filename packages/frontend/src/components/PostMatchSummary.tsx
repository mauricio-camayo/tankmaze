import type { MatchSnapshot } from '../types';
import type { MatchOverStats } from '../services/ws';

interface PostMatchSummaryProps {
  matchId: string;
  snapshot: MatchSnapshot;
  matchOver: { winner: 'a' | 'b' | null; reason: string; stats: MatchOverStats };
  authorNames?: { a?: string; b?: string };
}

const REASON_LABELS: Record<string, string> = {
  opponent_destroyed: 'Opponent destroyed',
  code_crash: 'Code crash',
  damage_tiebreak: 'Damage tiebreak',
  moves_tiebreak: 'Moves tiebreak',
  both_lose: 'Both tanks lost',
};

function reasonLabel(reason: string): string {
  return REASON_LABELS[reason] ?? reason.replace(/_/g, ' ');
}

function accuracy(hits: number, shots: number): string {
  if (shots === 0) return '—';
  return `${Math.round((hits / shots) * 100)}%`;
}

function formatDuration(ms: number): string {
  if (ms <= 0) return '—';
  return `${(ms / 1000).toFixed(1)}s`;
}

const statRowStyle: React.CSSProperties = {
  display: 'grid', gridTemplateColumns: '1fr 72px 1fr',
  alignItems: 'center', padding: '6px 0',
  borderBottom: '1px solid #123a54', fontSize: 13,
};
const valStyle: React.CSSProperties = { color: '#e7f1f7', textAlign: 'center', fontVariantNumeric: 'tabular-nums' };
const labelStyle: React.CSSProperties = { color: '#5b87a3', textAlign: 'center', fontSize: 11 };

function StatRow({ label, a, b }: { label: string; a: React.ReactNode; b: React.ReactNode }) {
  return (
    <div style={statRowStyle}>
      <div style={{ ...valStyle, textAlign: 'right', paddingRight: 10 }}>{a}</div>
      <div style={labelStyle}>{label}</div>
      <div style={{ ...valStyle, textAlign: 'left', paddingLeft: 10 }}>{b}</div>
    </div>
  );
}

// Post-Match Summary panel (functional spec §11). Accessible to both Authors
// and any observer — sourced entirely from the MATCH_OVER WS broadcast, which
// reaches unauthenticated viewers the same as the REST match record does for
// logged-in ones, so this renders whether or not authorNames resolved.
export default function PostMatchSummary({ matchId, snapshot, matchOver, authorNames }: PostMatchSummaryProps) {
  const { stats, winner, reason } = matchOver;
  const nameA = snapshot.tankA.config.name || snapshot.tankA.tankId;
  const nameB = snapshot.tankB.config.name || snapshot.tankB.tankId;
  const replayUrl = `${window.location.origin}/watch?matchId=${matchId}`;

  const outcomeText =
    winner === 'a' ? `${nameA} wins` : winner === 'b' ? `${nameB} wins` : 'Both tanks lost';

  return (
    <div style={{
      marginTop: 14, background: '#0a3550', border: '1px solid #23577a',
      borderRadius: 0, padding: 16, color: '#e7f1f7',
    }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', marginBottom: 10, flexWrap: 'wrap', gap: 6 }}>
        <div style={{ fontSize: 15, fontWeight: 700 }}>{outcomeText}</div>
        <div style={{ fontSize: 12, color: '#5b87a3' }}>{reasonLabel(reason)}</div>
      </div>

      {/* Winner/Loser identity row */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 72px 1fr', marginBottom: 8 }}>
        <div style={{ textAlign: 'right', paddingRight: 10 }}>
          <div style={{ color: '#4fa8e0', fontWeight: 600, fontSize: 13 }}>{nameA}</div>
          <div style={{ color: '#5b87a3', fontSize: 11 }}>
            {authorNames?.a ? `by ${authorNames.a} · ` : ''}v{snapshot.tankA.version}
          </div>
        </div>
        <div />
        <div style={{ textAlign: 'left', paddingLeft: 10 }}>
          <div style={{ color: '#ff7a29', fontWeight: 600, fontSize: 13 }}>{nameB}</div>
          <div style={{ color: '#5b87a3', fontSize: 11 }}>
            {authorNames?.b ? `by ${authorNames.b} · ` : ''}v{snapshot.tankB.version}
          </div>
        </div>
      </div>

      <StatRow label="Final HP" a={`${stats.finalHpA} HP`} b={`${stats.finalHpB} HP`} />
      <StatRow label="Damage dealt" a={stats.damageA} b={stats.damageB} />
      <StatRow label="Moves made" a={stats.movesA} b={stats.movesB} />
      <StatRow
        label="Shots / hits / acc."
        a={`${stats.shotsFiredA} / ${stats.hitsA} / ${accuracy(stats.hitsA, stats.shotsFiredA)}`}
        b={`${stats.shotsFiredB} / ${stats.hitsB} / ${accuracy(stats.hitsB, stats.shotsFiredB)}`}
      />
      <StatRow label="Tick violations" a={stats.tickViolationsA} b={stats.tickViolationsB} />
      {stats.flawless && (
        <div style={{ textAlign: 'center', color: '#59e6c0', fontSize: 12, marginTop: 6 }}>Flawless victory</div>
      )}

      <div style={{
        display: 'flex', justifyContent: 'space-between', alignItems: 'center',
        marginTop: 12, paddingTop: 10, borderTop: '1px solid #123a54', flexWrap: 'wrap', gap: 8,
      }}>
        <div style={{ fontSize: 12, color: '#5b87a3' }}>
          {stats.ticksElapsed} ticks · {formatDuration(stats.durationMs)}
        </div>
        <a href={replayUrl} style={{ color: '#4fa8e0', fontSize: 12 }}>
          Full replay ↗
        </a>
      </div>
    </div>
  );
}
