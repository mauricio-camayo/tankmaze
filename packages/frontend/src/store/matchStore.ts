import { create } from 'zustand';
import type { MatchSnapshot, TickUpdate } from '../types';

export type PlaybackSpeed = 0.25 | 0.5 | 1 | 2 | 4 | 8 | 'step';

interface MatchStore {
  snapshot: MatchSnapshot | null;
  currentTick: number;
  ticks: TickUpdate[];
  isPlaying: boolean;
  speed: PlaybackSpeed;

  setSnapshot: (s: MatchSnapshot) => void;
  applyTickUpdate: (t: TickUpdate) => void;
  setCurrentTick: (tick: number) => void;
  setPlaying: (playing: boolean) => void;
  setSpeed: (speed: PlaybackSpeed) => void;
  reset: () => void;
}

export const useMatchStore = create<MatchStore>((set) => ({
  snapshot: null,
  currentTick: 0,
  ticks: [],
  isPlaying: false,
  speed: 1,

  setSnapshot: (snapshot) => set({ snapshot, currentTick: snapshot.tick, ticks: [] }),
  applyTickUpdate: (t) =>
    set((s) => ({ ticks: [...s.ticks, t], currentTick: t.tick })),
  setCurrentTick: (tick) => set({ currentTick: tick }),
  setPlaying: (isPlaying) => set({ isPlaying }),
  setSpeed: (speed) => set({ speed }),
  reset: () =>
    set({ snapshot: null, currentTick: 0, ticks: [], isPlaying: false, speed: 1 }),
}));
