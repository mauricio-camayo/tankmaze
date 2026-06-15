import { create } from 'zustand';

interface GameDayStore {
  activeGameDayLabel: string | null;
  setActiveGameDayLabel: (label: string | null) => void;
}

export const useGameDayStore = create<GameDayStore>((set) => ({
  activeGameDayLabel: null,
  setActiveGameDayLabel: (label) => set({ activeGameDayLabel: label }),
}));
