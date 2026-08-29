import { getIdToken } from './auth';
import type { Tank, TankVersion, Match, RankingEntry, GameDay, GameDaySeries, GameMap, UserSettings, PublicUserProfile, FriendsResponse, ChatMessage } from '../types';

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
  // Some 202 responses (e.g. forgotPassword) send no body at all — guard
  // against res.json() throwing on an empty string.
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
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
  | { type: 'own'; tankId: string; version: string }
  // Item 37: challenge another author's tank to an unranked match.
  | { type: 'informal'; tankId: string; version: string }
  // Item 37: re-run a previous ranked Game Day match, unranked.
  | { type: 'rematch'; matchId: string };

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
// Rematch (item 37) re-runs a previous ranked match between the same two
// tank/version pairs — both derived server-side from matchId, so there's no
// "own tank" to pass, unlike the other opponent types.
export const rematch = (matchId: string) =>
  request<Match>('/matches', { method: 'POST', body: JSON.stringify({ opponent: { type: 'rematch', matchId } }) });
export const getMatchTicks = (matchId: string) =>
  request<unknown>(`/matches/${matchId}/ticks`);
export const exportMatch = (matchId: string) =>
  request<{ url: string }>(`/matches/${matchId}/export`);

// Rankings
export const getRankings = () => request<RankingEntry[]>('/rankings');
export const getPublicUserProfile = (sub: string) => request<PublicUserProfile>(`/users/${sub}`);

// Auth — enumeration-safe forgot-password trigger (item 217). Always
// resolves once the backend acks 202; never reveals whether the email
// exists or which branch (native/IdP/unknown) the backend took.
export const requestPasswordReset = (email: string) =>
  request<void>('/auth/forgot-password', { method: 'POST', body: JSON.stringify({ email }) });

// Friends (item 223)
export const listFriends = () => request<FriendsResponse>('/friends');
export const sendFriendRequest = (toUserId: string) =>
  request<{ status: string }>('/friends/requests', { method: 'POST', body: JSON.stringify({ toUserId }) });
export const acceptFriendRequest = (fromUserId: string) =>
  request<{ status: string }>(`/friends/requests/${fromUserId}/accept`, { method: 'POST' });
export const rejectFriendRequest = (fromUserId: string) =>
  request<{ status: string }>(`/friends/requests/${fromUserId}/reject`, { method: 'POST' });
export const removeFriend = (friendId: string) =>
  request<{ status: string }>(`/friends/${friendId}`, { method: 'DELETE' });

// Block/unblock (item 226)
export const blockUser = (targetUserId: string) =>
  request<{ status: string }>('/friends/block', { method: 'POST', body: JSON.stringify({ targetUserId }) });
export const unblockUser = (targetUserId: string) =>
  request<{ status: string }>('/friends/unblock', { method: 'POST', body: JSON.stringify({ targetUserId }) });

// Chat (item 223 Part 2) — accepted friends only. Polling-based rather than
// WebSocket push: sendMessage/listMessages(since) is called on an interval
// by pages/Chat.tsx while a conversation is open.
export const sendMessage = (toUserId: string, body: string) =>
  request<ChatMessage>('/messages', { method: 'POST', body: JSON.stringify({ toUserId, body }) });
export const listMessages = (userId: string, since?: string) =>
  request<ChatMessage[]>(`/messages/${userId}${since ? `?since=${encodeURIComponent(since)}` : ''}`);

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

// Recurring Game Day series (item 238)
export const listGameDaySeries = () => request<GameDaySeries[]>('/gameday-series');
export const createGameDaySeries = (body: {
  name?: string;
  frequency: 'weekly' | 'monthly' | 'every_n_days';
  byMonthDay?: number;
  intervalDays?: number;
  registrationCloseAt: string;
  roundRobinAt: string;
  finalAt: string;
  autofill?: boolean;
  forcedMapIds?: string[];
  randomMaps?: boolean;
  maxOccurrences?: number;
}) =>
  request<{ series: GameDaySeries; firstOccurrence: GameDay }>('/gameday-series', {
    method: 'POST',
    body: JSON.stringify(body),
  });
export const cancelGameDaySeries = (seriesId: string) =>
  request<void>(`/gameday-series/${seriesId}`, { method: 'DELETE' });
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
  // rescheduleFailures (item 254): present only when the DB schedule update
  // succeeded but one or more phases' EventBridge triggers could not be
  // synced to match — the admin edit still "succeeded" but didn't fully
  // take effect. Names the affected phases so the caller can warn instead of
  // showing a plain success.
) => request<GameDay & { rescheduleFailures?: string[] }>(`/gamedays/${gameDayId}`, { method: 'PATCH', body: JSON.stringify(body) });
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
  idp: string;
  createdAt: string;
  lastLoginAt: number | null;
  tankCount: number;
  tankLimit: number;
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
// getMyProfile (item 225, avatar added item 229) returns the durable display
// name and avatar — unlike the ID token's given_name/picture claims, neither
// is reverted by a federated (Google/Facebook) re-login, so callers that
// need the caller's own name/picture (nav, /account) should prefer this over
// decoding the JWT. picture is "" when the user has never uploaded one —
// callers should fall back to the JWT's own picture claim in that case.
export const getMyProfile = () => request<{ name: string; picture?: string }>('/me/profile');
export const updateMyProfile = (name: string) =>
  request<{ name: string }>('/me/profile', { method: 'PATCH', body: JSON.stringify({ name }) });
export const uploadProfilePicture = (data: string, contentType: string) =>
  request<{ picture: string }>('/me/profile/picture', {
    method: 'PUT',
    body: JSON.stringify({ data, contentType }),
  });

export const adminListTanks = (nextToken?: string) =>
  request<{ tanks: Tank[]; nextToken?: string }>(
    `/admin/tanks${nextToken ? `?nextToken=${encodeURIComponent(nextToken)}` : ''}`,
  );
export const adminUpdateTank = (tankId: string, name: string) =>
  request<void>(`/admin/tanks/${tankId}`, { method: 'PATCH', body: JSON.stringify({ name }) });
export const adminDeleteTank = (tankId: string) =>
  request<void>(`/admin/tanks/${tankId}`, { method: 'DELETE' });
