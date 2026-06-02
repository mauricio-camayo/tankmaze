import { useEffect, useRef, useState } from 'react';
import { useNavigate, useParams, Link } from 'react-router-dom';
import Editor from '@monaco-editor/react';
import Layout from '../components/Layout';
import {
  getTank,
  submitVersion,
  getVersionStatus,
  promoteVersion,
  startMatch,
  listMaps,
  registerForGameDay,
  withdrawRegistration,
  type OpponentSpec,
} from '../services/api';
import type { Tank, TankVersion, TankConfig, GameMap } from '../types';
import { cardStyle, primaryButtonStyle, ghostButtonStyle } from '../components/Layout';

// ── helpers ─────────────────────────────────────────────────────────────────

const STAT_NAMES: (keyof Omit<TankConfig, 'name'>)[] = [
  'speed', 'sensorRange', 'damage', 'armor', 'fireRate',
];
const STAT_LABELS: Record<string, string> = {
  speed: 'Speed', sensorRange: 'Sensor Range', damage: 'Damage',
  armor: 'Armor', fireRate: 'Fire Rate',
};
const STAT_SUM_TARGET = 15;

const DEFAULT_CONFIG: TankConfig = {
  name: 'My Tank', speed: 3, sensorRange: 3, damage: 3, armor: 3, fireRate: 3,
};

function defaultSource(cfg: TankConfig): string {
  return `package tank

import . "github.com/tankmaze/sdk"

var Config = TankConfig{
\tName:        "${cfg.name}",
\tSpeed:       ${cfg.speed},
\tSensorRange: ${cfg.sensorRange},
\tDamage:      ${cfg.damage},
\tArmor:       ${cfg.armor},
\tFireRate:    ${cfg.fireRate},
}

func Tick(s Sensors) Action {
\tif s.ProximityAlert && s.FireCooldown == 0 {
\t\treturn Action{Type: Fire}
\t}
\tif s.WallDistances[s.Facing] > 1 && s.MoveCooldown == 0 {
\t\treturn Action{Type: Move, Direction: Forward}
\t}
\treturn Action{Type: Rotate, Direction: Right}
}
`;
}

function isMajor(v: string) { return /^v\d+$/.test(v); }
function isMinor(v: string) { return /^v\d+\.\d+$/.test(v); }

function nextMajorLabel(v: string): string {
  const m = v.match(/^v(\d+)/);
  return m ? `v${parseInt(m[1]) + 1}` : 'next';
}

function sortedByAge(versions: TankVersion[]): TankVersion[] {
  return [...versions].sort((a, b) => b.createdAt - a.createdAt);
}

function latestReady(versions: TankVersion[]): TankVersion | undefined {
  return sortedByAge(versions).find((v) => v.compileStatus === 'ready');
}

// ── sub-components ───────────────────────────────────────────────────────────

function StatInput({
  label, value, onChange,
}: { label: string; value: number; onChange: (n: number) => void }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <span style={{ fontSize: 11, color: '#64748b', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
        {label}
      </span>
      <div style={{ display: 'flex', alignItems: 'center', gap: 0 }}>
        <button
          onClick={() => onChange(Math.max(1, value - 1))}
          style={{ ...stepBtn, borderRadius: '4px 0 0 4px' }}
        >−</button>
        <span style={{
          width: 32, textAlign: 'center', background: '#0f0f1a',
          border: '1px solid #2d2d4e', borderLeft: 'none', borderRight: 'none',
          padding: '3px 0', fontSize: 14, color: '#e2e8f0',
        }}>{value}</span>
        <button
          onClick={() => onChange(Math.min(5, value + 1))}
          style={{ ...stepBtn, borderRadius: '0 4px 4px 0' }}
        >+</button>
      </div>
    </div>
  );
}

const stepBtn: React.CSSProperties = {
  background: '#2d2d4e', border: '1px solid #3d3d6e', color: '#e2e8f0',
  width: 28, height: 28, cursor: 'pointer', fontSize: 16, lineHeight: 1,
  display: 'flex', alignItems: 'center', justifyContent: 'center',
};

function ConfigPanel({
  config, onChange,
}: { config: TankConfig; onChange: (c: TankConfig) => void }) {
  const sum = STAT_NAMES.reduce((acc, k) => acc + config[k], 0);
  const valid = sum === STAT_SUM_TARGET;

  function setStat(key: keyof Omit<TankConfig, 'name'>, val: number) {
    onChange({ ...config, [key]: val });
  }

  return (
    <div style={{ ...cardStyle, marginBottom: 12 }}>
      <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', alignItems: 'flex-end' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 160 }}>
          <span style={{ fontSize: 11, color: '#64748b', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
            Tank Name
          </span>
          <input
            value={config.name}
            onChange={(e) => onChange({ ...config, name: e.target.value })}
            style={{
              background: '#0f0f1a', border: '1px solid #2d2d4e', color: '#e2e8f0',
              padding: '4px 8px', borderRadius: 4, fontSize: 14, height: 28,
            }}
          />
        </div>
        {STAT_NAMES.map((k) => (
          <StatInput key={k} label={STAT_LABELS[k]} value={config[k]} onChange={(v) => setStat(k, v)} />
        ))}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          <span style={{ fontSize: 11, color: '#64748b', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
            Total
          </span>
          <span style={{
            fontSize: 15, fontWeight: 700, color: valid ? '#4ade80' : '#f87171',
            paddingTop: 4,
          }}>
            {sum}/{STAT_SUM_TARGET} {valid ? '✓' : '✗'}
          </span>
        </div>
      </div>
    </div>
  );
}

function StatusBar({
  status, error, version,
}: { status: string; error: string | null; version?: string }) {
  if (status === 'idle') return null;

  const { dot, label } = {
    submitting: { dot: '#94a3b8', label: 'Uploading…' },
    polling:    { dot: '#fbbf24', label: 'Compiling… (15–30 s)' },
    ready:      { dot: '#4ade80', label: `Compiled OK${version ? ` · ${version}` : ''}` },
    failed:     { dot: '#f87171', label: 'Compile failed' },
  }[status] ?? { dot: '#94a3b8', label: status };

  return (
    <div style={{ marginTop: 8 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: error ? 8 : 0 }}>
        <span style={{ width: 8, height: 8, borderRadius: '50%', background: dot, flexShrink: 0 }} />
        <span style={{ fontSize: 13, color: '#94a3b8' }}>{label}</span>
      </div>
      {error && (
        <pre style={{
          background: '#1c0a0a', border: '1px solid #7f1d1d', color: '#fca5a5',
          borderRadius: 6, padding: '12px 16px', fontSize: 12, overflowX: 'auto',
          margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all',
        }}>{error}</pre>
      )}
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
  const [mapId, setMapId] = useState<string | null>(null);

  return (
    <div style={overlay}>
      <div style={{ ...cardStyle, width: 420, maxHeight: '80vh', overflowY: 'auto' }}>
        <h3 style={{ margin: '0 0 16px', color: '#e2e8f0' }}>Test vs AI</h3>

        <div style={{ marginBottom: 16 }}>
          <p style={{ margin: '0 0 8px', fontSize: 13, color: '#94a3b8' }}>Opponent</p>
          {(['scout', 'bruiser', 'ranger'] as TestOpponent[]).map((op) => (
            <label key={op} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, cursor: 'pointer' }}>
              <input
                type="radio"
                name="opponent"
                value={op}
                checked={opponent === op}
                onChange={() => setOpponent(op)}
              />
              <span style={{ color: '#e2e8f0', textTransform: 'capitalize', fontSize: 14 }}>{op}</span>
            </label>
          ))}
        </div>

        <div style={{ marginBottom: 20 }}>
          <p style={{ margin: '0 0 8px', fontSize: 13, color: '#94a3b8' }}>Map</p>
          {loadingMaps ? (
            <span style={{ color: '#64748b', fontSize: 13 }}>Loading maps…</span>
          ) : (
            <>
              <label style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, cursor: 'pointer' }}>
                <input type="radio" name="map" value="" checked={mapId === null} onChange={() => setMapId(null)} />
                <span style={{ color: '#e2e8f0', fontSize: 14 }}>Random (default)</span>
              </label>
              {maps.map((m) => (
                <label key={m.mapId} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, cursor: 'pointer' }}>
                  <input type="radio" name="map" value={m.mapId} checked={mapId === m.mapId} onChange={() => setMapId(m.mapId)} />
                  <span style={{ color: '#e2e8f0', fontSize: 14 }}>{m.name}</span>
                  <span style={{ color: '#64748b', fontSize: 12 }}>{m.description}</span>
                </label>
              ))}
            </>
          )}
        </div>

        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <button onClick={onClose} style={ghostButtonStyle}>Cancel</button>
          <button onClick={() => onTest(opponent, mapId)} style={primaryButtonStyle}>
            Launch Match
          </button>
        </div>
      </div>
    </div>
  );
}

const overlay: React.CSSProperties = {
  position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)',
  display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 100,
};

// ── main component ───────────────────────────────────────────────────────────

type SaveStatus = 'idle' | 'submitting' | 'polling' | 'ready' | 'failed';

export default function TankEditor() {
  const { tankId } = useParams<{ tankId: string }>();
  const navigate = useNavigate();

  const [tank, setTank] = useState<Tank | null>(null);
  const [versions, setVersions] = useState<TankVersion[]>([]);
  const [source, setSource] = useState('');
  const [config, setConfig] = useState<TankConfig>(DEFAULT_CONFIG);
  const [saveStatus, setSaveStatus] = useState<SaveStatus>('idle');
  const [saveError, setSaveError] = useState<string | null>(null);
  const [pendingVersion, setPendingVersion] = useState<string | null>(null);
  const [showTestDialog, setShowTestDialog] = useState(false);
  const [maps, setMaps] = useState<GameMap[]>([]);
  const [loadingMaps, setLoadingMaps] = useState(false);
  const [pageLoading, setPageLoading] = useState(true);
  const [pageError, setPageError] = useState<string | null>(null);
  const [promoting, setPromoting] = useState(false);
  const [registering, setRegistering] = useState(false);

  const pollCancelRef = useRef(false);

  // Load tank on mount
  useEffect(() => {
    if (!tankId) return;
    getTank(tankId)
      .then(({ versions: v, ...t }) => {
        setTank(t);
        setVersions(v ?? []);
        // Restore last edited source from localStorage
        const saved = localStorage.getItem(`tankmaze-src-${tankId}`);
        const savedCfg = localStorage.getItem(`tankmaze-cfg-${tankId}`);
        setSource(saved ?? defaultSource(DEFAULT_CONFIG));
        setConfig(savedCfg ? (JSON.parse(savedCfg) as TankConfig) : DEFAULT_CONFIG);
        // Reflect latest version's compile status
        const latest = sortedByAge(v ?? [])[0];
        if (latest?.compileStatus === 'ready') setSaveStatus('ready');
        if (latest?.compileStatus === 'failed') {
          setSaveStatus('failed');
          setSaveError(latest.compileError ?? 'Unknown error');
        }
        // Resume polling if compile was in-flight when user left
        if (latest?.compileStatus === 'pending' || latest?.compileStatus === 'compiling') {
          setPendingVersion(latest.version);
          setSaveStatus('polling');
        }
      })
      .catch((e: Error) => setPageError(e.message))
      .finally(() => setPageLoading(false));
  }, [tankId]);

  // Polling loop
  useEffect(() => {
    if (saveStatus !== 'polling' || !pendingVersion || !tankId) return;
    pollCancelRef.current = false;

    async function poll() {
      while (!pollCancelRef.current) {
        await new Promise((r) => setTimeout(r, 2500));
        if (pollCancelRef.current) break;
        try {
          const s = await getVersionStatus(tankId!, pendingVersion!);
          if (s.compileStatus === 'ready') {
            setSaveStatus('ready');
            setSaveError(null);
            // Refresh versions list
            getTank(tankId!).then(({ versions: v }) => setVersions(v ?? []));
            break;
          }
          if (s.compileStatus === 'failed') {
            setSaveStatus('failed');
            setSaveError(s.compileError ?? 'Compile failed');
            getTank(tankId!).then(({ versions: v }) => setVersions(v ?? []));
            break;
          }
        } catch {
          // transient — keep polling
        }
      }
    }

    poll();
    return () => { pollCancelRef.current = true; };
  }, [saveStatus, pendingVersion, tankId]);

  // Persist source to localStorage on change
  useEffect(() => {
    if (tankId && source) localStorage.setItem(`tankmaze-src-${tankId}`, source);
  }, [tankId, source]);

  useEffect(() => {
    if (tankId) localStorage.setItem(`tankmaze-cfg-${tankId}`, JSON.stringify(config));
  }, [tankId, config]);

  async function handleSave() {
    if (!tankId) return;
    const statSum = STAT_NAMES.reduce((acc, k) => acc + config[k], 0);
    if (statSum !== STAT_SUM_TARGET) {
      setSaveError(`Stat points must sum to ${STAT_SUM_TARGET} (currently ${statSum})`);
      setSaveStatus('failed');
      return;
    }
    setSaveStatus('submitting');
    setSaveError(null);
    try {
      const v = await submitVersion(tankId, source, config);
      setPendingVersion(v.version);
      setSaveStatus('polling');
    } catch (e) {
      setSaveStatus('failed');
      setSaveError(e instanceof Error ? e.message : 'Submit failed');
    }
  }

  async function handlePromote() {
    if (!tankId) return;
    const latest = sortedByAge(versions)[0];
    if (!latest || !isMinor(latest.version)) return;
    setPromoting(true);
    try {
      await promoteVersion(tankId, latest.version);
      const { versions: v, ...t } = await getTank(tankId);
      setTank(t);
      setVersions(v ?? []);
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : 'Promote failed');
    } finally {
      setPromoting(false);
    }
  }

  async function handleRegister() {
    if (!tankId) return;
    const latest = sortedByAge(versions)[0];
    if (!latest || !isMajor(latest.version)) return;
    setRegistering(true);
    try {
      if (latest.registeredForGameDay) {
        await withdrawRegistration(tankId, latest.version);
      } else {
        await registerForGameDay(tankId, latest.version);
      }
      const { versions: v } = await getTank(tankId);
      setVersions(v ?? []);
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : 'Registration failed');
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
    if (!tankId) return;
    // Use the latest ready version, or fall back to pendingVersion if the
    // versions list hasn't refreshed yet after a just-finished compile.
    const version = latestReady(versions)?.version ?? pendingVersion;
    if (!version) return;
    try {
      const spec: OpponentSpec = { type: 'ai', name: opponent };
      const match = await startMatch(tankId, version, spec, mapId ?? undefined);
      navigate(`/watch?matchId=${match.matchId}`);
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : 'Failed to start match');
      setShowTestDialog(false);
    }
  }

  // Derived state
  const sortedVersions = sortedByAge(versions);
  const latestVersion = sortedVersions[0];
  const ready = latestReady(versions);
  const sessionReady = saveStatus === 'ready';

  const canTest = (sessionReady && pendingVersion != null) || ready != null;
  const canPromote =
    latestVersion &&
    isMinor(latestVersion.version) &&
    (sessionReady || latestVersion.compileStatus === 'ready');
  const canRegister =
    latestVersion &&
    isMajor(latestVersion.version) &&
    latestVersion.compileStatus === 'ready';
  const isRegistered = canRegister && !!latestVersion?.registeredForGameDay;
  const isSaving = saveStatus === 'submitting' || saveStatus === 'polling';

  if (pageLoading) return <Layout><p style={{ color: '#64748b' }}>Loading…</p></Layout>;
  if (pageError) return <Layout><p style={{ color: '#f87171' }}>{pageError}</p></Layout>;

  return (
    <Layout>
      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16, flexWrap: 'wrap', gap: 12 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <Link to="/dashboard" style={{ color: '#64748b', textDecoration: 'none', fontSize: 13 }}>
            ← My Tanks
          </Link>
          <h2 style={{ margin: 0, fontSize: 18, color: '#e2e8f0' }}>
            {tank?.name || 'Unnamed Tank'}
          </h2>
          {latestVersion && (
            <span style={{
              fontSize: 12, padding: '2px 8px', borderRadius: 4, fontWeight: 500,
              background: isMajor(latestVersion.version) ? '#1e3a2f' : '#1e2a3a',
              color: isMajor(latestVersion.version) ? '#4ade80' : '#60a5fa',
            }}>
              {latestVersion.version} {isMajor(latestVersion.version) ? 'major' : 'minor'}
            </span>
          )}
        </div>

        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          {canRegister && (
            <button onClick={handleRegister} disabled={registering} style={ghostButtonStyle}>
              {registering ? '…' : isRegistered ? 'Withdraw Registration' : 'Register for Game Day'}
            </button>
          )}
          {canTest && (
            <button onClick={openTestDialog} style={ghostButtonStyle}>
              Test vs AI
            </button>
          )}
          {canPromote && (
            <button onClick={handlePromote} disabled={promoting} style={ghostButtonStyle}>
              {promoting ? 'Promoting…' : `Promote to ${nextMajorLabel(latestVersion!.version)}`}
            </button>
          )}
          <button onClick={handleSave} disabled={isSaving} style={primaryButtonStyle}>
            {isSaving ? 'Saving…' : 'Save & Validate'}
          </button>
        </div>
      </div>

      {/* Config */}
      <ConfigPanel config={config} onChange={setConfig} />

      {/* Monaco */}
      <div style={{ border: '1px solid #2d2d4e', borderRadius: 8, overflow: 'hidden' }}>
        <Editor
          height="520px"
          language="go"
          theme="vs-dark"
          value={source}
          onChange={(v) => setSource(v ?? '')}
          options={{
            minimap: { enabled: false },
            fontSize: 14,
            wordWrap: 'on',
            scrollBeyondLastLine: false,
            tabSize: 4,
            insertSpaces: false,
            automaticLayout: true,
          }}
        />
      </div>

      {/* Status */}
      <StatusBar
        status={saveStatus}
        error={saveError}
        version={pendingVersion ?? latestVersion?.version}
      />

      {/* Test dialog */}
      {showTestDialog && (
        <TestDialog
          maps={maps}
          loadingMaps={loadingMaps}
          onTest={handleTest}
          onClose={() => setShowTestDialog(false)}
        />
      )}
    </Layout>
  );
}

type TestOpponent = 'scout' | 'bruiser' | 'ranger';
