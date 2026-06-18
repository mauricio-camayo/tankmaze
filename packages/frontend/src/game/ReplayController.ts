import type { TickUpdate } from '../types';
import type { PlaybackSpeed } from '../store/matchStore';

// Base tick interval at 1× speed (milliseconds)
const BASE_MS = 500;

type StateGetter = () => { ticks: TickUpdate[]; currentTick: number };

export class ReplayController {
  private timer: ReturnType<typeof setInterval> | null = null;

  /**
   * Start advancing through the ticks array. Uses a getter to avoid stale
   * closures — caller should pass a function that reads from a React ref.
   */
  start(
    getState: StateGetter,
    speed: Exclude<PlaybackSpeed, 'step'>,
    onAdvance: (nextTick: number) => void,
    onEnd: () => void,
  ) {
    this.stop();
    const interval = Math.round(BASE_MS / speed);

    this.timer = setInterval(() => {
      const { ticks, currentTick } = getState();
      const idx = ticks.findIndex((t) => t.tick === currentTick);

      if (idx < 0) {
        // currentTick not found — snap to first tick to start replay
        if (ticks.length > 0) onAdvance(ticks[0].tick);
        return;
      }
      if (idx >= ticks.length - 1) {
        onEnd();
        return;
      }
      onAdvance(ticks[idx + 1].tick);
    }, interval);
  }

  stop() {
    if (this.timer !== null) {
      clearInterval(this.timer);
      this.timer = null;
    }
  }

  destroy() {
    this.stop();
  }
}
