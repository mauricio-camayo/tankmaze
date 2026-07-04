export type Direction = 'N' | 'S' | 'E' | 'W';

export interface Point {
  x: number;
  y: number;
}

export interface TankConfig {
  name: string;
  speed: number;
  sensorRange: number;
  damage: number;
  armor: number;
  fireRate: number;
}

export interface Tank {
  tankId: string;
  userId: string;
  name: string;
  authorName: string;
  globalScore: number;
  bestFinish: number | null;
  gameDaysCount: number;
  lastActiveAt: number | null;
  createdAt: number;
  forkedFromTankId: string | null;
  forkedFromVersion: string | null;
  scoreTransferredTo: string | null;
  scoreTransferredFrom: string | null;
  avatarUrl?: string;
}

export interface TankVersion {
  tankId: string;
  version: string;
  versionType: 'major' | 'minor';
  config: TankConfig;
  wasmS3Key: string | null;
  sourceS3Key: string;
  wasmSha256: string | null;
  compileStatus: 'pending' | 'compiling' | 'ready' | 'failed' | '';
  compileError: string | null;
  registeredForGameDays: string[] | null;
  createdAt: number;
  winRate: number | null;
  matchesPlayed: number | null;
  avgDamageDealt: number | null;
  avgSurvivalTicks: number | null;
  testMatchCount: number | null;
  disqualified: boolean;
}

export type MatchType = 'ranked' | 'test-ai' | 'test-own' | 'informal';
export type MatchStatus = 'scheduled' | 'countdown' | 'active' | 'ended';

export interface MatchResult {
  winner: 'a' | 'b' | null;
  reason: 'opponent_destroyed' | 'code_crash' | 'damage_tiebreak' | 'moves_tiebreak' | 'both_lose';
  damageA: number;
  damageB: number;
  movesA: number;
  movesB: number;
  ticksElapsed: number;
  flawless: boolean;
}

export interface Match {
  matchId: string;
  matchType: MatchType;
  gameDayId: string | null;
  status: MatchStatus;
  mazeSeed: string | null;
  mapId: string | null;
  tankA: { tankId: string; version: string };
  tankB: { tankId: string; version: string };
  tickLogS3Key: string | null;
  result: MatchResult | null;
  createdAt: number;
}

export interface RankingEntry {
  rank: number;
  tankId: string;
  tankName: string;
  authorUsername: string;
  activeVersion: string;
  avatarUrl?: string;
  globalScore: number;
  bestFinish: number | null;
  gameDays: number;
  lastActiveAt: number | null;
}

export interface GameDayPhaseStatus {
  status: 'upcoming' | 'running' | 'complete' | 'cancelled';
  startedAt: number | null;
  endedAt: number | null;
}

export interface BracketSlot {
  tankId: string | null;
  version: string | null;
  tankName?: string;
  status: 'playing' | 'won' | 'lost' | 'both_lose' | 'bye';
  matchId?: string;
}

export interface GroupStanding {
  tankId: string;
  version: string;
  tankName?: string;
  wins: number;
  losses: number;
  points: number;
}

export interface GroupMatchResult {
  tankAId: string;
  tankBId: string;
  matchId: string;
  winner: 'a' | 'b' | 'both_lose' | '';
}

export interface GameDayGroup {
  groupId: string;
  tanks: Array<{ tankId: string; version: string; tankName?: string }>;
  standings?: GroupStanding[];
  matchResults?: GroupMatchResult[];
}

export interface GameDay {
  gameDayId: string;
  name?: string;
  schedule: {
    registrationClose: string;
    roundRobin: string;
    elimination: string[];
    final: string;
  };
  phases: {
    roundRobin: GameDayPhaseStatus;
    elimination?: Record<string, GameDayPhaseStatus>;
    final: GameDayPhaseStatus;
  };
  registeredTanks?: Array<{ tankId: string; version: string; tankName?: string }>;
  groups?: GameDayGroup[];
  bracket: Record<string, BracketSlot[]>;
  placementPoints: Record<string, number>;
  createdAt: number;
  autofill?: boolean;
  forcedMapIds?: string[];
  randomMaps?: boolean;
}

export type SubscriptionTier = 'free' | 'builder' | 'pro';

export interface UserSettings {
  tier: SubscriptionTier;
  compilationsThisWindow: number;
  windowStart: string;
  tankLimit: number;
  compilationLimit: number;
}

export interface GameMap {
  mapId: string;
  slug: string;
  name: string;
  description: string;
  layout: boolean[][];
  isBuiltIn: boolean;
  isActive: boolean;
  createdAt: number;
}

export interface TankState {
  tankId: string;
  version: string;
  position: Point;
  facing: Direction;
  hp: number;
  config: TankConfig;
  avatarUrl?: string;
}

export interface Projectile {
  position: Point;
  direction: Direction;
  ownerTankId: string;
}

export interface TickUpdate {
  tick: number;
  tankA: TankState & {
    action?: { type: string; direction?: string };
    sensors?: Record<string, unknown>;
    log?: string[];
    durationMs?: number;
    violation?: boolean;
  };
  tankB: TankState & {
    action?: { type: string; direction?: string };
    sensors?: Record<string, unknown>;
    log?: string[];
    durationMs?: number;
    violation?: boolean;
  };
  projectiles: Projectile[];
}

export interface MatchSnapshot {
  matchId: string;
  status: MatchStatus;
  maze: boolean[][] | null;
  tankA: TankState;
  tankB: TankState;
  projectiles: Projectile[];
  tick: number;
  totalTicks?: number;
}
