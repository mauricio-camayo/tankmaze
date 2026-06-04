import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import Layout, { cardStyle, primaryButtonStyle, ghostButtonStyle } from '../components/Layout';
import { listGameDays, createGameDay, deleteGameDay } from '../services/api';
import { useAuthStore } from '../store/authStore';
import type { GameDay, GameDayPhaseStatus } from '../types';

function phaseOverallStatus(gd: GameDay): 'upcoming' | 'active' | 'complete' {
  const { phases } = gd;
  if (phases.final.status === 'complete') return 'complete';
  if (
    phases.roundRobin.status === 'running' ||
    phases.final.status === 'running' ||
    Object.values(phases.elimination ?? {}).some((p) => p.status === 'running')
  ) return 'active';
  if (
    phases.roundRobin.status === 'complete' ||
    phases.final.status !== 'upcoming'
  ) return 'active';
  return 'upcoming';
}

function StatusBadge({ status }: { status: 'upcoming' | 'active' | 'complete' }) {
  const map: Record<string, [string, string]> = {
    upcoming: ['#fbbf24', 'rgba(251,191,36,0.1)'],
    active: ['#4ade80', 'rgba(74,222,128,0.1)'],
    complete: ['#475569', 'rgba(71,85,105,0.1)'],
  };
  const [fg, bg] = map[status];
  return (
    <span style={{
      color: fg, background: bg, border: `1px solid ${fg}`,
      borderRadius: 4, fontSize: 11, padding: '2px 8px', fontWeight: 600, textTransform: 'uppercase',
    }}>
      {status}
    </span>
  );
}

function PhaseDot({ phase }: { phase: GameDayPhaseStatus }) {
  const color = phase.status === 'complete' ? '#475569' : phase.status === 'running' ? '#4ade80' : '#2d2d4e';
  return (
    <span style={{ display: 'inline-block', width: 8, height: 8, borderRadius: '50%', background: color, marginRight: 4 }} />
  );
}

function GameDayRow({ gd, onDeleted }: { gd: GameDay; onDeleted: () => void }) {
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const { user } = useAuthStore();
  const status = phaseOverallStatus(gd);

  async function handleDelete() {
    if (!confirmDelete) { setConfirmDelete(true); return; }
    setDeleting(true);
    try {
      await deleteGameDay(gd.gameDayId);
      onDeleted();
    } catch {
      setDeleting(false);
      setConfirmDelete(false);
    }
  }

  return (
    <div style={{ ...cardStyle, display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 16 }}>
      <div style={{ flex: 1 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 6 }}>
          <StatusBadge status={status} />
          <span style={{ color: '#e2e8f0', fontSize: 15, fontWeight: 600 }}>
            {new Date(gd.createdAt * 1000).toLocaleDateString(undefined, {
              weekday: 'short', year: 'numeric', month: 'short', day: 'numeric',
            })}
          </span>
        </div>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', fontSize: 12, color: '#64748b' }}>
          <span>
            <PhaseDot phase={gd.phases.roundRobin} />
            Round Robin — {new Date(gd.schedule.roundRobin).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
          </span>
          <span>
            <PhaseDot phase={gd.phases.final} />
            Final — {new Date(gd.schedule.final).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
          </span>
          {gd.registeredTanks.length > 0 && (
            <span style={{ color: '#a78bfa' }}>{gd.registeredTanks.length} registered</span>
          )}
        </div>
      </div>
      <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
        <Link
          to={`/gameday/${gd.gameDayId}`}
          style={{ ...ghostButtonStyle, textDecoration: 'none', display: 'inline-block' }}
        >
          View
        </Link>
        {user?.isAdmin && status === 'upcoming' && (
          <button
            onClick={handleDelete}
            disabled={deleting}
            style={{ ...ghostButtonStyle, borderColor: '#7f1d1d', color: '#f87171', cursor: 'pointer' }}
          >
            {deleting ? 'Deleting…' : confirmDelete ? 'Confirm?' : 'Delete'}
          </button>
        )}
      </div>
    </div>
  );
}

function localDatetimeValue(dt?: Date): string {
  const d = dt ?? new Date();
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function toISO(localDt: string): string {
  return new Date(localDt).toISOString();
}

function defaultSchedule() {
  const base = new Date();
  base.setMinutes(0, 0, 0);
  const addHours = (h: number) => new Date(base.getTime() + h * 3600000);
  return {
    registrationClose: localDatetimeValue(addHours(1)),
    roundRobin: localDatetimeValue(addHours(2)),
    eliminationR1: localDatetimeValue(addHours(4)),
    eliminationR2: '',
    final: localDatetimeValue(addHours(6)),
  };
}

function CreateGameDayForm({ onCreated }: { onCreated: () => void }) {
  const [open, setOpen] = useState(false);
  const [fields, setFields] = useState(defaultSchedule);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  function set(key: string, val: string) {
    setFields((f) => ({ ...f, [key]: val }));
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSaving(true);
    setErr(null);
    try {
      await createGameDay({
        registrationCloseAt: toISO(fields.registrationClose),
        roundRobinAt: toISO(fields.roundRobin),
        eliminationR1At: toISO(fields.eliminationR1),
        ...(fields.eliminationR2 ? { eliminationR2At: toISO(fields.eliminationR2) } : {}),
        finalAt: toISO(fields.final),
      });
      setOpen(false);
      setFields(defaultSchedule());
      onCreated();
    } catch (e2: unknown) {
      setErr(e2 instanceof Error ? e2.message : 'Failed to create');
    } finally {
      setSaving(false);
    }
  }

  if (!open) {
    return (
      <button onClick={() => setOpen(true)} style={primaryButtonStyle}>
        + Create Game Day
      </button>
    );
  }

  const inputStyle: React.CSSProperties = {
    background: '#0f0f1a', border: '1px solid #2d2d4e', borderRadius: 6,
    color: '#e2e8f0', padding: '6px 10px', fontSize: 13, width: '100%',
    colorScheme: 'dark',
  };

  const labelStyle: React.CSSProperties = {
    display: 'block', color: '#64748b', fontSize: 11,
    textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 4,
  };

  return (
    <div style={{ ...cardStyle, marginBottom: 24 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
        <h3 style={{ margin: 0, color: '#e2e8f0', fontSize: 16, fontWeight: 700 }}>Create Game Day</h3>
        <button onClick={() => setOpen(false)} style={ghostButtonStyle}>Cancel</button>
      </div>
      <form onSubmit={handleSubmit}>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 16 }}>
          {([
            ['registrationClose', 'Registration closes'],
            ['roundRobin', 'Round Robin starts'],
            ['eliminationR1', 'Elimination R1 starts'],
            ['eliminationR2', 'Elimination R2 starts (optional)'],
            ['final', 'Final starts'],
          ] as [string, string][]).map(([key, label]) => (
            <div key={key}>
              <label style={labelStyle}>{label}</label>
              <input
                type="datetime-local"
                value={fields[key as keyof typeof fields]}
                onChange={(e) => set(key, e.target.value)}
                required={key !== 'eliminationR2'}
                style={inputStyle}
              />
            </div>
          ))}
        </div>
        {err && <div style={{ color: '#f87171', fontSize: 13, marginBottom: 12 }}>{err}</div>}
        <button type="submit" disabled={saving} style={primaryButtonStyle}>
          {saving ? 'Creating…' : 'Create'}
        </button>
      </form>
    </div>
  );
}

export default function GameDayList() {
  const { user } = useAuthStore();
  const [gameDays, setGameDays] = useState<GameDay[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  function load() {
    listGameDays()
      .then((data) => setGameDays(data ?? []))
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }

  useEffect(() => { load(); }, []);

  return (
    <Layout>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 28 }}>
        <h2 style={{ margin: 0, fontSize: 22, color: '#e2e8f0' }}>Game Days</h2>
      </div>

      {user?.isAdmin && <CreateGameDayForm onCreated={load} />}

      {loading && <p style={{ color: '#64748b' }}>Loading…</p>}

      {error && (
        <div style={{ ...cardStyle, borderColor: '#7f1d1d', color: '#fca5a5', marginBottom: 16 }}>
          {error}
        </div>
      )}

      {!loading && !error && gameDays.length === 0 && (
        <div style={{ ...cardStyle, textAlign: 'center', padding: '48px 24px', color: '#64748b' }}>
          No game days scheduled yet.
        </div>
      )}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        {gameDays.map((gd) => (
          <GameDayRow key={gd.gameDayId} gd={gd} onDeleted={load} />
        ))}
      </div>
    </Layout>
  );
}
