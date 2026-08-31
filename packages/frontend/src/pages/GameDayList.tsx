import { useEffect, useRef, useState } from 'react';
import { Link, useBlocker, useLocation } from 'react-router-dom';
import Layout, { cardStyle, primaryButtonStyle, ghostButtonStyle } from '../components/Layout';
import { listGameDays, createGameDay, createGameDaySeries, cancelGameDaySeries, deleteGameDay, patchGameDay, listMaps, overrideGameDayPhase } from '../services/api';
import { useAuthStore } from '../store/authStore';
import { gameDayBaseName, localGameDayName } from '../utils/gameDayName';
import type { GameDay, GameDayPhaseStatus, GameDaySeriesFrequency, GameMap } from '../types';

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
      const updated = await patchGameDay(gd.gameDayId, {
        ...(fields.name.trim() ? { name: fields.name.trim() } : {}),
        registrationCloseAt: new Date(fields.registrationClose).toISOString(),
        roundRobinAt: new Date(fields.roundRobin).toISOString(),
        finalAt: new Date(fields.final).toISOString(),
        autofill,
        randomMaps,
        forcedMapIds,
      });
      // Item 254: the schedule saved, but one or more phases' real trigger
      // may not have — surface this instead of a plain silent success.
      if (updated.rescheduleFailures?.length) {
        setErr(
          `Saved, but couldn't reschedule: ${updated.rescheduleFailures.join(', ')}. ` +
          `Those phase(s) may still run at their old time — try editing again.`,
        );
        return false;
      }
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

function GameDayRow({ gd, onDeleted, onRefresh, autoOpen }: { gd: GameDay; onDeleted: (cleanupFailures?: string[]) => void; onRefresh: () => void; autoOpen?: boolean }) {
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [overriding, setOverriding] = useState(false);
  const [confirmOverride, setConfirmOverride] = useState(false);
  const [editing, setEditing] = useState(autoOpen ?? false);
  const [cancellingSeries, setCancellingSeries] = useState(false);
  const [confirmCancelSeries, setConfirmCancelSeries] = useState(false);
  const [seriesCancelled, setSeriesCancelled] = useState(false);
  const { user } = useAuthStore();
  const status = phaseOverallStatus(gd);

  const isUpcoming = status === 'upcoming';
  const stuck = isStuck(gd);

  async function handleCancelSeries() {
    if (!gd.seriesId) return;
    if (!confirmCancelSeries) { setConfirmCancelSeries(true); return; }
    setCancellingSeries(true);
    try {
      await cancelGameDaySeries(gd.seriesId);
      setSeriesCancelled(true);
    } catch {
      // ignore — button resets on next render
    } finally {
      setCancellingSeries(false);
      setConfirmCancelSeries(false);
    }
  }

  async function handleDelete() {
    if (!confirmDelete) { setConfirmDelete(true); return; }
    setDeleting(true);
    try {
      const result = await deleteGameDay(gd.gameDayId, !isUpcoming);
      // Item 255: the game day record itself is gone either way, but one or
      // more of its EventBridge schedules or tank registrations may not have
      // cleaned up. This row is about to unmount once the list reloads, so
      // the warning has to travel up to the parent rather than live in local
      // state here.
      onDeleted(result?.cleanupFailures?.length ? result.cleanupFailures : undefined);
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
              {gd.name
                ? localGameDayName(gd.name, gd.schedule.roundRobin, gd.schedule.final)
                : new Date(gd.schedule.roundRobin).toLocaleDateString(undefined, {
                    weekday: 'short', year: 'numeric', month: 'short', day: 'numeric',
                  })}
            </span>
            {gd.seriesId && (
              <span
                title="Part of a recurring series"
                style={{ fontSize: 11, color: '#7c6af7', background: 'rgba(124,106,247,0.1)', border: '1px solid #7c6af7', borderRadius: 0, padding: '2px 8px', fontWeight: 600 }}
              >
                ↻ Recurring
              </span>
            )}
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
              {gd.seriesId && !seriesCancelled && (
                <button
                  onClick={handleCancelSeries}
                  disabled={cancellingSeries}
                  title="Stop future occurrences of this series — this occurrence is untouched"
                  style={{ ...ghostButtonStyle, borderColor: '#7c6af7', color: '#7c6af7', cursor: 'pointer' }}
                >
                  {cancellingSeries ? 'Cancelling…' : confirmCancelSeries ? 'Confirm?' : 'Cancel series'}
                </button>
              )}
              {gd.seriesId && seriesCancelled && (
                <span style={{ fontSize: 12, color: '#5b87a3', alignSelf: 'center' }}>Series cancelled</span>
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

  // Repeats (item 238)
  const [recurring, setRecurring] = useState(false);
  const [frequency, setFrequency] = useState<GameDaySeriesFrequency>('weekly');
  const [byMonthDay, setByMonthDay] = useState(1);
  const [intervalDays, setIntervalDays] = useState(14);
  const [endless, setEndless] = useState(true);
  const [maxOccurrences, setMaxOccurrences] = useState(4);

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
    JSON.stringify(forcedMapIds) !== JSON.stringify(initialRef.current.forcedMapIds) ||
    recurring
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
    if (recurring && frequency === 'every_n_days' && intervalDays < 1) {
      setErr('Repeat interval must be at least 1 day');
      return false;
    }
    if (recurring && !endless && maxOccurrences < 1) {
      setErr('Number of occurrences must be at least 1');
      return false;
    }
    setSaving(true);
    setErr(null);
    try {
      const shared = {
        ...(name.trim() ? { name: name.trim() } : {}),
        registrationCloseAt: toISO(fields.registrationClose),
        roundRobinAt: toISO(fields.roundRobin),
        finalAt: toISO(fields.final),
        autofill,
        randomMaps,
        forcedMapIds,
      };
      if (recurring) {
        await createGameDaySeries({
          ...shared,
          frequency,
          ...(frequency === 'monthly' ? { byMonthDay } : {}),
          ...(frequency === 'every_n_days' ? { intervalDays } : {}),
          ...(endless ? {} : { maxOccurrences }),
        });
      } else {
        await createGameDay(shared);
      }
      const newDefaults = defaultSchedule();
      setOpen(false);
      setName('');
      setFields(newDefaults);
      setAutofill(false);
      setRandomMaps(false);
      setForcedMapIds([]);
      setRecurring(false);
      setFrequency('weekly');
      setEndless(true);
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
        <div style={{ border: '1px solid #23577a', padding: 14, marginBottom: 16 }}>
          <label style={{ ...checkLabel, marginBottom: recurring ? 12 : 0 }}>
            <input type="checkbox" checked={recurring} onChange={(e) => setRecurring(e.target.checked)} style={{ accentColor: '#7c6af7' }} />
            Repeats — the dates above become the first occurrence
          </label>
          {recurring && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <div style={{ display: 'grid', gridTemplateColumns: frequency === 'weekly' ? '1fr' : '1fr 1fr', gap: 16 }}>
                <div>
                  <label style={labelStyle}>Frequency</label>
                  <select
                    value={frequency}
                    onChange={(e) => setFrequency(e.target.value as GameDaySeriesFrequency)}
                    style={{ ...inputStyle, cursor: 'pointer' }}
                  >
                    <option value="weekly">Weekly (same weekday as above)</option>
                    <option value="monthly">Monthly (on a fixed day)</option>
                    <option value="every_n_days">Every N days</option>
                  </select>
                </div>
                {frequency === 'monthly' && (
                  <div>
                    <label style={labelStyle}>Day of month</label>
                    <input
                      type="number" min={1} max={31}
                      value={byMonthDay}
                      onChange={(e) => setByMonthDay(Number(e.target.value))}
                      style={inputStyle}
                    />
                  </div>
                )}
                {frequency === 'every_n_days' && (
                  <div>
                    <label style={labelStyle}>Every N days</label>
                    <input
                      type="number" min={1}
                      value={intervalDays}
                      onChange={(e) => setIntervalDays(Number(e.target.value))}
                      style={inputStyle}
                    />
                  </div>
                )}
              </div>
              <div>
                <label style={labelStyle}>Ends</label>
                <div style={{ display: 'flex', gap: 16, alignItems: 'center' }}>
                  <label style={checkLabel}>
                    <input type="radio" name="ends" checked={endless} onChange={() => setEndless(true)} style={{ accentColor: '#7c6af7' }} />
                    Never
                  </label>
                  <label style={checkLabel}>
                    <input type="radio" name="ends" checked={!endless} onChange={() => setEndless(false)} style={{ accentColor: '#7c6af7' }} />
                    After
                  </label>
                  {!endless && (
                    <input
                      type="number" min={1}
                      value={maxOccurrences}
                      onChange={(e) => setMaxOccurrences(Number(e.target.value))}
                      style={{ ...inputStyle, width: 70 }}
                    />
                  )}
                  {!endless && <span style={{ color: '#7fa2ba', fontSize: 13 }}>occurrences</span>}
                </div>
              </div>
              <p style={{ color: '#4a7291', fontSize: 12, margin: 0 }}>
                Only the next occurrence is ever pre-created; each following one is created automatically as its
                turn approaches. Cancelling the series later stops future occurrences without touching ones
                already created.
              </p>
            </div>
          )}
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
  const [deleteWarning, setDeleteWarning] = useState<string | null>(null);

  function load() {
    listGameDays()
      .then((data) => setGameDays(data ?? []))
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }

  // Item 255: a game day delete can succeed while leaving stale EventBridge
  // schedules or tank registrations behind — surface that instead of a
  // plain silent success, since the deleted row's own state doesn't survive
  // the list reload that follows.
  function handleGameDayDeleted(cleanupFailures?: string[]) {
    setDeleteWarning(
      cleanupFailures?.length
        ? `Deleted, but cleanup didn't fully finish: ${cleanupFailures.join(', ')}. ` +
          `Stale entries may remain — safe to ignore for a test game day.`
        : null,
    );
    load();
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

      {deleteWarning && (
        <div style={{ ...cardStyle, borderColor: '#5a3a18', color: '#ffcf9b', marginBottom: 16, display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 12 }}>
          <span>{deleteWarning}</span>
          <button
            onClick={() => setDeleteWarning(null)}
            style={{ ...ghostButtonStyle, borderColor: '#5a3a18', color: '#ffcf9b', cursor: 'pointer', flexShrink: 0 }}
          >
            Dismiss
          </button>
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
            <GameDayRow key={gd.gameDayId} gd={gd} onDeleted={handleGameDayDeleted} onRefresh={load} autoOpen={editId === gd.gameDayId} />
          ))}
      </div>
    </Layout>
  );
}
