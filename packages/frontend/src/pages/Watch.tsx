import Phaser from 'phaser';
import { useEffect, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import Layout from '../components/Layout';
import { ObserverScene } from '../game/ObserverScene';
import ObserverHUD from '../game/ObserverHUD';
import { ReplayController } from '../game/ReplayController';
import { ObserverSocket } from '../services/ws';
import { useMatchStore } from '../store/matchStore';
import type { PlaybackSpeed } from '../store/matchStore';

const CANVAS = 560;

export default function Watch() {
  const [searchParams] = useSearchParams();
  const matchId = searchParams.get('matchId');

  const hostRef    = useRef<HTMLDivElement>(null);
  const gameRef    = useRef<Phaser.Game | null>(null);
  const sceneRef   = useRef<ObserverScene | null>(null);
  const socketRef  = useRef<ObserverSocket | null>(null);
  const ctrlRef    = useRef<ReplayController | null>(null);

  // Mutable refs for stale-closure-safe access inside callbacks
  const ticksRef       = useRef(useMatchStore.getState().ticks);
  const curTickRef     = useRef(useMatchStore.getState().currentTick);
  const isPlayingRef   = useRef(false);

  const {
    snapshot, ticks, currentTick, isPlaying, speed,
    setSnapshot, applyTickUpdate, setCurrentTick, setPlaying, setSpeed, reset,
  } = useMatchStore();

  const [matchOver, setMatchOver] = useState<{ winner: 'a' | 'b' | null; reason: string } | null>(null);
  const [wsError,   setWsError]   = useState<string | null>(null);
  const [sceneReady, setSceneReady] = useState(false);

  // Keep refs in sync with Zustand state
  useEffect(() => { ticksRef.current    = ticks;     }, [ticks]);
  useEffect(() => { curTickRef.current  = currentTick; }, [currentTick]);
  useEffect(() => { isPlayingRef.current = isPlaying; }, [isPlaying]);

  // ── Phaser bootstrap ──────────────────────────────────────────────────
  useEffect(() => {
    if (!hostRef.current) return;
    const parent = hostRef.current;

    const game = new Phaser.Game({
      type:   Phaser.AUTO,
      parent,
      width:  CANVAS,
      height: CANVAS,
      backgroundColor: '#0a0a14',
      scene:  [ObserverScene],
      scale:  { mode: Phaser.Scale.NONE },
      banner: false,
    });
    gameRef.current = game;

    game.events.once('ready', () => {
      sceneRef.current = game.scene.getScene('ObserverScene') as ObserverScene;
      setSceneReady(true);
    });

    return () => {
      game.destroy(true);
      gameRef.current  = null;
      sceneRef.current = null;
      setSceneReady(false);
    };
  }, []);

  // ── WebSocket connection ──────────────────────────────────────────────
  useEffect(() => {
    if (!matchId) return;
    reset();
    setMatchOver(null);
    setWsError(null);

    const socket = new ObserverSocket();
    socketRef.current = socket;

    socket.connect(matchId, (event) => {
      switch (event.type) {
        case 'MATCH_SNAPSHOT':
          setSnapshot(event.payload);
          // For active matches start playing; for ended matches pause at end
          if (event.payload.status === 'active') {
            setPlaying(true);
          }
          break;
        case 'TICK_UPDATE':
          applyTickUpdate(event.payload);
          // In live mode advance the display tick with the stream
          if (!isPlayingRef.current || useMatchStore.getState().snapshot?.status === 'active') {
            setCurrentTick(event.payload.tick);
          }
          break;
        case 'HIT':
          sceneRef.current?.flashHit(event.payload.victim);
          break;
        case 'MATCH_OVER':
          setMatchOver({ winner: event.payload.winner, reason: event.payload.reason });
          setPlaying(false);
          break;
        case 'ERROR':
          setWsError(event.payload.message);
          break;
      }
    });

    return () => {
      socket.disconnect();
      socketRef.current = null;
    };
  }, [matchId]); // eslint-disable-line react-hooks/exhaustive-deps

  // ── Apply snapshot to scene once both are ready ────────────────────────
  useEffect(() => {
    if (!snapshot || !sceneReady || !sceneRef.current) return;
    sceneRef.current.initMaze(snapshot.maze);
    sceneRef.current.render(snapshot.tankA, snapshot.tankB, snapshot.projectiles, snapshot.tankA.tankId);
  }, [snapshot, sceneReady]);

  // ── Render scene whenever currentTick changes ──────────────────────────
  useEffect(() => {
    if (!snapshot || !sceneRef.current) return;
    const tick = ticks.find((t) => t.tick === currentTick);
    if (tick) {
      sceneRef.current.render(tick.tankA, tick.tankB, tick.projectiles, snapshot.tankA.tankId);
    }
  }, [currentTick]); // eslint-disable-line react-hooks/exhaustive-deps

  // ── Replay controller ─────────────────────────────────────────────────
  useEffect(() => {
    ctrlRef.current?.stop();
    if (!isPlaying || speed === 'step' || ticks.length === 0) return;

    const ctrl = new ReplayController();
    ctrlRef.current = ctrl;
    ctrl.start(
      () => ({ ticks: ticksRef.current, currentTick: curTickRef.current }),
      speed as Exclude<PlaybackSpeed, 'step'>,
      (next) => setCurrentTick(next),
      () => setPlaying(false),
    );

    return () => ctrl.stop();
  }, [isPlaying, speed]); // eslint-disable-line react-hooks/exhaustive-deps

  // ── HUD handlers ───────────────────────────────────────────────────────
  function handleStep() {
    const all = ticksRef.current;
    const cur = curTickRef.current;
    const idx = all.findIndex((t) => t.tick === cur);
    if (idx >= 0 && idx < all.length - 1) {
      setCurrentTick(all[idx + 1].tick);
    }
  }

  function handleSeek(tick: number) {
    setCurrentTick(tick);
    socketRef.current?.seek(tick);
  }

  function handleSpeed(s: PlaybackSpeed) {
    setSpeed(s);
    if (s === 'step') {
      setPlaying(false);
      socketRef.current?.setSpeed('step');
    } else {
      socketRef.current?.setSpeed(isPlaying ? s : 0);
    }
  }

  // ── Render ─────────────────────────────────────────────────────────────
  return (
    <Layout>
      {!matchId ? (
        <div style={{ color: '#64748b', textAlign: 'center', padding: '60px 0' }}>
          No match ID — navigate here from a match link.
        </div>
      ) : (
        <div>
          {wsError && (
            <div style={{ color: '#f87171', marginBottom: 10, fontSize: 13 }}>{wsError}</div>
          )}

          {/* Canvas host — always in DOM so Phaser can mount; hidden until snapshot */}
          <div style={{
            width: CANVAS, height: CANVAS,
            border: '1px solid #2d2d4e', borderRadius: 8,
            overflow: 'hidden',
            display: snapshot ? 'block' : 'none',
          }}>
            <div ref={hostRef} style={{ width: '100%', height: '100%' }} />
          </div>

          {/* Placeholder shown before snapshot arrives */}
          {!snapshot && !wsError && (
            <div style={{
              width: CANVAS, height: CANVAS,
              border: '1px solid #2d2d4e', borderRadius: 8,
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              color: '#64748b', fontSize: 14, background: '#0f0f1a',
            }}>
              Connecting to match…
            </div>
          )}

          {snapshot && (
            <div style={{ width: CANVAS, marginTop: 14 }}>
              <ObserverHUD
                snapshot={snapshot}
                ticks={ticks}
                currentTick={currentTick}
                isPlaying={isPlaying}
                speed={speed}
                matchOver={matchOver}
                onPlay={() => setPlaying(true)}
                onPause={() => setPlaying(false)}
                onStep={handleStep}
                onSeek={handleSeek}
                onSpeed={handleSpeed}
              />
            </div>
          )}
        </div>
      )}
    </Layout>
  );
}
