import { useEffect, useRef, useState } from 'react';
import { useNavigate, useParams, Link, useBlocker } from 'react-router-dom';
import Editor from '@monaco-editor/react';
import type * as MonacoType from 'monaco-editor';
import { syntaxCheck } from '../lib/syntaxCheck';
import Layout from '../components/Layout';
import {
  createTank,
  getTank,
  submitVersion,
  getVersionStatus,
  getVersionSource,
  promoteVersion,
  startMatch,
  listMaps,
  listGameDays,
  registerForGameDay,
  withdrawRegistration,
  updateTank,
  type OpponentSpec,
} from '../services/api';
import type { Tank, TankVersion, TankConfig, GameDay, GameMap } from '../types';
import { cardStyle, primaryButtonStyle, ghostButtonStyle } from '../components/Layout';
import { AvatarPicker, avatarSrc } from '../components/AvatarPicker';

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

function defaultSource(): string {
  return `func Tick(s Sensors) Action {
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

function buildSource(body: string, cfg: TankConfig, extraImports: string[] = []): string {
  const sdkLine = '. "github.com/tankmaze/sdk"';
  const importBlock = extraImports.length === 0
    ? `import ${sdkLine}`
    : `import (\n\t${sdkLine}\n${extraImports.map(p => `\t"${p}"`).join('\n')}\n)`;
  return `package tank\n\n${importBlock}\n\nvar Config = TankConfig{\n\tName:        ${JSON.stringify(cfg.name)},\n\tSpeed:       ${cfg.speed},\n\tSensorRange: ${cfg.sensorRange},\n\tDamage:      ${cfg.damage},\n\tArmor:       ${cfg.armor},\n\tFireRate:    ${cfg.fireRate},\n}\n\n${body}`;
}

function parseExtraImports(src: string): string[] {
  const blockMatch = src.match(/import \(([\s\S]*?)\)/);
  if (!blockMatch) return [];
  return blockMatch[1]
    .split('\n')
    .map(l => l.trim())
    .filter(l => l && !l.includes('tankmaze'))
    .map(l => l.replace(/^"(.*)"$/, '$1'))
    .filter(pkg => (STDLIB_IMPORTS as readonly string[]).includes(pkg));
}

// Strip the locked preamble from sources loaded from S3 or old localStorage values.
// When the source contains the full preamble (package tank + import + var Config
// block), skip past the closing "}\n\n" of the var Config block so the Config
// declaration is not leaked into the editable body. For preamble-free bodies that
// start with user-declared var/const/type blocks (e.g. Scout's var block), those
// declarations are preserved because we never reach the fallback path.
function stripPreamble(src: string): string {
  // Fast path: full preamble produced by buildSource always contains this marker.
  const configMarker = '\n\nvar Config = TankConfig{';
  const configStart = src.indexOf(configMarker);
  if (configStart >= 0) {
    // Find the standalone closing brace (at start of line) followed by \n\n.
    const closeMarker = '\n}\n\n';
    const closeIdx = src.indexOf(closeMarker, configStart);
    if (closeIdx >= 0) {
      return src.slice(closeIdx + closeMarker.length);
    }
  }
  // Fallback: no Config block found — if the source already starts with a
  // top-level declaration it's a body-only string (e.g. from localStorage);
  // return it as-is so leading var/const/type declarations are never dropped.
  if (/^(var |const |type |func )/.test(src)) return src;
  // No package declaration means this is body content (e.g. a localStorage body
  // that starts with a comment block from a Randy/AI fork). The double-newline
  // scan below would drop everything before the first \n\nvar/func, including
  // leading comments and helper functions. Return as-is.
  if (!src.startsWith('package ') && !src.includes('\npackage ')) return src;
  // AI-converted source: single dot-import of SDK with no Config block.
  // Return everything after the import line so helper functions defined
  // before the first var block (e.g. randDir in Randy forks) are preserved.
  const sdkImportSuffix = '\nimport . "github.com/tankmaze/sdk"\n\n';
  const sdkImportEnd = src.indexOf(sdkImportSuffix);
  if (sdkImportEnd >= 0) {
    return src.slice(sdkImportEnd + sdkImportSuffix.length);
  }
  // Otherwise strip the package/import header by finding the earliest token.
  const idx = Math.min(
    ...['\n\nfunc ', '\n\nvar ', '\n\nconst ', '\n\ntype '].map(t => {
      const i = src.indexOf(t);
      return i >= 0 ? i : Infinity;
    })
  );
  return isFinite(idx) ? src.slice(idx + 2) : src;
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

const STDLIB_IMPORTS = ['fmt', 'log', 'math', 'math/rand', 'sort'] as const;

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

function PreambleBanner({
  cfg, imports, onImportsChange,
}: {
  cfg: TankConfig;
  imports: string[];
  onImportsChange: (i: string[]) => void;
}) {
  const [expanded, setExpanded] = useState(false);

  function toggleImport(pkg: string) {
    onImportsChange(imports.includes(pkg)
      ? imports.filter(i => i !== pkg)
      : [...imports, pkg]);
  }

  const sdkLine = '. "github.com/tankmaze/sdk"';
  const importBlock = imports.length === 0
    ? `import ${sdkLine}`
    : `import (\n\t${sdkLine}\n${imports.map(p => `\t"${p}"`).join('\n')}\n)`;
  const preamble = `package tank\n\n${importBlock}\n\nvar Config = TankConfig{\n\tName:        ${JSON.stringify(cfg.name)},\n\tSpeed:       ${cfg.speed},\n\tSensorRange: ${cfg.sensorRange},\n\tDamage:      ${cfg.damage},\n\tArmor:       ${cfg.armor},\n\tFireRate:    ${cfg.fireRate},\n}`;

  return (
    <div style={{ marginBottom: 8, border: '1px solid #2d2d4e', borderRadius: 6, overflow: 'hidden' }}>
      <button
        onClick={() => setExpanded((e) => !e)}
        style={{
          width: '100%', background: '#1a1a2e', border: 'none', color: '#64748b',
          padding: '6px 12px', textAlign: 'left', cursor: 'pointer', fontSize: 12,
          display: 'flex', alignItems: 'center', gap: 6,
        }}
      >
        <span>{expanded ? '▾' : '▸'}</span>
        <span>Preamble (read-only) — package, imports, Config</span>
        {imports.length > 0 && (
          <span style={{ marginLeft: 'auto', fontSize: 11, color: '#60a5fa' }}>
            +{imports.length} stdlib import{imports.length > 1 ? 's' : ''}
          </span>
        )}
      </button>
      {expanded && (
        <>
          <div style={{
            padding: '8px 12px', background: '#0d0d1a', borderTop: '1px solid #1e1e3a',
            display: 'flex', gap: 16, flexWrap: 'wrap',
          }}>
            <span style={{ fontSize: 11, color: '#64748b', alignSelf: 'center' }}>stdlib:</span>
            {STDLIB_IMPORTS.map(pkg => (
              <label key={pkg} style={{ display: 'flex', alignItems: 'center', gap: 5, cursor: 'pointer', fontSize: 12, color: '#94a3b8' }}>
                <input
                  type="checkbox"
                  checked={imports.includes(pkg)}
                  onChange={() => toggleImport(pkg)}
                />
                {pkg}
              </label>
            ))}
          </div>
          <pre style={{
            margin: 0, padding: '10px 14px', background: '#0d0d1a', borderTop: '1px solid #1e1e3a',
            color: '#64748b', fontSize: 12, overflowX: 'auto', userSelect: 'none',
          }}>{preamble}</pre>
        </>
      )}
    </div>
  );
}

function StatusBar({
  status, error, version, pollingPhase, elapsedSecs,
}: {
  status: string;
  error: string | null;
  version?: string;
  pollingPhase?: 'queued' | 'compiling' | null;
  elapsedSecs?: number;
}) {
  if (status === 'idle') return null;

  let dot: string;
  let label: string;
  if (status === 'submitting') {
    dot = '#94a3b8'; label = 'Uploading…';
  } else if (status === 'polling') {
    dot = '#fbbf24';
    label = (!pollingPhase || pollingPhase === 'queued') ? 'Queued…' : `Compiling… ${elapsedSecs ?? 0}s`;
  } else if (status === 'ready') {
    dot = '#4ade80'; label = `Compiled OK${version ? ` · ${version}` : ''}`;
  } else if (status === 'failed') {
    dot = '#f87171'; label = 'Compile failed';
  } else {
    dot = '#94a3b8'; label = status;
  }

  return (
    <div style={{ marginTop: 8 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: error ? 8 : 0 }}>
        <span style={{ width: 8, height: 8, borderRadius: '50%', background: dot, flexShrink: 0 }} />
        <span style={{ fontSize: 13, color: '#94a3b8' }}>{label}</span>
      </div>
      {error && (
        <pre style={{
          background: '#1c0a0a', border: '1px solid #7f1d1d', color: '#fca5a5',
          borderRadius: 6, padding: '12px 16px', fontSize: 12,
          overflowX: 'auto', overflowY: 'auto', maxHeight: '200px',
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
  const [mapId, setMapId] = useState<string | null>(() => {
    const saved = localStorage.getItem('tankmaze:lastMapId');
    return saved ?? null;
  });

  function selectMap(id: string | null) {
    setMapId(id);
    if (id === null) localStorage.removeItem('tankmaze:lastMapId');
    else localStorage.setItem('tankmaze:lastMapId', id);
  }

  return (
    <div style={overlay}>
      <div style={{ ...cardStyle, width: 420, maxHeight: '80vh', overflowY: 'auto' }}>
        <h3 style={{ margin: '0 0 16px', color: '#e2e8f0' }}>Test vs AI</h3>

        <div style={{ marginBottom: 16 }}>
          <p style={{ margin: '0 0 8px', fontSize: 13, color: '#94a3b8' }}>Opponent</p>
          {(['scout', 'bruiser', 'ranger', 'randy'] as TestOpponent[]).map((op) => (
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
                <input type="radio" name="map" value="" checked={mapId === null} onChange={() => selectMap(null)} />
                <span style={{ color: '#e2e8f0', fontSize: 14 }}>Random (default)</span>
              </label>
              {maps.map((m) => (
                <label key={m.mapId} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, cursor: 'pointer' }}>
                  <input type="radio" name="map" value={m.mapId} checked={mapId === m.mapId} onChange={() => selectMap(m.mapId)} />
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

function GameDayPickerModal({
  gameDays, loading, onSelect, onClose,
}: {
  gameDays: GameDay[];
  loading: boolean;
  onSelect: (gameDayId: string) => void;
  onClose: () => void;
}) {
  const now = new Date();
  const sorted = [...gameDays]
    .filter((gd) => new Date(gd.schedule.registrationClose) >= now && gd.phases.roundRobin.status === 'upcoming')
    .sort((a, b) => (a.schedule.registrationClose < b.schedule.registrationClose ? -1 : 1));

  return (
    <div style={overlay}>
      <div style={{ ...cardStyle, width: 440, maxHeight: '80vh', overflowY: 'auto' }}>
        <h3 style={{ margin: '0 0 16px', color: '#e2e8f0' }}>Select Game Day</h3>

        {loading ? (
          <p style={{ color: '#64748b', fontSize: 13, margin: '0 0 16px' }}>Loading game days…</p>
        ) : sorted.length === 0 ? (
          <p style={{ color: '#64748b', fontSize: 13, margin: '0 0 16px' }}>No open game days right now.</p>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 16 }}>
            {sorted.map((gd) => {
              const regClose = new Date(gd.schedule.registrationClose).toLocaleDateString(undefined, {
                year: 'numeric', month: 'short', day: 'numeric',
              });
              const final = new Date(gd.schedule.final).toLocaleDateString(undefined, {
                year: 'numeric', month: 'short', day: 'numeric',
              });
              return (
                <button
                  key={gd.gameDayId}
                  onClick={() => onSelect(gd.gameDayId)}
                  style={{
                    background: '#1a1a2e', border: '1px solid #2d2d4e', borderRadius: 6,
                    color: '#e2e8f0', padding: '10px 14px', textAlign: 'left',
                    cursor: 'pointer', display: 'flex', flexDirection: 'column', gap: 4,
                  }}
                >
                  <span style={{ fontSize: 14, fontWeight: 600 }}>{gd.name ?? final}</span>
                  <span style={{ fontSize: 12, color: '#64748b' }}>
                    Registration closes {regClose} · Final {final}
                  </span>
                </button>
              );
            })}
          </div>
        )}

        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <button onClick={onClose} style={ghostButtonStyle}>Cancel</button>
        </div>
      </div>
    </div>
  );
}

function UnsavedChangesDialog({
  onSaveAndLeave, onDiscard, onStay,
}: {
  onSaveAndLeave: () => void;
  onDiscard: () => void;
  onStay: () => void;
}) {
  return (
    <div style={overlay}>
      <div style={{ ...cardStyle, width: 380 }}>
        <h3 style={{ margin: '0 0 8px', color: '#e2e8f0' }}>Unsaved changes</h3>
        <p style={{ margin: '0 0 20px', fontSize: 14, color: '#94a3b8' }}>
          You have unsaved changes. Do you want to save before leaving?
        </p>
        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <button onClick={onStay} style={ghostButtonStyle}>Keep editing</button>
          <button onClick={onDiscard} style={{ ...ghostButtonStyle, color: '#f87171', borderColor: '#7f1d1d' }}>
            Discard
          </button>
          <button onClick={onSaveAndLeave} style={primaryButtonStyle}>Save & Leave</button>
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
  const [avatarUrl, setAvatarUrl] = useState<string | undefined>(undefined);
  const [source, setSource] = useState('');
  const [config, setConfig] = useState<TankConfig>(DEFAULT_CONFIG);
  const [extraImports, setExtraImports] = useState<string[]>([]);
  const [saveStatus, setSaveStatus] = useState<SaveStatus>('idle');
  const [saveError, setSaveError] = useState<string | null>(null);
  const [compileLimitReached, setCompileLimitReached] = useState(false);
  const [pendingVersion, setPendingVersion] = useState<string | null>(null);
  const [pollingPhase, setPollingPhase] = useState<'queued' | 'compiling' | null>(null);
  const [elapsedSecs, setElapsedSecs] = useState(0);
  const [showTestDialog, setShowTestDialog] = useState(false);
  const [maps, setMaps] = useState<GameMap[]>([]);
  const [loadingMaps, setLoadingMaps] = useState(false);
  const [pageLoading, setPageLoading] = useState(true);
  const [pageError, setPageError] = useState<string | null>(null);
  const [promoting, setPromoting] = useState(false);
  const [registering, setRegistering] = useState(false);
  const [showGameDayPicker, setShowGameDayPicker] = useState(false);
  const [gameDays, setGameDays] = useState<GameDay[]>([]);
  const [loadingGameDays, setLoadingGameDays] = useState(false);
  const [gameDaysLoaded, setGameDaysLoaded] = useState(false);

  const pollCancelRef = useRef(false);
  const pollingStartRef = useRef<number | null>(null);
  const editorRef = useRef<MonacoType.editor.IStandaloneCodeEditor | null>(null);
  const monacoRef = useRef<typeof MonacoType | null>(null);
  // Prevents the config persistence effect from writing DEFAULT_CONFIG to
  // localStorage before getTank resolves and loads the real config.
  const configLoadedRef = useRef(false);
  // Tracks the source/config as of the last successful save (or initial load),
  // so we can detect unsaved changes for the navigation guard.
  const savedStateRef = useRef<{ source: string; config: TankConfig } | null>(null);
  // Captures what was submitted so the polling loop can update savedStateRef on success.
  const submittedStateRef = useRef<{ source: string; config: TankConfig } | null>(null);

  // Load tank on mount — skip for the 'new' route (tank not created yet)
  useEffect(() => {
    configLoadedRef.current = false;
    savedStateRef.current = null;
    if (!tankId || tankId === 'new') {
      const initSource = defaultSource();
      setSource(initSource);
      savedStateRef.current = { source: initSource, config: DEFAULT_CONFIG };
      configLoadedRef.current = true;
      setPageLoading(false);
      return;
    }
    getTank(tankId)
      .then(async ({ versions: v, ...t }) => {
        setTank(t);
        setAvatarUrl(t.avatarUrl);
        setVersions(v ?? []);

        const latestVer = sortedByAge(v ?? [])[0];

        // Source: localStorage → own version → fork origin → default.
        // Always strip preamble — old localStorage/S3 values may include it.
        let srcToSet: string;
        let fetchedRaw = '';
        const savedSrc = localStorage.getItem(`tankmaze-src-${tankId}`);
        if (savedSrc) {
          srcToSet = stripPreamble(savedSrc);
        } else {
          const attempts: Array<[string, string]> = [];
          if (latestVer?.sourceS3Key) attempts.push([tankId, latestVer.version]);
          if (t.forkedFromTankId && t.forkedFromVersion) {
            attempts.push([t.forkedFromTankId, t.forkedFromVersion]);
          }
          let loaded = false;
          srcToSet = defaultSource();
          for (const [tid, ver] of attempts) {
            try {
              const { source: fetched } = await getVersionSource(tid, ver);
              fetchedRaw = fetched;
              srcToSet = stripPreamble(fetched);
              loaded = true;
              break;
            } catch { /* try next */ }
          }
          if (!loaded) srcToSet = defaultSource();
        }
        setSource(srcToSet);

        // Extra imports: prefer localStorage, then parse from S3 preamble.
        // Forks of AI tanks have their own converted source that has stdlib imports
        // stripped during the AI-to-tank conversion — fall back to parsing from the
        // fork origin source when own source has no detected stdlib imports.
        const savedImports = localStorage.getItem(`tankmaze-imports-${tankId}`);
        if (savedImports) {
          setExtraImports(JSON.parse(savedImports) as string[]);
        } else if (t.forkedFromTankId && t.forkedFromVersion) {
          const fromOwn = parseExtraImports(fetchedRaw);
          if (fromOwn.length > 0) {
            setExtraImports(fromOwn);
          } else {
            try {
              const { source: originSrc } = await getVersionSource(t.forkedFromTankId, t.forkedFromVersion);
              setExtraImports(parseExtraImports(originSrc));
            } catch { /* no stdlib imports in origin */ }
          }
        } else if (fetchedRaw) {
          setExtraImports(parseExtraImports(fetchedRaw));
        }

        // Config: prefer localStorage, then seed from API (tank name + version stats).
        // Always override name from the API — localStorage name can be stale or empty.
        let cfgToSet: TankConfig;
        const savedCfg = localStorage.getItem(`tankmaze-cfg-${tankId}`);
        if (savedCfg) {
          const parsed = JSON.parse(savedCfg) as TankConfig;
          cfgToSet = { ...parsed, name: t.name || parsed.name || DEFAULT_CONFIG.name };
        } else {
          const vc = latestVer?.config;
          cfgToSet = {
            name: t.name || DEFAULT_CONFIG.name,
            speed: vc?.speed ?? DEFAULT_CONFIG.speed,
            sensorRange: vc?.sensorRange ?? DEFAULT_CONFIG.sensorRange,
            damage: vc?.damage ?? DEFAULT_CONFIG.damage,
            armor: vc?.armor ?? DEFAULT_CONFIG.armor,
            fireRate: vc?.fireRate ?? DEFAULT_CONFIG.fireRate,
          };
        }
        setConfig(cfgToSet);

        savedStateRef.current = { source: srcToSet, config: cfgToSet };
        configLoadedRef.current = true;

        // Reflect latest version's compile status.
        if (latestVer?.compileStatus === 'ready') setSaveStatus('ready');
        if (latestVer?.compileStatus === 'failed') {
          setSaveStatus('failed');
          setSaveError(latestVer.compileError ?? 'Unknown error');
        }
        // Resume polling if compile was in-flight when user left.
        if (latestVer?.compileStatus === 'pending' || latestVer?.compileStatus === 'compiling') {
          setPendingVersion(latestVer.version);
          setSaveStatus('polling');
        }
      })
      .catch((e: Error) => setPageError(e.message))
      .finally(() => setPageLoading(false));
  }, [tankId]);

  // Pre-fetch game days when the tank is already registered, so Withdraw can
  // be hidden for game days that have already started/completed (item 211;
  // same gate TankDetail.tsx uses, mirrored here since this page's own
  // multi-registration Withdraw loop never got it originally).
  useEffect(() => {
    const ids = versions.flatMap((v) => v.registeredForGameDays ?? []);
    if (ids.length > 0 && gameDays.length === 0) {
      setLoadingGameDays(true);
      listGameDays()
        .then((days) => { setGameDays(days); setGameDaysLoaded(true); })
        .catch(() => { setGameDays([]); setGameDaysLoaded(true); })
        .finally(() => setLoadingGameDays(false));
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [versions]);

  // Polling loop
  useEffect(() => {
    if (saveStatus !== 'polling' || !pendingVersion || !tankId) return;
    pollCancelRef.current = false;
    pollingStartRef.current = Date.now();
    setPollingPhase('queued');
    setElapsedSecs(0);

    const timer = setInterval(() => {
      if (pollingStartRef.current) {
        setElapsedSecs(Math.floor((Date.now() - pollingStartRef.current) / 1000));
      }
    }, 1000);

    async function poll() {
      while (!pollCancelRef.current) {
        await new Promise((r) => setTimeout(r, 2500));
        if (pollCancelRef.current) break;
        try {
          const s = await getVersionStatus(tankId!, pendingVersion!);
          if (s.compileStatus === 'compiling') setPollingPhase('compiling');
          if (s.compileStatus === 'ready') {
            setPollingPhase(null);
            setSaveStatus('ready');
            setSaveError(null);
            clearSyntaxMarkers();
            if (submittedStateRef.current) savedStateRef.current = submittedStateRef.current;
            getTank(tankId!).then(({ versions: v }) => setVersions(v ?? []));
            break;
          }
          if (s.compileStatus === 'failed') {
            setPollingPhase(null);
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
    return () => {
      pollCancelRef.current = true;
      clearInterval(timer);
    };
  }, [saveStatus, pendingVersion, tankId]);

  // Persist source to localStorage on change (skip while in new-tank mode)
  useEffect(() => {
    if (configLoadedRef.current && tankId && tankId !== 'new' && source) {
      localStorage.setItem(`tankmaze-src-${tankId}`, source);
    }
  }, [tankId, source]);

  useEffect(() => {
    if (configLoadedRef.current && tankId && tankId !== 'new') {
      localStorage.setItem(`tankmaze-cfg-${tankId}`, JSON.stringify(config));
    }
  }, [tankId, config]);

  useEffect(() => {
    if (configLoadedRef.current && tankId && tankId !== 'new') {
      localStorage.setItem(`tankmaze-imports-${tankId}`, JSON.stringify(extraImports));
    }
  }, [tankId, extraImports]);

  function clearSyntaxMarkers() {
    const editor = editorRef.current;
    const monaco = monacoRef.current;
    if (editor && monaco) {
      const model = editor.getModel();
      if (model) monaco.editor.setModelMarkers(model, 'go-syntax', []);
    }
  }

  async function handleSave() {
    const statSum = STAT_NAMES.reduce((acc, k) => acc + config[k], 0);
    if (statSum !== STAT_SUM_TARGET) {
      setSaveError(`Stat points must sum to ${STAT_SUM_TARGET} (currently ${statSum})`);
      setSaveStatus('failed');
      return;
    }

    // Browser-side syntax pre-check — abort before uploading if there are parse errors.
    clearSyntaxMarkers();
    try {
      const fullSrc = buildSource(source, config, extraImports);
      const errors = await syntaxCheck(fullSrc);
      if (errors.length > 0) {
        // Map error lines from full-source positions to body-only Monaco positions.
        const closeIdx = fullSrc.lastIndexOf('\n}\n\n');
        const preambleLines = closeIdx >= 0
          ? fullSrc.slice(0, closeIdx + '\n}\n\n'.length).split('\n').length - 1
          : 0;
        const editor = editorRef.current;
        const monaco = monacoRef.current;
        if (editor && monaco) {
          const model = editor.getModel();
          if (model) {
            monaco.editor.setModelMarkers(model, 'go-syntax', errors.map(e => ({
              startLineNumber: Math.max(1, e.line - preambleLines),
              startColumn: e.col,
              endLineNumber: Math.max(1, e.line - preambleLines),
              endColumn: e.col + 80,
              message: e.message,
              severity: monaco.MarkerSeverity.Error,
            })));
          }
        }
        setSaveStatus('failed');
        setSaveError('Syntax errors:\n' + errors.map(e => `line ${e.line - preambleLines}: ${e.message}`).join('\n'));
        return;
      }
    } catch {
      // WASM unavailable — skip pre-check, let CodeBuild catch syntax errors
    }

    setSaveStatus('submitting');
    setSaveError(null);
    submittedStateRef.current = { source, config };
    try {
      let id = tankId;
      if (!id || id === 'new') {
        const created = await createTank(config.name || 'My Tank');
        id = created.tankId;
        navigate(`/tanks/${id}/edit`, { replace: true });
      } else if (tank && config.name.trim() !== '' && config.name.trim() !== tank.name) {
        await updateTank(id, { name: config.name.trim() });
        setTank({ ...tank, name: config.name.trim() });
      }
      const v = await submitVersion(id, buildSource(source, config, extraImports), config);
      setPendingVersion(v.version);
      setSaveStatus('polling');
    } catch (e) {
      const msg = e instanceof Error ? e.message : 'Submit failed';
      if (msg.startsWith('429')) {
        setCompileLimitReached(true);
        setSaveStatus('idle');
      } else {
        setSaveStatus('failed');
        setSaveError(msg);
      }
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

  async function openRegisterPicker() {
    setShowGameDayPicker(true);
    if (gameDays.length === 0) {
      setLoadingGameDays(true);
      listGameDays()
        .then((days) => { setGameDays(days); setGameDaysLoaded(true); })
        .catch(() => { setGameDays([]); setGameDaysLoaded(true); })
        .finally(() => setLoadingGameDays(false));
    }
  }

  async function handleRegister(gameDayId: string) {
    if (!tankId) return;
    const latest = sortedByAge(versions)[0];
    if (!latest || !isMajor(latest.version)) return;
    setShowGameDayPicker(false);
    setRegistering(true);
    try {
      await registerForGameDay(tankId, latest.version, gameDayId);
      const { versions: v } = await getTank(tankId);
      setVersions(v ?? []);
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : 'Registration failed');
    } finally {
      setRegistering(false);
    }
  }

  async function handleWithdraw(gameDayId: string) {
    if (!tankId) return;
    const latest = sortedByAge(versions)[0];
    if (!latest || !isMajor(latest.version)) return;
    setRegistering(true);
    try {
      await withdrawRegistration(tankId, latest.version, gameDayId);
      const { versions: v } = await getTank(tankId);
      setVersions(v ?? []);
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : 'Withdraw failed');
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

  // Unsaved-changes guard
  const dirty = savedStateRef.current !== null && (
    source !== savedStateRef.current.source ||
    JSON.stringify(config) !== JSON.stringify(savedStateRef.current.config)
  );
  const blocker = useBlocker(dirty);

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
  const isDisqualified = !!latestVersion?.disqualified;
  const isRegistered = canRegister && (latestVersion?.registeredForGameDays?.length ?? 0) > 0;
  const isSaving = saveStatus === 'submitting' || saveStatus === 'polling';

  if (pageLoading) return <Layout><p style={{ color: '#64748b' }}>Loading…</p></Layout>;
  if (pageError) return <Layout><p style={{ color: '#f87171' }}>{pageError}</p></Layout>;

  return (
    <Layout>
      {/* Mobile read-only notice (hidden on tablet/desktop via responsive.css) */}
      <div className="tm-mobile-readonly" style={{
        background: '#1a1a2e', border: '1px solid #2d2d4e', borderRadius: 10,
        padding: '24px 20px', textAlign: 'center',
      }}>
        <div style={{ fontSize: 32, marginBottom: 12 }}>🖥️</div>
        <p style={{ margin: '0 0 8px', color: '#e2e8f0', fontWeight: 600, fontSize: 16 }}>
          Desktop browser required
        </p>
        <p style={{ margin: 0, color: '#64748b', fontSize: 14, lineHeight: 1.5 }}>
          Tank code editing requires a desktop browser. You can view this page on a desktop to edit your tank.
        </p>
      </div>

      {/* Editor — hidden on mobile via responsive.css */}
      <div className="tm-mobile-editor">

      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16, flexWrap: 'wrap', gap: 12 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <Link to="/dashboard" style={{ color: '#64748b', textDecoration: 'none', fontSize: 13 }}>
            ← My Tanks
          </Link>
          {tankId && tankId !== 'new' && tank && (
            <img
              src={avatarSrc(tankId, avatarUrl)}
              alt=""
              style={{ width: 36, height: 36, borderRadius: 6, imageRendering: 'pixelated', border: '1px solid #2d2d4e' }}
            />
          )}
          <h2 style={{ margin: 0, fontSize: 18, color: '#e2e8f0' }}>
            {tank?.name || 'Unnamed Tank'}
          </h2>
          {tankId && tankId !== 'new' && (
            <Link
              to={`/tanks/${tankId}`}
              style={{ color: '#64748b', textDecoration: 'none', fontSize: 13 }}
            >
              View tank →
            </Link>
          )}
          {latestVersion && (
            <span style={{
              fontSize: 12, padding: '2px 8px', borderRadius: 4, fontWeight: 500,
              background: isMajor(latestVersion.version) ? '#1e3a2f' : '#1e2a3a',
              color: isMajor(latestVersion.version) ? '#4ade80' : '#60a5fa',
            }}>
              {latestVersion.version} {isMajor(latestVersion.version) ? 'major' : 'minor'}
            </span>
          )}
          {isDisqualified && (
            <span style={{ background: '#f87171', color: '#fff', fontSize: 10, padding: '1px 6px', borderRadius: 4, fontWeight: 600 }}>DQ</span>
          )}
        </div>

        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          {canRegister && (
            <>
              {gameDaysLoaded && (latestVersion?.registeredForGameDays ?? []).map((gdId) => {
                const gd = gameDays.find((d) => d.gameDayId === gdId);
                if (!gd || gd.phases.roundRobin.status !== 'upcoming') return null;
                return (
                  <button key={gdId} onClick={() => handleWithdraw(gdId)} disabled={registering} style={ghostButtonStyle}>
                    {registering ? '…' : `Withdraw ·${gdId.slice(-6)}`}
                  </button>
                );
              })}
              <button
                onClick={openRegisterPicker}
                disabled={registering || isDisqualified}
                title={isDisqualified ? 'This version is disqualified due to excessive tick violations and cannot register for Game Days.' : undefined}
                style={{ ...ghostButtonStyle, opacity: isDisqualified ? 0.5 : 1, cursor: isDisqualified ? 'not-allowed' : 'pointer' }}
              >
                {registering ? '…' : 'Register for Game Day'}
              </button>
            </>
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

      {/* Avatar picker — only for existing tanks (not /tanks/new) */}
      {tankId && tankId !== 'new' && tank && (
        <details style={{ ...cardStyle, marginBottom: 12 }}>
          <summary style={{ cursor: 'pointer', fontSize: 13, fontWeight: 600, color: '#94a3b8', userSelect: 'none' }}>
            Avatar
          </summary>
          <div style={{ marginTop: 12 }}>
            <AvatarPicker
              tankId={tankId}
              current={avatarUrl}
              onSaved={setAvatarUrl}
            />
          </div>
        </details>
      )}

      {/* Compilation limit banner */}
      {compileLimitReached && (
        <div style={{ background: '#1c1200', border: '1px solid #92400e', color: '#fbbf24', borderRadius: 8, padding: '12px 16px', marginBottom: 12, fontSize: 14 }}>
          Compilation limit reached for this period.{' '}
          <a href="/account" style={{ color: '#f59e0b', fontWeight: 600 }}>View your account</a>{' '}
          to see your quota or upgrade your plan.
        </div>
      )}

      {/* Status — shown between config and editor so it's always visible */}
      <StatusBar
        status={saveStatus}
        error={saveError}
        version={pendingVersion ?? latestVersion?.version}
        pollingPhase={pollingPhase}
        elapsedSecs={elapsedSecs}
      />

      {/* Preamble banner */}
      <PreambleBanner cfg={config} imports={extraImports} onImportsChange={setExtraImports} />

      {/* Monaco */}
      <div style={{ border: '1px solid #2d2d4e', borderRadius: 8, overflow: 'hidden' }}>
        <Editor
          height="520px"
          language="go"
          theme="vs-dark"
          value={source}
          onChange={(v) => setSource(v ?? '')}
          onMount={(editor, monaco) => {
            editorRef.current = editor;
            monacoRef.current = monaco;
          }}
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

      {/* Test dialog */}
      {showTestDialog && (
        <TestDialog
          maps={maps}
          loadingMaps={loadingMaps}
          onTest={handleTest}
          onClose={() => setShowTestDialog(false)}
        />
      )}

      {/* Game Day picker */}
      {showGameDayPicker && (
        <GameDayPickerModal
          gameDays={gameDays}
          loading={loadingGameDays}
          onSelect={handleRegister}
          onClose={() => setShowGameDayPicker(false)}
        />
      )}

      {/* Unsaved-changes guard */}
      {blocker.state === 'blocked' && (
        <UnsavedChangesDialog
          onSaveAndLeave={() => { handleSave(); blocker.proceed?.(); }}
          onDiscard={() => blocker.proceed?.()}
          onStay={() => blocker.reset?.()}
        />
      )}

      </div>{/* end tm-mobile-editor */}
    </Layout>
  );
}

type TestOpponent = 'scout' | 'bruiser' | 'ranger' | 'randy';
