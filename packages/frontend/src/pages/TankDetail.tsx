import { useEffect, useState } from 'react';
import { useParams, Link, useNavigate } from 'react-router-dom';
import Layout, { cardStyle, primaryButtonStyle, ghostButtonStyle } from '../components/Layout';
import { getTank, deleteTank } from '../services/api';
import type { Tank, TankVersion } from '../types';
import ForkDialog from '../components/ForkDialog';

function majorOf(version: string): string {
  const m = version.match(/^(v\d+)/);
  return m ? m[1] : version;
}

function isMajor(v: string): boolean {
  return /^v\d+$/.test(v);
}

function pct(n: number | null): string {
  if (n === null) return '—';
  return `${Math.round(n * 100)}%`;
}

function num(n: number | null, decimals = 0): string {
  if (n === null) return '—';
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

function MajorVersionCard({ major, minors }: { major: TankVersion; minors: TankVersion[] }) {
  const [expanded, setExpanded] = useState(false);

  return (
    <div style={{ ...cardStyle, marginBottom: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 14 }}>
        <div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
            <span style={{ color: '#a78bfa', fontWeight: 700, fontSize: 15 }}>{major.version}</span>
            <StatusDot status={major.compileStatus} />
            <span style={{ color: '#64748b', fontSize: 12 }}>{major.compileStatus}</span>
            {major.disqualified && (
              <span style={{ background: '#f87171', color: '#fff', fontSize: 10, padding: '1px 6px', borderRadius: 4, fontWeight: 600 }}>DQ</span>
            )}
            {major.registeredForGameDay && (
              <span style={{ background: '#4ade80', color: '#0f0f1a', fontSize: 10, padding: '1px 6px', borderRadius: 4, fontWeight: 600 }}>REGISTERED</span>
            )}
          </div>
          <div style={{ color: '#64748b', fontSize: 12 }}>
            {relativeTime(major.createdAt)} · {new Date(major.createdAt * 1000).toLocaleDateString()}
          </div>
        </div>

        {(major.matchesPlayed ?? 0) > 0 ? (
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
        ) : (
          <span style={{ color: '#475569', fontSize: 12 }}>No ranked matches yet</span>
        )}
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
                  <span style={{ color: '#475569', fontSize: 11 }}>
                    {new Date(mv.createdAt * 1000).toLocaleDateString()}
                  </span>
                </div>
              ))}
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
  const [tank, setTank] = useState<(Tank & { versions: TankVersion[] }) | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showFork, setShowFork] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);

  useEffect(() => {
    if (!tankId) return;
    getTank(tankId)
      .then(setTank)
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }, [tankId]);

  if (loading) {
    return <Layout><div style={{ color: '#64748b', padding: '40px 0' }}>Loading…</div></Layout>;
  }
  if (error || !tank) {
    return <Layout><div style={{ color: '#f87171' }}>{error ?? 'Tank not found'}</div></Layout>;
  }

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

  return (
    <Layout>
      <div style={{ marginBottom: 20 }}>
        <Link to="/dashboard" style={{ color: '#64748b', fontSize: 13, textDecoration: 'none' }}>
          ← My tanks
        </Link>
      </div>

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 28 }}>
        <div>
          <h1 style={{ margin: '0 0 6px', color: '#e2e8f0', fontSize: 26, fontWeight: 700 }}>
            {tank.name}
          </h1>
          <div style={{ display: 'flex', gap: 16, alignItems: 'center', flexWrap: 'wrap', marginBottom: 10 }}>
            <span style={{ color: '#a78bfa', fontSize: 20, fontWeight: 700 }}>
              {tank.globalScore.toLocaleString()} pts
            </span>
            {tank.bestFinish !== null && (
              <span style={{ color: '#94a3b8', fontSize: 13 }}>Best: {ordinal(tank.bestFinish)}</span>
            )}
            <span style={{ color: '#64748b', fontSize: 13 }}>
              {tank.gameDaysCount} game {tank.gameDaysCount === 1 ? 'day' : 'days'}
            </span>
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

        <div style={{ display: 'flex', gap: 8, flexShrink: 0, marginLeft: 16, alignItems: 'center' }}>
          {latestReadyMajor && !tank.scoreTransferredTo && (
            <button onClick={() => setShowFork(true)} style={ghostButtonStyle}>Fork</button>
          )}
          <button onClick={() => navigate(`/tanks/${tankId}/edit`)} style={primaryButtonStyle}>
            Edit
          </button>
          {confirmDelete ? (
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
                    alert(e instanceof Error ? e.message : 'Delete failed');
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
          <MajorVersionCard key={major.version} major={major} minors={minorsByMajor[major.version] ?? []} />
        ))
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
    </Layout>
  );
}
