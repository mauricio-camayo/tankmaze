import { getIdToken } from './auth';
import type { Tank, TankVersion, Match, RankingEntry, GameDay, GameMap, UserSettings } from '../types';

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
export const listTanks = (userId?: string) =>
  request<Tank[]>(userId ? `/tanks?userId=${encodeURIComponent(userId)}` : '/tanks');
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
export const updateTank = (tankId: string, updates: { name?: string; avatarUrl?: string }) =>
  request<{ name: string }>(`/tanks/${tankId}`, {
    method: 'PATCH',
    body: JSON.stringify(updates),
  });
export const uploadTankAvatar = (tankId: string, data: string, contentType: string) =>
  request<{ avatarUrl: string }>(`/tanks/${tankId}/avatar`, {
    method: 'PUT',
    body: JSON.stringify({ data, contentType }),
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
export const getVersionSource = (tankId: string, version: string) =>
  request<{ source: string }>(
    `/tanks/${tankId}/versions/${encodeURIComponent(version)}/source`,
  );
export const promoteVersion = (tankId: string, version: string) =>
  request<TankVersion>(
    `/tanks/${tankId}/versions/${encodeURIComponent(version)}/promote`,
    { method: 'POST' },
  );
export const registerForGameDay = (tankId: string, version: string, gameDayId: string) =>
  request<void>(
    `/tanks/${tankId}/versions/${encodeURIComponent(version)}/register`,
    { method: 'POST', body: JSON.stringify({ gameDayId }) },
  );
export const withdrawRegistration = (tankId: string, version: string, gameDayId: string) =>
  request<void>(
    `/tanks/${tankId}/versions/${encodeURIComponent(version)}/register`,
    { method: 'DELETE', body: JSON.stringify({ gameDayId }) },
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
export const listGameDays = () => request<GameDay[]>('/gamedays');
export const getGameDay = (gameDayId: string) =>
  request<GameDay>(`/gamedays/${gameDayId}`);
export const createGameDay = (body: {
  name?: string;
  registrationCloseAt: string;
  roundRobinAt: string;
  finalAt: string;
  autofill?: boolean;
  forcedMapIds?: string[];
  randomMaps?: boolean;
}) => request<GameDay>('/gamedays', { method: 'POST', body: JSON.stringify(body) });
export const deleteGameDay = (gameDayId: string, force?: boolean) =>
  request<void>(`/gamedays/${gameDayId}${force ? '?force=true' : ''}`, { method: 'DELETE' });
export const patchGameDay = (
  gameDayId: string,
  body: {
    name?: string;
    registrationCloseAt?: string;
    roundRobinAt?: string;
    finalAt?: string;
    autofill?: boolean;
    forcedMapIds?: string[];
    randomMaps?: boolean;
  },
) => request<GameDay>(`/gamedays/${gameDayId}`, { method: 'PATCH', body: JSON.stringify(body) });
export const overrideGameDayPhase = (
  gameDayId: string,
  phaseOverride: Record<string, 'upcoming' | 'running' | 'complete' | 'cancelled'>,
) =>
  request<GameDay>(`/gamedays/${gameDayId}?force=true`, {
    method: 'PATCH',
    body: JSON.stringify({ phaseOverride }),
  });
export const addRosterEntry = (gameDayId: string, tankId: string, version: string) =>
  request<void>(`/gamedays/${gameDayId}/roster`, {
    method: 'POST',
    body: JSON.stringify({ tankId, version }),
  });
export const removeRosterEntry = (gameDayId: string, tankId: string) =>
  request<void>(`/gamedays/${gameDayId}/roster/${tankId}`, { method: 'DELETE' });

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

// Admin
export interface AdminUser {
  sub: string;
  email: string;
  name: string;
  enabled: boolean;
  isAdmin: boolean;
  tier: string;
}

export const adminListUsers = (nextToken?: string) =>
  request<{ users: AdminUser[]; nextToken?: string }>(
    `/admin/users${nextToken ? `?nextToken=${encodeURIComponent(nextToken)}` : ''}`,
  );
export const adminUpdateUser = (sub: string, disabled: boolean) =>
  request<void>(`/admin/users/${sub}`, { method: 'PATCH', body: JSON.stringify({ disabled }) });
export const adminToggleUserRole = (sub: string) =>
  request<{ isAdmin: boolean }>(`/admin/users/${sub}/role`, { method: 'PATCH' });
export const adminDeleteUser = (sub: string) =>
  request<void>(`/admin/users/${sub}`, { method: 'DELETE' });

// Ad config
export interface AdConfigBody {
  publisherId?: string;
  topSlotId?: string;
  rightSlotId?: string;
  bottomSlotId?: string;
  enabled?: boolean;
}
export const adminGetAdConfig = () =>
  request<AdConfigBody & { enabled: boolean }>('/admin/config/ads');
export const adminUpdateAdConfig = (body: AdConfigBody) =>
  request<void>('/admin/config/ads', { method: 'PATCH', body: JSON.stringify(body) });

// User settings / subscription
export const getMySettings = () => request<UserSettings>('/me/settings');
export const adminSetUserTier = (userId: string, tier: string) =>
  request<{ tier: string }>(`/me/settings?userId=${encodeURIComponent(userId)}`, {
    method: 'PATCH',
    body: JSON.stringify({ tier }),
  });

// User profile
export const updateMyProfile = (name: string) =>
  request<{ name: string }>('/me/profile', { method: 'PATCH', body: JSON.stringify({ name }) });

export const adminListTanks = (nextToken?: string) =>
  request<{ tanks: Tank[]; nextToken?: string }>(
    `/admin/tanks${nextToken ? `?nextToken=${encodeURIComponent(nextToken)}` : ''}`,
  );
export const adminUpdateTank = (tankId: string, name: string) =>
  request<void>(`/admin/tanks/${tankId}`, { method: 'PATCH', body: JSON.stringify({ name }) });
export const adminDeleteTank = (tankId: string) =>
  request<void>(`/admin/tanks/${tankId}`, { method: 'DELETE' });
