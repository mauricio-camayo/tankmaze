import Phaser from 'phaser';
import { useEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams, useNavigate } from 'react-router-dom';
import Layout, { primaryButtonStyle } from '../components/Layout';
import { ObserverScene } from '../game/ObserverScene';
import ObserverHUD from '../game/ObserverHUD';
import { ReplayController } from '../game/ReplayController';
import { ObserverSocket } from '../services/ws';
import type { MatchOverStats } from '../services/ws';
import { exportMatch, getMatch, getTank, listTanks, rematch } from '../services/api';
import PostMatchSummary from '../components/PostMatchSummary';
import type { Match } from '../types';
import { useAuthStore } from '../store/authStore';
import { useMatchStore } from '../store/matchStore';
import type { PlaybackSpeed } from '../store/matchStore';

const CANVAS = 560;

export default function Watch() {
  const [searchParams] = useSearchParams();
  const matchId = searchParams.get('matchId');
  const navigate = useNavigate();
  const currentUser = useAuthStore((s) => s.user);

  // Rematch (item 37): fetched separately from the WS snapshot because the
  // button must only show for ranked matches, and only to the two
  // participating authors — the snapshot payload has neither matchType nor
  // ownership info, both of which the REST match record + the viewer's own
  // tank list can answer without exposing anything to spectators.
  const [restMatch, setRestMatch] = useState<Match | null>(null);
  const [ownTankIds, setOwnTankIds] = useState<Set<string>>(new Set());
  const [rematching, setRematching] = useState(false);
  const [rematchError, setRematchError] = useState<string | null>(null);

  // Match data export (item 35): generated on demand, so there's nothing to
  // fetch up front — exportGone only flips true after a real 410 from the
  // backend (source tick log expired off S3), at which point the button is
  // replaced rather than left to fail again on a retry.
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);
  const [exportGone, setExportGone] = useState(false);

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

  const [matchOver, setMatchOver] = useState<{ winner: 'a' | 'b' | null; reason: string; stats: MatchOverStats } | null>(null);
  const [authorNames, setAuthorNames] = useState<{ a?: string; b?: string }>({});
  const [wsError,   setWsError]   = useState<string | null>(null);
  const [sceneReady, setSceneReady] = useState(false);
  const [matchPending, setMatchPending] = useState(false);
  // Set to true when MATCH_OVER triggers auto-play but sceneReady is still false.
  // Cleared once sceneReady fires and setPlaying(true) is dispatched.
  const [pendingAutoPlay, setPendingAutoPlay] = useState(false);

  // §9.6: 'both' for test matches (one side is a builtin AI); null for ranked matches
  // where ownership requires a userId field not yet in the snapshot payload.
  const myTankSide = useMemo((): 'a' | 'b' | 'both' | null => {
    if (!snapshot) return null;
    const aIsAI = snapshot.tankA.tankId.startsWith('builtin-');
    const bIsAI = snapshot.tankB.tankId.startsWith('builtin-');
    if (aIsAI || bIsAI) return 'both';
    return null;
  }, [snapshot]);
  const matchPendingRef = useRef(false);
  const snapshotTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pollRef        = useRef<ReturnType<typeof setInterval> | null>(null);
  const autoPlayRef    = useRef(false);

  const setPending = (val: boolean) => {
    matchPendingRef.current = val;
    setMatchPending(val);
  };

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
      backgroundColor: '#072943',
      scene:  [ObserverScene],
      scale:  { mode: Phaser.Scale.NONE },
      banner: false,
    });
    gameRef.current = game;

    game.events.once('ready', () => {
      const scene = game.scene.getScene('ObserverScene') as ObserverScene;
      sceneRef.current = scene;
      // Wait for scene create() — which only fires after preload() finishes loading
      // all avatar textures. Listening here avoids the race where initMaze() is
      // called before mazeGfx/tankA/tankB are initialized (cold cache = first visit).
      scene.events.once('create', () => setSceneReady(true));
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
    setPending(false);
    setPendingAutoPlay(false);
    autoPlayRef.current = false;
    if (pollRef.current) { clearInterval(pollRef.current); pollRef.current = null; }

    const socket = new ObserverSocket();
    socketRef.current = socket;

    // Surface an error if no snapshot arrives within 15 seconds.
    snapshotTimeoutRef.current = setTimeout(() => {
      if (!useMatchStore.getState().snapshot) {
        setWsError('No response from match server — the match may not be active or may have already ended.');
      }
    }, 15000);

    socket.connect(matchId, (event) => {
      switch (event.type) {
        case 'MATCH_SNAPSHOT':
          if (snapshotTimeoutRef.current) {
            clearTimeout(snapshotTimeoutRef.current);
            snapshotTimeoutRef.current = null;
          }
          // Clear any prior pending-poll state from a previous observe
          if (pollRef.current) { clearInterval(pollRef.current); pollRef.current = null; }
          setSnapshot(event.payload);
          if (event.payload.status === 'active' || event.payload.status === 'scheduled') {
            setPlaying(true);
            // Show spinner immediately; start polling every 2 s until ticks arrive
            setPending(true);
            pollRef.current = setInterval(() => {
              socketRef.current?.reobserve();
            }, 2000);
          } else if (event.payload.status === 'ended') {
            // Arriving from a pending-poll re-observe: auto-play once ticks buffer
            if (matchPendingRef.current) {
              setPending(false);
              autoPlayRef.current = true;
            }
          }
          break;
        case 'TICK_UPDATE':
          applyTickUpdate(event.payload);
          // First tick arriving means match is live — cancel the pending poll
          if (pollRef.current) {
            clearInterval(pollRef.current);
            pollRef.current = null;
            setPending(false);
          }
          // For live matches, advance the display tick with the stream
          if (useMatchStore.getState().snapshot?.status === 'active') {
            setCurrentTick(event.payload.tick);
          }
          break;
        case 'HIT':
          sceneRef.current?.flashHit(event.payload.victim);
          break;
        case 'MATCH_OVER':
          setMatchOver({ winner: event.payload.winner, reason: event.payload.reason, stats: event.payload.stats });
          autoPlayRef.current = false;
          // Fire destroyed animation if render() missed the death tick (fast playback / tick multiplier)
          {
            const ts = useMatchStore.getState().ticks;
            const lastTick = ts[ts.length - 1];
            if (lastTick && sceneRef.current) {
              sceneRef.current.notifyMatchOver(lastTick.tankA, lastTick.tankB);
            }
          }
          if (useMatchStore.getState().snapshot?.status !== 'ended') {
            // Live match ended: stop immediately
            setPlaying(false);
          } else if (useMatchStore.getState().ticks.length > 0) {
            // Ended match with buffered ticks: defer auto-play until the Phaser scene
            // has finished its create() cycle (sceneReady). Calling setPlaying(true)
            // here races with preload() still loading avatar images — the render effect
            // fires before initMaze() is called, so tanks render with default 0,0 offsets.
            setPendingAutoPlay(true);
          }
          break;
        case 'ERROR':
          setWsError(event.payload.message);
          break;
      }
    });

    return () => {
      if (snapshotTimeoutRef.current) { clearTimeout(snapshotTimeoutRef.current); snapshotTimeoutRef.current = null; }
      if (pollRef.current) { clearInterval(pollRef.current); pollRef.current = null; }
      socket.disconnect();
      socketRef.current = null;
    };
  }, [matchId]); // eslint-disable-line react-hooks/exhaustive-deps

  // ── Apply snapshot to scene once both are ready ────────────────────────
  useEffect(() => {
    if (!snapshot || !sceneReady || !sceneRef.current) return;
    if (!snapshot.maze) {
      setWsError('Match data unavailable — try refreshing');
      return;
    }
    sceneRef.current.initMaze(snapshot.maze);
    sceneRef.current.setAvatarURLs(snapshot.tankA.avatarUrl, snapshot.tankB.avatarUrl, snapshot.tankA.tankId, snapshot.tankB.tankId);
    sceneRef.current.render(snapshot.tankA, snapshot.tankB, snapshot.projectiles, snapshot.tankA.tankId);
  }, [snapshot, sceneReady]);

  // ── Render scene whenever currentTick changes ──────────────────────────
  useEffect(() => {
    if (!snapshot || !sceneReady || !sceneRef.current) return;
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

  // ── Deferred auto-play: wait for scene ready ──────────────────────────
  // Fires when MATCH_OVER set pendingAutoPlay AND sceneReady becomes true.
  // Works regardless of which condition arrives first.
  useEffect(() => {
    if (!sceneReady || !pendingAutoPlay || ticksRef.current.length === 0) return;
    setPendingAutoPlay(false);
    setPlaying(true);
  }, [sceneReady, pendingAutoPlay]); // eslint-disable-line react-hooks/exhaustive-deps

  // ── Rematch (item 37) ─────────────────────────────────────────────────
  useEffect(() => {
    if (!matchId || !currentUser) return;
    getMatch(matchId).then(setRestMatch).catch(() => setRestMatch(null));
    listTanks().then((tanks) => setOwnTankIds(new Set(tanks.map((t) => t.tankId)))).catch(() => setOwnTankIds(new Set()));
    setExportGone(false);
    setExportError(null);
  }, [matchId, currentUser]);

  // ── Post-match summary (item 244): author names ────────────────────────
  // Best-effort only — getTank requires auth, so anonymous observers simply
  // see the summary without author attribution rather than losing the panel.
  useEffect(() => {
    setAuthorNames({});
    if (!snapshot || !currentUser) return;
    getTank(snapshot.tankA.tankId).then((t) => setAuthorNames((n) => ({ ...n, a: t.authorName }))).catch(() => {});
    getTank(snapshot.tankB.tankId).then((t) => setAuthorNames((n) => ({ ...n, b: t.authorName }))).catch(() => {});
  }, [snapshot?.tankA.tankId, snapshot?.tankB.tankId, currentUser]); // eslint-disable-line react-hooks/exhaustive-deps

  const canRematch = !!restMatch
    && restMatch.matchType === 'ranked'
    && (ownTankIds.has(restMatch.tankA.tankId) || ownTankIds.has(restMatch.tankB.tankId));

  async function handleRematch() {
    if (!matchId) return;
    setRematching(true);
    setRematchError(null);
    try {
      const match = await rematch(matchId);
      navigate(`/watch?matchId=${match.matchId}`);
    } catch (e) {
      setRematchError(e instanceof Error ? e.message : 'Failed to start rematch');
      setRematching(false);
    }
  }

  // §9.5: any participating Tank Author, on any match type — not gated to
  // ranked like rematch. tickLogS3Key must be present (match has ended and
  // match-runner has written the source tick log).
  const canExport = !!restMatch
    && !!restMatch.tickLogS3Key
    && !exportGone
    && (ownTankIds.has(restMatch.tankA.tankId) || ownTankIds.has(restMatch.tankB.tankId));

  async function handleExport() {
    if (!matchId) return;
    setExporting(true);
    setExportError(null);
    try {
      const { url } = await exportMatch(matchId);
      window.location.href = url;
    } catch (e) {
      const msg = e instanceof Error ? e.message : 'Export failed';
      if (msg.startsWith('410')) {
        setExportGone(true);
      } else {
        setExportError(msg);
      }
    } finally {
      setExporting(false);
    }
  }

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
    if (s === 'step') setPlaying(false);
    socketRef.current?.setSpeed(s);
  }

  // ── Render ─────────────────────────────────────────────────────────────
  return (
    <Layout>
      {!matchId ? (
        <div style={{ color: '#5b87a3', textAlign: 'center', padding: '60px 0' }}>
          No match ID — navigate here from a match link.
        </div>
      ) : (
        <div>
          {wsError && (
            <div style={{ color: '#ff8a75', marginBottom: 10, fontSize: 13 }}>{wsError}</div>
          )}

          {/* Canvas host — always in DOM so Phaser can mount; hidden until ready */}
          <div className="tm-canvas-wrap" style={{
            width: CANVAS, height: CANVAS,
            border: '1px solid #23577a', borderRadius: 0,
            overflow: 'hidden',
            display: snapshot && !matchPending ? 'block' : 'none',
          }}>
            <div ref={hostRef} style={{ width: '100%', height: '100%' }} />
          </div>

          {/* Placeholder: connecting or waiting for match to be processed */}
          {(!snapshot || matchPending) && !wsError && (
            <div className="tm-canvas-wrap" style={{
              width: CANVAS, height: CANVAS,
              border: '1px solid #23577a', borderRadius: 0,
              display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
              gap: 14, color: '#5b87a3', fontSize: 14, background: '#0a3550',
            }}>
              <div style={{
                width: 28, height: 28, border: '3px solid #23577a',
                borderTopColor: '#4fa8e0', borderRadius: '50%',
                animation: 'spin 0.9s linear infinite',
              }} />
              <span>{matchPending ? 'Match is being processed… replaying automatically when ready' : 'Connecting to match…'}</span>
              <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
            </div>
          )}

          {snapshot && (
            <div style={{ maxWidth: CANVAS, width: '100%', marginTop: 14 }}>
              <ObserverHUD
                snapshot={snapshot}
                ticks={ticks}
                currentTick={currentTick}
                isPlaying={isPlaying}
                speed={speed}
                matchOver={matchOver}
                myTankSide={myTankSide}
                onPlay={() => setPlaying(true)}
                onPause={() => setPlaying(false)}
                onStep={handleStep}
                onSeek={handleSeek}
                onSpeed={handleSpeed}
              />
              {matchOver && matchId && (
                <PostMatchSummary
                  matchId={matchId}
                  snapshot={snapshot}
                  matchOver={matchOver}
                  authorNames={authorNames}
                />
              )}
              {matchOver && canRematch && (
                <div style={{ marginTop: 14, display: 'flex', alignItems: 'center', gap: 10 }}>
                  <button onClick={handleRematch} disabled={rematching} style={primaryButtonStyle}>
                    {rematching ? 'Starting rematch…' : 'Rematch'}
                  </button>
                  {rematchError && <span style={{ color: '#ff8a75', fontSize: 13 }}>{rematchError}</span>}
                </div>
              )}
              {matchOver && canExport && (
                <div style={{ marginTop: 10, display: 'flex', alignItems: 'center', gap: 10 }}>
                  <button onClick={handleExport} disabled={exporting} style={primaryButtonStyle}>
                    {exporting ? 'Preparing download…' : 'Download match data (JSON)'}
                  </button>
                  {exportError && <span style={{ color: '#ff8a75', fontSize: 13 }}>{exportError}</span>}
                </div>
              )}
              {matchOver && exportGone && (
                <div style={{ marginTop: 10, color: '#5b87a3', fontSize: 13 }}>
                  Match data export is no longer available — it expires 7 days after the match.
                </div>
              )}
            </div>
          )}
        </div>
      )}
    </Layout>
  );
}
