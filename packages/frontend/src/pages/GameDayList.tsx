import { useEffect, useRef, useState } from 'react';
import { Link, useBlocker, useLocation } from 'react-router-dom';
import Layout, { cardStyle, primaryButtonStyle, ghostButtonStyle } from '../components/Layout';
import { listGameDays, createGameDay, deleteGameDay, patchGameDay, listMaps, overrideGameDayPhase } from '../services/api';
import { useAuthStore } from '../store/authStore';
import type { GameDay, GameDayPhaseStatus, GameMap } from '../types';

function phaseOverallStatus(gd: GameDay): 'upcoming' | 'active' | 'complete' | 'past' {
  const { phases, schedule } = gd;
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
  if (new Date(schedule.final).getTime() < Date.now()) return 'past';
  return 'upcoming';
}

function StatusBadge({ status }: { status: 'upcoming' | 'active' | 'complete' | 'past' }) {
  const map: Record<string, [string, string]> = {
    upcoming: ['#e8b339', 'rgba(251,191,36,0.1)'],
    active: ['#59e6c0', 'rgba(74,222,128,0.1)'],
    complete: ['#4a7291', 'rgba(71,85,105,0.1)'],
    past: ['#4a7291', 'rgba(71,85,105,0.1)'],
  };
  const [fg, bg] = map[status];
  return (
    <span style={{
      color: fg, background: bg, border: `1px solid ${fg}`,
      borderRadius: 0, fontSize: 11, padding: '2px 8px', fontWeight: 600, textTransform: 'uppercase',
    }}>
      {status}
    </span>
  );
}

function PhaseDot({ phase }: { phase: GameDayPhaseStatus }) {
  const color = phase.status === 'complete' ? '#4a7291' : phase.status === 'running' ? '#59e6c0' : '#23577a';
  return (
    <span style={{ display: 'inline-block', width: 8, height: 8, borderRadius: '50%', background: color, marginRight: 4 }} />
  );
}

function isoToLocalDatetime(iso: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** Strip the " · <date suffix>" appended by the backend. */
function gameDayBaseName(displayName: string): string {
  const idx = displayName.lastIndexOf(' · ');
  return idx >= 0 ? displayName.slice(0, idx) : displayName;
}

function MapSelector({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
  const [maps, setMaps] = useState<GameMap[]>([]);
  useEffect(() => { listMaps().then(setMaps).catch(() => {}); }, []);
  if (maps.length === 0) return null;
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4, maxHeight: 140, overflowY: 'auto' }}>
      {maps.map((m) => (
        <label key={m.mapId} style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', color: '#e7f1f7', fontSize: 13 }}>
          <input
            type="checkbox"
            checked={value.includes(m.mapId)}
            onChange={(e) => {
              if (e.target.checked) onChange([...value, m.mapId]);
              else onChange(value.filter((id) => id !== m.mapId));
            }}
            style={{ accentColor: '#ffab6b' }}
          />
          {m.name}
        </label>
      ))}
    </div>
  );
}

const overlay: React.CSSProperties = {
  position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)',
  display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 100,
};

function UnsavedChangesDialog({ onSaveAndLeave, onDiscard, onStay }: {
  onSaveAndLeave: () => void;
  onDiscard: () => void;
  onStay: () => void;
}) {
  return (
    <div style={overlay}>
      <div style={{ ...cardStyle, width: 380 }}>
        <h3 style={{ margin: '0 0 8px', color: '#e7f1f7' }}>Unsaved changes</h3>
        <p style={{ margin: '0 0 20px', fontSize: 14, color: '#7fa2ba' }}>
          You have unsaved changes. Do you want to save before leaving?
        </p>
        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <button onClick={onStay} style={ghostButtonStyle}>Keep editing</button>
          <button onClick={onDiscard} style={{ ...ghostButtonStyle, color: '#ff8a75', borderColor: '#3a1a18' }}>
            Discard
          </button>
          <button onClick={onSaveAndLeave} style={primaryButtonStyle}>Save & Leave</button>
        </div>
      </div>
    </div>
  );
}

function EditGameDayForm({ gd, onSaved, onCancel }: { gd: GameDay; onSaved: () => void; onCancel: () => void }) {
  const [fields, setFields] = useState({
    name: gameDayBaseName(gd.name ?? ''),
    registrationClose: isoToLocalDatetime(gd.schedule.registrationClose),
    roundRobin: isoToLocalDatetime(gd.schedule.roundRobin),
    final: isoToLocalDatetime(gd.schedule.final),
  });
  const [autofill, setAutofill] = useState(gd.autofill ?? false);
  const [randomMaps, setRandomMaps] = useState(gd.randomMaps ?? false);
  const [forcedMapIds, setForcedMapIds] = useState<string[]>(gd.forcedMapIds ?? []);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const initialRef = useRef({
    name: gameDayBaseName(gd.name ?? ''),
    registrationClose: isoToLocalDatetime(gd.schedule.registrationClose),
    roundRobin: isoToLocalDatetime(gd.schedule.roundRobin),
    final: isoToLocalDatetime(gd.schedule.final),
    autofill: gd.autofill ?? false,
    randomMaps: gd.randomMaps ?? false,
    forcedMapIds: [...(gd.forcedMapIds ?? [])],
  });

  const dirty =
    fields.name !== initialRef.current.name ||
    fields.registrationClose !== initialRef.current.registrationClose ||
    fields.roundRobin !== initialRef.current.roundRobin ||
    fields.final !== initialRef.current.final ||
    autofill !== initialRef.current.autofill ||
    randomMaps !== initialRef.current.randomMaps ||
    JSON.stringify(forcedMapIds) !== JSON.stringify(initialRef.current.forcedMapIds);

  const blocker = useBlocker(dirty);

  function set(key: string, val: string) {
    setFields((f) => ({ ...f, [key]: val }));
  }

  async function doSave(): Promise<boolean> {
    if (new Date(fields.registrationClose) >= new Date(fields.roundRobin)) {
      setErr('Registration must close before round robin starts');
      return false;
    }
    if (new Date(fields.roundRobin) >= new Date(fields.final)) {
      setErr('Round robin must start before the final');
      return false;
    }
    setSaving(true);
    setErr(null);
    try {
      await patchGameDay(gd.gameDayId, {
        ...(fields.name.trim() ? { name: fields.name.trim() } : {}),
        registrationCloseAt: new Date(fields.registrationClose).toISOString(),
        roundRobinAt: new Date(fields.roundRobin).toISOString(),
        finalAt: new Date(fields.final).toISOString(),
        autofill,
        randomMaps,
        forcedMapIds,
      });
      return true;
    } catch (e2: unknown) {
      setErr(e2 instanceof Error ? e2.message : 'Failed to save');
      return false;
    } finally {
      setSaving(false);
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    const ok = await doSave();
    if (ok) onSaved();
  }

  const inputStyle: React.CSSProperties = {
    background: '#0a3550', border: '1px solid #23577a', borderRadius: 0,
    color: '#e7f1f7', padding: '6px 10px', fontSize: 13, width: '100%',
    colorScheme: 'dark',
  };
  const labelStyle: React.CSSProperties = {
    display: 'block', color: '#5b87a3', fontSize: 11,
    textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 4,
  };
  const checkLabel: React.CSSProperties = {
    display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', color: '#7fa2ba', fontSize: 13,
  };

  return (
    <div style={{ ...cardStyle, marginTop: 12, borderColor: '#ffab6b' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <h4 style={{ margin: 0, color: '#ffab6b', fontSize: 14, fontWeight: 700 }}>Edit Schedule</h4>
        <button onClick={onCancel} style={ghostButtonStyle}>Cancel</button>
      </div>
      <form onSubmit={handleSubmit}>
        <div style={{ marginBottom: 14 }}>
          <label style={labelStyle}>Name</label>
          <input
            type="text"
            value={fields.name}
            onChange={(e) => set('name', e.target.value)}
            placeholder="e.g. Season 1"
            style={inputStyle}
          />
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 14 }}>
          {([
            ['registrationClose', 'Registration closes'],
            ['roundRobin', 'Round Robin starts'],
            ['final', 'Final starts'],
          ] as [string, string][]).map(([key, label]) => (
            <div key={key}>
              <label style={labelStyle}>{label}</label>
              <input
                type="datetime-local"
                value={fields[key as keyof typeof fields]}
                onChange={(e) => set(key, e.target.value)}
                required
                style={inputStyle}
              />
            </div>
          ))}
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginBottom: 14 }}>
          <label style={checkLabel}>
            <input type="checkbox" checked={autofill} onChange={(e) => setAutofill(e.target.checked)} style={{ accentColor: '#ffab6b' }} />
            Auto-fill bracket with AI bots to nearest power-of-two (≥8)
          </label>
          <label style={checkLabel}>
            <input type="checkbox" checked={randomMaps} onChange={(e) => setRandomMaps(e.target.checked)} style={{ accentColor: '#ffab6b' }} />
            Random maze per match (ignore map selection below)
          </label>
        </div>
        {!randomMaps && (
          <div style={{ marginBottom: 14 }}>
            <label style={labelStyle}>Forced maps (leave empty for random mazes)</label>
            <MapSelector value={forcedMapIds} onChange={setForcedMapIds} />
          </div>
        )}
        {err && <div style={{ color: '#ff8a75', fontSize: 13, marginBottom: 10 }}>{err}</div>}
        <button type="submit" disabled={saving} style={primaryButtonStyle}>
          {saving ? 'Saving…' : 'Save'}
        </button>
      </form>
      {blocker.state === 'blocked' && (
        <UnsavedChangesDialog
          onSaveAndLeave={async () => {
            const ok = await doSave();
            if (ok) blocker.proceed?.();
            else blocker.reset?.();
          }}
          onDiscard={() => blocker.proceed?.()}
          onStay={() => blocker.reset?.()}
        />
      )}
    </div>
  );
}

/** A game day is "stuck" when its scheduled time has passed but phases are still
 *  "upcoming" — the EventBridge scheduler rule never fired or the Lambda exited
 *  early without advancing the phase statuses. */
function isStuck(gd: GameDay): boolean {
  const past = new Date(gd.schedule.final).getTime() < Date.now();
  const neverStarted = gd.phases.roundRobin.status === 'upcoming' && gd.phases.final.status === 'upcoming';
  return past && neverStarted;
}

function GameDayRow({ gd, onDeleted, onRefresh, autoOpen }: { gd: GameDay; onDeleted: () => void; onRefresh: () => void; autoOpen?: boolean }) {
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [overriding, setOverriding] = useState(false);
  const [confirmOverride, setConfirmOverride] = useState(false);
  const [editing, setEditing] = useState(autoOpen ?? false);
  const { user } = useAuthStore();
  const status = phaseOverallStatus(gd);

  const isUpcoming = status === 'upcoming';
  const stuck = isStuck(gd);

  async function handleDelete() {
    if (!confirmDelete) { setConfirmDelete(true); return; }
    setDeleting(true);
    try {
      await deleteGameDay(gd.gameDayId, !isUpcoming);
      onDeleted();
    } catch {
      setDeleting(false);
      setConfirmDelete(false);
    }
  }

  async function handleCancelStuck() {
    if (!confirmOverride) { setConfirmOverride(true); return; }
    setOverriding(true);
    try {
      await overrideGameDayPhase(gd.gameDayId, {
        roundRobin: 'cancelled',
        final: 'cancelled',
      });
      onRefresh();
    } catch {
      // ignore — button resets on next render
    } finally {
      setOverriding(false);
      setConfirmOverride(false);
    }
  }

  return (
    <div style={cardStyle}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 16 }}>
        <div style={{ flex: 1 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 6 }}>
            <StatusBadge status={status} />
            {stuck && (
              <span style={{ fontSize: 11, color: '#ff7a29', background: 'rgba(249,115,22,0.1)', border: '1px solid #ff7a29', borderRadius: 0, padding: '2px 8px', fontWeight: 600 }}>
                STUCK
              </span>
            )}
            <span style={{ color: '#e7f1f7', fontSize: 15, fontWeight: 600 }}>
              {gd.name || new Date(gd.schedule.roundRobin).toLocaleDateString(undefined, {
                weekday: 'short', year: 'numeric', month: 'short', day: 'numeric',
              })}
            </span>
          </div>
          <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', fontSize: 12, color: '#5b87a3' }}>
            <span>
              <PhaseDot phase={gd.phases.roundRobin} />
              Round Robin — {new Date(gd.schedule.roundRobin).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
            </span>
            <span>
              <PhaseDot phase={gd.phases.final} />
              Final — {new Date(gd.schedule.final).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
            </span>
            {(gd.registeredTanks ?? []).length > 0 && (
              <span style={{ color: '#ffab6b' }}>{(gd.registeredTanks ?? []).length} registered</span>
            )}
          </div>
        </div>
        <div style={{ display: 'flex', gap: 8, flexShrink: 0, flexWrap: 'wrap', justifyContent: 'flex-end' }}>
          <Link
            to={`/gameday/${gd.gameDayId}`}
            style={{ ...ghostButtonStyle, textDecoration: 'none', display: 'inline-block' }}
          >
            View
          </Link>
          {user?.isAdmin && (
            <>
              {isUpcoming && new Date(gd.schedule.final) > new Date() && (
                <button
                  onClick={() => { setEditing((v) => !v); setConfirmDelete(false); setConfirmOverride(false); }}
                  style={{ ...ghostButtonStyle, borderColor: '#ffab6b', color: '#ffab6b', cursor: 'pointer' }}
                >
                  {editing ? 'Close' : 'Edit'}
                </button>
              )}
              {stuck && (
                <button
                  onClick={handleCancelStuck}
                  disabled={overriding}
                  title="Mark both phases as cancelled so this game day stops appearing as upcoming"
                  style={{ ...ghostButtonStyle, borderColor: '#ff7a29', color: '#ff7a29', cursor: 'pointer' }}
                >
                  {overriding ? 'Cancelling…' : confirmOverride ? 'Confirm cancel?' : 'Cancel stuck'}
                </button>
              )}
              <button
                onClick={handleDelete}
                disabled={deleting}
                style={{ ...ghostButtonStyle, borderColor: '#3a1a18', color: '#ff8a75', cursor: 'pointer' }}
              >
                {deleting ? 'Deleting…' : confirmDelete ? (!isUpcoming ? 'Force delete?' : 'Confirm?') : 'Delete'}
              </button>
            </>
          )}
        </div>
      </div>
      {editing && (
        <EditGameDayForm
          gd={gd}
          onSaved={() => { setEditing(false); onRefresh(); }}
          onCancel={() => setEditing(false)}
        />
      )}
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
    final: localDatetimeValue(addHours(6)),
  };
}

function CreateGameDayForm({ onCreated }: { onCreated: () => void }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [fields, setFields] = useState(defaultSchedule);
  const [autofill, setAutofill] = useState(false);
  const [randomMaps, setRandomMaps] = useState(false);
  const [forcedMapIds, setForcedMapIds] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // Baseline captured at mount; updated after a successful create so that a
  // re-open of the reset form starts clean again.
  const initialRef = useRef({ name: '', fields: defaultSchedule(), autofill: false, randomMaps: false, forcedMapIds: [] as string[] });

  const dirty = open && (
    name !== initialRef.current.name ||
    fields.registrationClose !== initialRef.current.fields.registrationClose ||
    fields.roundRobin !== initialRef.current.fields.roundRobin ||
    fields.final !== initialRef.current.fields.final ||
    autofill !== initialRef.current.autofill ||
    randomMaps !== initialRef.current.randomMaps ||
    JSON.stringify(forcedMapIds) !== JSON.stringify(initialRef.current.forcedMapIds)
  );

  const blocker = useBlocker(dirty);

  function set(key: string, val: string) {
    setFields((f) => ({ ...f, [key]: val }));
  }

  async function doCreate(): Promise<boolean> {
    if (new Date(fields.registrationClose) >= new Date(fields.roundRobin)) {
      setErr('Registration must close before round robin starts');
      return false;
    }
    if (new Date(fields.roundRobin) >= new Date(fields.final)) {
      setErr('Round robin must start before the final');
      return false;
    }
    setSaving(true);
    setErr(null);
    try {
      await createGameDay({
        ...(name.trim() ? { name: name.trim() } : {}),
        registrationCloseAt: toISO(fields.registrationClose),
        roundRobinAt: toISO(fields.roundRobin),
        finalAt: toISO(fields.final),
        autofill,
        randomMaps,
        forcedMapIds,
      });
      const newDefaults = defaultSchedule();
      setOpen(false);
      setName('');
      setFields(newDefaults);
      setAutofill(false);
      setRandomMaps(false);
      setForcedMapIds([]);
      initialRef.current = { name: '', fields: newDefaults, autofill: false, randomMaps: false, forcedMapIds: [] };
      onCreated();
      return true;
    } catch (e2: unknown) {
      setErr(e2 instanceof Error ? e2.message : 'Failed to create');
      return false;
    } finally {
      setSaving(false);
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    await doCreate();
  }

  if (!open) {
    return (
      <button onClick={() => setOpen(true)} style={primaryButtonStyle}>
        + Create Game Day
      </button>
    );
  }

  const inputStyle: React.CSSProperties = {
    background: '#0a3550', border: '1px solid #23577a', borderRadius: 0,
    color: '#e7f1f7', padding: '6px 10px', fontSize: 13, width: '100%',
    colorScheme: 'dark',
  };

  const labelStyle: React.CSSProperties = {
    display: 'block', color: '#5b87a3', fontSize: 11,
    textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 4,
  };

  const checkLabel: React.CSSProperties = {
    display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', color: '#7fa2ba', fontSize: 13,
  };

  return (
    <div style={{ ...cardStyle, marginBottom: 24 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
        <h3 style={{ margin: 0, color: '#e7f1f7', fontSize: 16, fontWeight: 700 }}>Create Game Day</h3>
        <button onClick={() => setOpen(false)} style={ghostButtonStyle}>Cancel</button>
      </div>
      <form onSubmit={handleSubmit}>
        <div style={{ marginBottom: 16 }}>
          <label style={labelStyle}>Name</label>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. Season 1"
            style={inputStyle}
          />
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 16 }}>
          {([
            ['registrationClose', 'Registration closes'],
            ['roundRobin', 'Round Robin starts'],
            ['final', 'Final starts'],
          ] as [string, string][]).map(([key, label]) => (
            <div key={key}>
              <label style={labelStyle}>{label}</label>
              <input
                type="datetime-local"
                value={fields[key as keyof typeof fields]}
                onChange={(e) => set(key, e.target.value)}
                required
                style={inputStyle}
              />
            </div>
          ))}
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginBottom: 16 }}>
          <label style={checkLabel}>
            <input type="checkbox" checked={autofill} onChange={(e) => setAutofill(e.target.checked)} style={{ accentColor: '#ffab6b' }} />
            Auto-fill bracket with AI bots to nearest power-of-two (≥8)
          </label>
          <label style={checkLabel}>
            <input type="checkbox" checked={randomMaps} onChange={(e) => setRandomMaps(e.target.checked)} style={{ accentColor: '#ffab6b' }} />
            Random maze per match
          </label>
        </div>
        {!randomMaps && (
          <div style={{ marginBottom: 16 }}>
            <label style={labelStyle}>Forced maps (leave empty for random mazes)</label>
            <MapSelector value={forcedMapIds} onChange={setForcedMapIds} />
          </div>
        )}
        {err && <div style={{ color: '#ff8a75', fontSize: 13, marginBottom: 12 }}>{err}</div>}
        <button type="submit" disabled={saving} style={primaryButtonStyle}>
          {saving ? 'Creating…' : 'Create'}
        </button>
      </form>
      {blocker.state === 'blocked' && (
        <UnsavedChangesDialog
          onSaveAndLeave={async () => {
            const ok = await doCreate();
            if (ok) blocker.proceed?.();
            else blocker.reset?.();
          }}
          onDiscard={() => blocker.proceed?.()}
          onStay={() => blocker.reset?.()}
        />
      )}
    </div>
  );
}

export default function GameDayList() {
  const { user } = useAuthStore();
  const { state } = useLocation();
  const editId: string | undefined = state?.editId;
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
        <h2 style={{ margin: 0, fontSize: 22, color: '#e7f1f7' }}>Game Days</h2>
      </div>

      {user?.isAdmin && <CreateGameDayForm onCreated={load} />}

      {loading && <p style={{ color: '#5b87a3' }}>Loading…</p>}

      {error && (
        <div style={{ ...cardStyle, borderColor: '#3a1a18', color: '#ffb8a3', marginBottom: 16 }}>
          {error}
        </div>
      )}

      {!loading && !error && gameDays.length === 0 && (
        <div style={{ ...cardStyle, textAlign: 'center', padding: '48px 24px', color: '#5b87a3' }}>
          No game days scheduled yet.
        </div>
      )}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        {[...gameDays]
          .sort((a, b) => {
            const order = { active: 0, upcoming: 1, past: 2, complete: 3 };
            return order[phaseOverallStatus(a)] - order[phaseOverallStatus(b)];
          })
          .map((gd) => (
            <GameDayRow key={gd.gameDayId} gd={gd} onDeleted={load} onRefresh={load} autoOpen={editId === gd.gameDayId} />
          ))}
      </div>
    </Layout>
  );
}
