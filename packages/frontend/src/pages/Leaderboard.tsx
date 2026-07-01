import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import Layout, { cardStyle } from '../components/Layout';
import { getRankings } from '../services/api';
import type { RankingEntry } from '../types';

const DECAY_DAYS = 90;
const PAGE_SIZE = 20;
const COLS = '48px 1fr 140px 88px 60px 50px 110px';

function ordinal(n: number | null): string {
  if (n === null) return '—';
  const s = ['th', 'st', 'nd', 'rd'];
  const v = n % 100;
  return `${n}${s[(v - 20) % 10] ?? s[v] ?? s[0]}`;
}

function rankColor(rank: number): string {
  if (rank === 1) return '#fbbf24';
  if (rank === 2) return '#94a3b8';
  if (rank === 3) return '#c2773d';
  return '#475569';
}

function DecayBar({ lastActiveAt }: { lastActiveAt: number | null }) {
  if (!lastActiveAt) return <span style={{ color: '#475569', fontSize: 12 }}>—</span>;

  const daysSince = (Date.now() / 1000 - lastActiveAt) / 86400;
  const fill = Math.min(1, daysSince / DECAY_DAYS);
  const barColor = fill > 0.75 ? '#f87171' : fill > 0.4 ? '#fbbf24' : '#4ade80';

  let label: string;
  if (daysSince < 1) label = 'today';
  else if (daysSince < 30) label = `${Math.floor(daysSince)}d ago`;
  else label = `${Math.floor(daysSince / 30)}mo ago`;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 80 }}>
      <span style={{ color: '#94a3b8', fontSize: 11 }}>{label}</span>
      <div style={{ height: 3, borderRadius: 2, background: '#2d2d4e', overflow: 'hidden' }}>
        <div style={{ height: '100%', width: `${fill * 100}%`, background: barColor, borderRadius: 2 }} />
      </div>
    </div>
  );
}

function LeaderboardRow({ entry, onClick }: { entry: RankingEntry; onClick: () => void }) {
  const [hovered, setHovered] = useState(false);

  return (
    <div
      onClick={onClick}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{
        display: 'grid', gridTemplateColumns: COLS,
        alignItems: 'center', gap: 8, padding: '10px 4px',
        borderBottom: '1px solid #2d2d4e', cursor: 'pointer',
        background: hovered ? 'rgba(255,255,255,0.03)' : 'transparent',
        transition: 'background 0.1s', minWidth: 560,
      }}
    >
      <span style={{ fontWeight: 700, fontSize: 14, color: rankColor(entry.rank) }}>
        {entry.rank}
      </span>
      <div>
        <div style={{ color: '#e2e8f0', fontSize: 14 }}>{entry.tankName}</div>
        <div style={{ color: '#475569', fontSize: 11 }}>{entry.activeVersion}</div>
      </div>
      <span style={{ color: '#94a3b8', fontSize: 13 }}>{entry.authorUsername}</span>
      <span style={{ color: '#a78bfa', fontWeight: 600, fontSize: 14, textAlign: 'right' }}>
        {entry.globalScore.toLocaleString()}
      </span>
      <span style={{ color: '#94a3b8', fontSize: 13, textAlign: 'center' }}>
        {ordinal(entry.bestFinish)}
      </span>
      <span style={{ color: '#94a3b8', fontSize: 13, textAlign: 'center' }}>
        {entry.gameDays}
      </span>
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <DecayBar lastActiveAt={entry.lastActiveAt} />
      </div>
    </div>
  );
}

const thStyle: React.CSSProperties = {
  color: '#475569', fontSize: 11, fontWeight: 600,
  textTransform: 'uppercase', letterSpacing: '0.05em',
};

export default function Leaderboard() {
  const navigate = useNavigate();
  const [entries, setEntries] = useState<RankingEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [page, setPage] = useState(1);

  useEffect(() => {
    getRankings()
      .then((data) => { setEntries(data); setPage(1); })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }, []);

  const pageCount = Math.ceil(entries.length / PAGE_SIZE);
  const visible = entries.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE);

  return (
    <Layout>
      <h1 style={{ margin: '0 0 24px', color: '#e2e8f0', fontSize: 22, fontWeight: 700 }}>
        Leaderboard
      </h1>

      {loading && <div style={{ color: '#64748b' }}>Loading…</div>}
      {error && <div style={{ color: '#f87171' }}>{error}</div>}

      {!loading && !error && entries.length === 0 && (
        <div style={{ ...cardStyle, color: '#64748b', textAlign: 'center', padding: '40px 24px' }}>
          No rankings yet. Compete in a game day to appear here.
        </div>
      )}

      {!loading && !error && entries.length > 0 && (
        <div style={cardStyle}>
          <div className="tm-table-scroll">
          <div style={{
            display: 'grid', gridTemplateColumns: COLS,
            alignItems: 'center', gap: 8, padding: '0 4px 10px',
            borderBottom: '1px solid #2d2d4e', marginBottom: 2, minWidth: 560,
          }}>
            <span style={thStyle}>#</span>
            <span style={thStyle}>Tank</span>
            <span style={thStyle}>Author</span>
            <span style={{ ...thStyle, textAlign: 'right' }}>Score</span>
            <span style={{ ...thStyle, textAlign: 'center' }}>Best</span>
            <span style={{ ...thStyle, textAlign: 'center' }}>Days</span>
            <span style={{ ...thStyle, textAlign: 'right' }}>Last active</span>
          </div>

          {visible.map((e) => (
            <LeaderboardRow key={e.tankId} entry={e} onClick={() => navigate(`/tanks/${e.tankId}`)} />
          ))}
          </div>{/* end tm-table-scroll */}

          {pageCount > 1 && (
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 16, padding: '16px 4px 4px' }}>
              <button
                onClick={() => setPage((p) => p - 1)}
                disabled={page === 1}
                style={{
                  background: 'none', border: '1px solid #2d2d4e', color: page === 1 ? '#475569' : '#94a3b8',
                  borderRadius: 6, padding: '4px 14px', cursor: page === 1 ? 'default' : 'pointer', fontSize: 13,
                  minHeight: 44,
                }}
              >
                Prev
              </button>
              <span style={{ color: '#64748b', fontSize: 13 }}>Page {page} of {pageCount}</span>
              <button
                onClick={() => setPage((p) => p + 1)}
                disabled={page === pageCount}
                style={{
                  background: 'none', border: '1px solid #2d2d4e', color: page === pageCount ? '#475569' : '#94a3b8',
                  borderRadius: 6, padding: '4px 14px', cursor: page === pageCount ? 'default' : 'pointer', fontSize: 13,
                  minHeight: 44,
                }}
              >
                Next
              </button>
            </div>
          )}
        </div>
      )}
    </Layout>
  );
}
