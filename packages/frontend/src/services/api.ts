import { getIdToken } from './auth';
import type { Tank, TankVersion, Match, RankingEntry, GameDay, GameMap } from '../types';

const BASE = (import.meta.env.VITE_API_ENDPOINT as string) ?? '';

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const token = await getIdToken();
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(init.headers as Record<string, string>),
  };
  if (token) headers['Authorization'] = `Bearer ${token}`;

  const res = await fetch(`${BASE}${path}`, { ...init, headers });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`${res.status} ${res.statusText}: ${body}`);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

// Tanks
export const listTanks = () => request<Tank[]>('/tanks');
export const listAiTanks = () =>
  request<(Tank & { versions: TankVersion[] })[]>('/tanks/ai');
export const getTank = (id: string) =>
  request<Tank & { versions: TankVersion[] }>(`/tanks/${id}`);
export const createTank = (name: string) =>
  request<Tank>('/tanks', { method: 'POST', body: JSON.stringify({ name }) });
export const deleteTank = (id: string) =>
  request<void>(`/tanks/${id}`, { method: 'DELETE' });
export const forkTank = (tankId: string, version: string) =>
  request<Tank>(`/tanks?forkFrom=${tankId}&forkVersion=${encodeURIComponent(version)}`, {
    method: 'POST',
  });

// Versions
export const submitVersion = (tankId: string, source: string, config: object) =>
  request<TankVersion>(`/tanks/${tankId}/versions`, {
    method: 'POST',
    body: JSON.stringify({ source, config }),
  });
export const getVersionStatus = (tankId: string, version: string) =>
  request<Pick<TankVersion, 'compileStatus' | 'compileError'>>(
    `/tanks/${tankId}/versions/${encodeURIComponent(version)}/status`,
  );
export const promoteVersion = (tankId: string, version: string) =>
  request<TankVersion>(
    `/tanks/${tankId}/versions/${encodeURIComponent(version)}/promote`,
    { method: 'POST' },
  );
export const registerForGameDay = (tankId: string, version: string) =>
  request<void>(
    `/tanks/${tankId}/versions/${encodeURIComponent(version)}/register`,
    { method: 'POST' },
  );
export const withdrawRegistration = (tankId: string, version: string) =>
  request<void>(
    `/tanks/${tankId}/versions/${encodeURIComponent(version)}/register`,
    { method: 'DELETE' },
  );

// Score transfer
export const transferScore = (tankId: string, targetTankId: string) =>
  request<void>(`/tanks/${tankId}/score-transfer`, {
    method: 'POST',
    body: JSON.stringify({ targetTankId }),
  });

// Matches
export type OpponentSpec =
  | { type: 'ai'; name: string }
  | { type: 'own'; tankId: string; version: string };

export const startMatch = (
  tankId: string,
  version: string,
  opponent: OpponentSpec,
  mapId?: string,
) =>
  request<Match>('/matches', {
    method: 'POST',
    body: JSON.stringify({ tankId, version, opponent, ...(mapId ? { mapId } : {}) }),
  });
export const getMatch = (matchId: string) => request<Match>(`/matches/${matchId}`);
export const getMatchTicks = (matchId: string) =>
  request<unknown>(`/matches/${matchId}/ticks`);

// Rankings
export const getRankings = () => request<RankingEntry[]>('/rankings');

// GameDay
export const getGameDay = (gameDayId: string) =>
  request<GameDay>(`/gamedays/${gameDayId}`);

// Maps (no auth required for GET)
export const listMaps = () => request<GameMap[]>('/maps');
export const createMap = (
  map: Omit<GameMap, 'mapId' | 'createdAt' | 'isBuiltIn' | 'isActive'>,
) => request<GameMap>('/maps', { method: 'POST', body: JSON.stringify(map) });
export const updateMap = (
  mapId: string,
  updates: Partial<Pick<GameMap, 'name' | 'description' | 'isActive'>>,
) =>
  request<GameMap>(`/maps/${mapId}`, {
    method: 'PATCH',
    body: JSON.stringify(updates),
  });
