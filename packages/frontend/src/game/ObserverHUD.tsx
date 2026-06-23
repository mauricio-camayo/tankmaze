import { useState } from 'react';
import type { MatchSnapshot, TickUpdate } from '../types';
import type { PlaybackSpeed } from '../store/matchStore';

interface ObserverHUDProps {
  snapshot: MatchSnapshot;
  ticks: TickUpdate[];
  currentTick: number;
  isPlaying: boolean;
  speed: PlaybackSpeed;
  matchOver: { winner: 'a' | 'b' | null; reason: string } | null;
  myTankSide?: 'a' | 'b' | 'both' | null;
  onPlay: () => void;
  onPause: () => void;
  onStep: () => void;
  onSeek: (tick: number) => void;
  onSpeed: (s: PlaybackSpeed) => void;
}

const SPEEDS: PlaybackSpeed[] = [0.25, 0.5, 1, 2, 4, 8, 'step'];

// Direction keys: Go SDK serialises map[Direction]int with int keys as JSON strings.
// N=0, S=1, E=2, W=3 (tankmaze.Direction constants).
const DIR_KEYS: [string, string][] = [['0', 'N'], ['2', 'E'], ['1', 'S'], ['3', 'W']];

function HPBar({ label, hp, color }: { label: string; hp: number; color: string }) {
  const pct = Math.max(0, Math.min(100, hp));
  const barColor = pct > 50 ? '#4ade80' : pct > 25 ? '#fbbf24' : '#f87171';
  return (
    <div style={{ flex: 1, minWidth: 0 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 3 }}>
        <span style={{ color, fontSize: 12, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {label}
        </span>
        <span style={{ color: '#94a3b8', fontSize: 11, flexShrink: 0, marginLeft: 4 }}>{hp} HP</span>
      </div>
      <div style={{ height: 6, background: '#2d2d4e', borderRadius: 3, overflow: 'hidden' }}>
        <div style={{ height: '100%', width: `${pct}%`, background: barColor, borderRadius: 3, transition: 'width 0.08s' }} />
      </div>
    </div>
  );
}

function SensorDots({ sensors }: { sensors: Record<string, unknown> | undefined }) {
  if (!sensors) return null;
  const wd = sensors['WallDistances'] as Record<string, number> | undefined;
  const hasOpp = sensors['OpponentBearing'] != null;

  const dot = (blocked: boolean | null, title: string) => (
    <span
      title={title}
      style={{
        display: 'inline-block',
        width: 9, height: 9, borderRadius: 2,
        background: blocked === null ? '#374151' : blocked ? '#f87171' : '#4ade80',
        verticalAlign: 'middle',
      }}
    />
  );

  return (
    <div style={{ display: 'flex', gap: 6, alignItems: 'center', marginTop: 4, flexWrap: 'wrap' }}>
      {DIR_KEYS.map(([key, label]) => {
        const dist = wd ? wd[key] : undefined;
        const blocked = dist === undefined ? null : dist === 0;
        return (
          <span key={key} style={{ display: 'flex', alignItems: 'center', gap: 2, fontSize: 10, color: '#64748b' }}>
            {dot(blocked, `${label}: ${dist ?? '?'} cells`)} {label}
          </span>
        );
      })}
      <span style={{ display: 'flex', alignItems: 'center', gap: 2, fontSize: 10, color: '#64748b' }}>
        {dot(hasOpp, hasOpp ? 'Opponent in range' : 'Opponent not in range')} 👁
      </span>
    </div>
  );
}

function TankDebugPanel({ label, data, color, obscured }: {
  label: string;
  data: TickUpdate['tankA'];
  color: string;
  obscured: boolean;
}) {
  const sensors = data.sensors;
  const memory = sensors?.['Memory'] as Record<string, unknown> | undefined;

  return (
    <div style={{
      background: '#0f0f1a', border: '1px solid #2d2d4e',
      borderRadius: 6, padding: 10, fontSize: 11, lineHeight: 1.5,
    }}>
      <div style={{ color, fontWeight: 600, marginBottom: 4 }}>{label}</div>
      <div style={{ color: '#64748b' }}>
        ({data.position.x}, {data.position.y}) · {data.facing} · {data.hp} HP
      </div>
      {data.action && (
        <div style={{ color: '#94a3b8' }}>
          {data.action.type}{data.action.direction ? ` ${data.action.direction}` : ''}
        </div>
      )}
      {data.durationMs !== undefined && (
        <div style={{ color: data.durationMs > 50 ? '#fbbf24' : '#475569' }}>
          {data.durationMs}ms
          {data.violation && <span style={{ color: '#f87171', marginLeft: 6 }}>⚠ violation</span>}
        </div>
      )}

      <SensorDots sensors={sensors} />

      {!obscured && memory !== undefined && (
        <details style={{ marginTop: 4 }}>
          <summary style={{ color: '#64748b', cursor: 'pointer', userSelect: 'none' }}>memory</summary>
          <pre style={{
            color: '#475569', fontSize: 9, overflow: 'auto', maxHeight: 80,
            background: '#0a0a14', padding: 4, borderRadius: 3, margin: '3px 0 0',
            whiteSpace: 'pre-wrap', wordBreak: 'break-all',
          }}>
            {JSON.stringify(memory, null, 2)}
          </pre>
        </details>
      )}

      {!obscured && data.log && data.log.length > 0 && (
        <div style={{ marginTop: 4, borderTop: '1px solid #2d2d4e', paddingTop: 4, maxHeight: 160, overflowY: 'auto' }}>
          {data.log.slice(-10).map((line, i) => (
            <div key={i} style={{ color: '#475569', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {line}
            </div>
          ))}
        </div>
      )}

      {!obscured && (!data.log || data.log.length === 0) && (
        <div style={{ color: '#1e2433', fontSize: 10, marginTop: 4, fontStyle: 'italic' }}>
          no log output this tick
        </div>
      )}

      {obscured && (
        <div style={{ color: '#374151', fontSize: 10, marginTop: 4, fontStyle: 'italic' }}>
          memory & log hidden
        </div>
      )}
    </div>
  );
}

const ctrlBtn: React.CSSProperties = {
  background: 'none', border: '1px solid #2d2d4e', color: '#94a3b8',
  padding: '5px 10px', borderRadius: 6, cursor: 'pointer', fontSize: 13,
};

export default function ObserverHUD({
  snapshot, ticks, currentTick, isPlaying, speed, matchOver,
  myTankSide = null,
  onPlay, onPause, onStep, onSeek, onSpeed,
}: ObserverHUDProps) {
  const [showDebug, setShowDebug] = useState(false);

  const tickData = ticks.find((t) => t.tick === currentTick);
  const hpA = tickData?.tankA.hp ?? snapshot.tankA.hp;
  const hpB = tickData?.tankB.hp ?? snapshot.tankB.hp;
  const nameA = snapshot.tankA.config.name;
  const nameB = snapshot.tankB.config.name;

  const currentIdx = ticks.findIndex((t) => t.tick === currentTick);
  const maxIdx = Math.max(0, ticks.length - 1);
  const totalTicks = snapshot.totalTicks ?? ticks.length;

  // §9.6: show memory/log only for the viewer's own tank(s).
  const showPrivateA = myTankSide === 'a' || myTankSide === 'both';
  const showPrivateB = myTankSide === 'b' || myTankSide === 'both';

  return (
    <div style={{ color: '#e2e8f0' }}>
      {/* HP bars + tick counter */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 10 }}>
        <HPBar label={nameA} hp={hpA} color="#60a5fa" />
        <div style={{ flexShrink: 0, textAlign: 'center', minWidth: 64 }}>
          <div style={{ fontSize: 18, fontWeight: 700, color: '#e2e8f0', lineHeight: 1 }}>
            {currentTick}
          </div>
          <div style={{ fontSize: 11, color: '#64748b' }}>
            {totalTicks ? `/ ${totalTicks}` : 'tick'}
          </div>
        </div>
        <HPBar label={nameB} hp={hpB} color="#f97316" />
      </div>

      {/* Match over banner */}
      {matchOver && (
        <div style={{
          textAlign: 'center', padding: '7px 12px', marginBottom: 10,
          background: 'rgba(124,106,247,0.15)', borderRadius: 6,
          color: '#a78bfa', fontSize: 13,
        }}>
          {matchOver.winner === 'a'
            ? `${nameA} wins`
            : matchOver.winner === 'b'
            ? `${nameB} wins`
            : 'Draw'} — {matchOver.reason.replace(/_/g, ' ')}
        </div>
      )}

      {/* Controls row */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap', marginBottom: 8 }}>
        <button onClick={isPlaying ? onPause : onPlay} style={ctrlBtn}>
          {isPlaying ? '⏸' : '▶'}
        </button>
        <button onClick={onStep} style={ctrlBtn} title="Step forward one tick">⏭</button>

        <div style={{ display: 'flex', gap: 3, marginLeft: 6 }}>
          {SPEEDS.map((s) => (
            <button
              key={String(s)}
              onClick={() => onSpeed(s)}
              style={{
                ...ctrlBtn,
                background: speed === s ? '#7c6af7' : 'none',
                color: speed === s ? '#fff' : '#64748b',
                borderColor: speed === s ? '#7c6af7' : '#2d2d4e',
                fontSize: 11, padding: '3px 7px',
              }}
            >
              {s === 'step' ? 'step' : `${s}×`}
            </button>
          ))}
        </div>
      </div>

      {/* Scrubber */}
      {ticks.length > 1 && (
        <div style={{ marginBottom: 8 }}>
          <input
            type="range"
            min={0}
            max={maxIdx}
            value={Math.max(0, currentIdx)}
            onChange={(e) => {
              const idx = Number(e.target.value);
              const t = ticks[idx];
              if (t) onSeek(t.tick);
            }}
            style={{ width: '100%', accentColor: '#7c6af7' }}
          />
        </div>
      )}

      {/* Debug panel toggle */}
      <div>
        <button
          onClick={() => setShowDebug((d) => !d)}
          style={{ background: 'none', border: 'none', color: '#475569', cursor: 'pointer', fontSize: 12, padding: 0 }}
        >
          {showDebug ? '▾' : '▸'} Debug
        </button>

        {showDebug && tickData && (
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8, marginTop: 8 }}>
            <TankDebugPanel label={`${nameA} (A)`} data={tickData.tankA} color="#60a5fa" obscured={!showPrivateA} />
            <TankDebugPanel label={`${nameB} (B)`} data={tickData.tankB} color="#f97316" obscured={!showPrivateB} />
          </div>
        )}
      </div>
    </div>
  );
}
