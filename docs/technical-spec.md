# TankMaze — Technical Specification

## 1. Technology Stack

| Layer | Technology | Rationale |
|---|---|---|
| Frontend | React 18 + TypeScript + Phaser 3 | Component model; Canvas rendering; strong typing |
| Code Editor | Monaco Editor (Go syntax) | VS Code engine; Go language support built-in |
| Auth Client | AWS Amplify v6 | First-class Cognito integration |
| Backend Language | Go 1.22 | Lambda cold start ~100 ms; compiled; low memory; native concurrency |
| Tank Language | Go → WebAssembly (`GOOS=wasip1 GOARCH=wasm`) | User code sandboxed by WASM isolation |
| WASM Runtime | Wazero (pure-Go) | No CGO; runs in Lambda; deterministic fuel-based time limit |
| Tank Compilation | AWS CodeBuild | Isolated build environment with Go toolchain; avoids Lambda timeout constraints |
| WASM Artifact Store | S3 | Durable; versioned; loaded into Lambda /tmp per match |
| Real-time Transport | API Gateway WebSocket API | Fully managed; observer-only connections |
| Game State Store | DynamoDB | Single-digit ms latency; TTL for cleanup |
| Match Tick Log Store | S3 | Full tick-by-tick JSON per match; replay source |
| Auth Provider | AWS Cognito | Managed user pool; JWT tokens |
| Static Hosting | S3 + CloudFront | CDN-distributed; HTTPS by default |
| Infrastructure as Code | AWS CDK v2 (TypeScript) | Mature; broad L2 construct coverage |
| CI/CD | GitHub Actions | Native GitHub integration; OIDC AWS auth |

---

## 2. Architecture Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│                             Browser                                  │
│  ┌────────────────────┐   WebSocket   ┌──────────────────────────┐  │
│  │ React + Phaser     │◄─────────────►│ API Gateway (WS API)     │  │
│  │ Monaco Editor      │               └────────────┬─────────────┘  │
│  └────────┬───────────┘                            │ observer only  │
│           │ HTTPS (REST)                            ▼                │
│           ▼                            ┌──────────────────────────┐  │
│  ┌────────────────────┐               │   wss-handler Lambda     │  │
│  │   Cognito (auth)   │               └──────────────────────────┘  │
│  └────────────────────┘                                             │
└──────────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────────┐
│                          AWS Backend                                 │
│                                                                      │
│  REST (HTTP API)          Lambda Functions                           │
│  ┌────────────┐    ┌──────────────────────────────────────────┐     │
│  │ tank-api   │    │ match-runner  (one per active match)      │     │
│  │ (CRUD,     │    │  ├─ loads WASM from S3 via Wazero         │     │
│  │  history,  │    │  ├─ runs game loop (≤100 ticks, 100ms ea) │     │
│  │  replays)  │    │  ├─ broadcasts state to observers         │     │
│  └─────┬──────┘    │  └─ writes tick log to S3                │     │
│        │           └──────────────┬───────────────────────────┘     │
│        │                          │                                  │
│  ┌─────▼──────────────────────────▼──────────────────────────┐      │
│  │                         DynamoDB                           │      │
│  │  tanks | tank-versions | matches | connections | gamedays  │      │
│  │  rankings | maps                                           │      │
│  └────────────────────────────────────────────────────────────┘      │
│                                                                      │
│  ┌─────────────────────┐    ┌─────────────────────────────────┐     │
│  │  S3: wasm-artifacts │    │  S3: match-logs                 │     │
│  │  (compiled tanks)   │    │  (tick-by-tick JSON per match)  │     │
│  └─────────────────────┘    └─────────────────────────────────┘     │
│                                                                      │
│  EventBridge Scheduler (one rule per Game Day phase)                 │
│  ┌──────────────────────────────────────────────────────────┐       │
│  │ tournament-scheduler Lambda                               │       │
│  │  ├─ registration_close → lock registrations              │       │
│  │  ├─ round_robin        → pot seeding → spawn match-runner │       │
│  │  ├─ elimination_rN     → bracket update → spawn matches   │       │
│  │  └─ final              → champion → ranking-updater       │       │
│  └──────────────────────────────────────────────────────────┘       │
│                                                                      │
│  CodeBuild Project: tank-compiler                                    │
│  ┌──────────────────────────────────────────────────────────┐       │
│  │  go build -o tank.wasm (GOOS=wasip1 GOARCH=wasm)         │       │
│  │  → upload to S3 wasm-artifacts → update tank-versions    │       │
│  └──────────────────────────────────────────────────────────┘       │
└──────────────────────────────────────────────────────────────────────┘
```

---

## 3. DynamoDB Table Design

### 3.1 `tankmaze-tanks`

Tank-level identity and aggregated stats. Never reset by version promotions.

| Attribute | Type | Description |
|---|---|---|
| `tankId` (PK) | String | UUID v4 |
| `userId` | String | Cognito sub of owner |
| `name` | String | Display name (from latest config) |
| `globalScore` | Number | Sum of valid placement points (§6.7) |
| `bestFinish` | Number | Best placement ever (1 = champion) |
| `gameDaysCount` | Number | Game Days participated in (within validity window) |
| `lastActiveAt` | Number | Unix timestamp of last Game Day match |
| `createdAt` | Number | Unix timestamp of first major version promotion |
| `forkedFromTankId` | String | tankId of the source tank if this tank was created by forking (null otherwise) |
| `forkedFromVersion` | String | Version of the source tank used as the fork origin (e.g. `"v2.1"`) |
| `scoreTransferredTo` | String | tankId that received this tank's score via a score transfer (null if not transferred) |
| `scoreTransferredFrom` | String | tankId this tank received its score from via a score transfer (null if not received) |

GSI: `userId-index` on `userId` — list all tanks for a user.

### 3.2 `tankmaze-tank-versions`

One record per version (major and minor). WASM artifact lives in S3; this table holds the reference and stats.

| Attribute | Type | Description |
|---|---|---|
| `tankId` (PK) | String | Foreign key → `tankmaze-tanks` |
| `version` (SK) | String | `"v0.1"`, `"v1"`, `"v2.3"`, etc. |
| `versionType` | String | `"major"` or `"minor"` |
| `config` | Map | Stat allocation (`speed`, `sensorRange`, `damage`, `armor`, `fireRate`) |
| `wasmS3Key` | String | S3 key of compiled WASM binary (null if compiling or failed) |
| `sourceS3Key` | String | S3 key of submitted Go source |
| `wasmSha256` | String | SHA-256 of WASM binary (integrity check before match execution) |
| `compileStatus` | String | `"pending"` \| `"compiling"` \| `"ready"` \| `"failed"` |
| `compileError` | String | Compiler output if status = `"failed"` |
| `registeredForGameDay` | String | gameDayId if registered, null otherwise |
| `createdAt` | Number | Unix timestamp |
| `winRate` | Number | (major only) wins ÷ ranked matches |
| `matchesPlayed` | Number | (major only) total Game Day matches |
| `avgDamageDealt` | Number | (major only) average damage per ranked match |
| `avgSurvivalTicks` | Number | (major only) average ticks survived per ranked match |
| `testMatchCount` | Number | (minor only) test matches run |
| `disqualified` | Boolean | True if >20% tick violations across any match |

GSI: `userId-version-index` on `userId` (projected from tanks table join) for dashboard queries.

### 3.3 `tankmaze-matches`

One record per match of any type.

| Attribute | Type | Description |
|---|---|---|
| `matchId` (PK) | String | UUID v4 |
| `matchType` | String | `"ranked"` \| `"test-ai"` \| `"test-own"` \| `"informal"` |
| `gameDayId` | String | Game Day reference (null for non-ranked) |
| `status` | String | `"scheduled"` \| `"countdown"` \| `"active"` \| `"ended"` |
| `mazeSeed` | String | Seed used for random maze generation — `null` when `mapId` is set |
| `mapId` | String | Static map used for this match (FK → `tankmaze-maps`) — `null` when `mazeSeed` is set; mutually exclusive with `mazeSeed` |
| `tankA` | Map | `{ tankId, version }` |
| `tankB` | Map | `{ tankId, version }` |
| `tickLogS3Key` | String | S3 key of full tick log (written on match end) |
| `result` | Map | `{ winner, reason, damageA, damageB, movesA, movesB, ticksElapsed, flawless }` — `winner` is `null` and `reason` is `"both_lose"` when §10.4 rule 5 applies |
| `createdAt` | Number | Unix timestamp |
| `ttl` | Number | Auto-expire active-only fields 2h after creation; completed matches kept indefinitely |

GSI: `gameDayId-index` on `gameDayId` — retrieve all matches for a Game Day.  
GSI: `tankId-index` (on both `tankA.tankId` and `tankB.tankId`, via sparse index) — match history per tank.

### 3.4 `tankmaze-connections`

Observer WebSocket connections only. Tanks are autonomous — no player connections exist.

| Attribute | Type | Description |
|---|---|---|
| `connectionId` (PK) | String | API Gateway connection ID |
| `matchId` | String | Match being observed |
| `ttl` | Number | Auto-expire 2h |

GSI: `matchId-index` on `matchId` — broadcast to all observers of a match.

### 3.5 `tankmaze-gamedays`

One record per scheduled Game Day tournament.

| Attribute | Type | Description |
|---|---|---|
| `gameDayId` (PK) | String | UUID v4 |
| `schedule` | Map | Cron expressions per phase (see §6.1) |
| `phases` | Map | Per-phase status: `{ roundRobin, elimR1, …, final }` each with `{ status, startedAt, endedAt }` |
| `registeredTanks` | List | `[{ tankId, version }]` — locked at `registration_close` |
| `groups` | List | Round-robin group assignments post-seeding |
| `bracket` | Map | Elimination bracket state (seeds, match results per round). Each slot carries `{ tankId, version, status }` where `status` is `"playing"` \| `"won"` \| `"lost"` \| `"both_lose"` \| `"bye"`. A `"bye"` slot is created automatically when the opposing slot resolves to `"both_lose"` |
| `placementPoints` | Map | `{ tankId: points }` — final placement points awarded |
| `createdAt` | Number | Unix timestamp |

### 3.6 `tankmaze-maps`

One record per static map. Platform-seeded built-in maps and any maps added by the administrator. Not used for ranked matches (those always use randomly generated mazes).

| Attribute | Type | Description |
|---|---|---|
| `mapId` (PK) | String | UUID v4 |
| `slug` | String | URL-safe identifier (e.g., `"open"`, `"donut"`, `"x"`, `"rooms"`, `"double-spiral"`) |
| `name` | String | Display name shown in the map picker |
| `description` | String | Short description shown in the map picker |
| `layout` | List | N×N boolean matrix — outer list = rows, inner list = cells; `true` = open/passable, `false` = wall. N is the dimension the map was created with; static maps carry their own size independently of `MAZE_SIZE` |
| `isBuiltIn` | Boolean | `true` for platform-seeded maps; built-in maps cannot be deleted |
| `isActive` | Boolean | `false` hides the map from the picker without deleting it |
| `createdAt` | Number | Unix timestamp |

GSI: `slug-index` on `slug` — look up a map by slug for API and match-runner use.

**Notes:**
- The `layout` matrix must be N×N (square). Spawn points sit at (1,1) and (N-2,N-2) — N must be odd and ≥ 5 for these to be valid room cells. Both spawns must be open and reachable from the rest of the open cells.
- Built-in maps are seeded during CDK deployment via a one-time Lambda custom resource; the `isBuiltIn` flag prevents accidental deletion.
- The `layout` field is small (N² booleans); no external storage is needed. At the default size of 25 this is 625 booleans ≈ 1 KB.

### 3.7 `tankmaze-rankings`

One record per (tank, game-day) pair. Used to compute rolling Global Score.

| Attribute | Type | Description |
|---|---|---|
| `tankId` (PK) | String | Foreign key → `tankmaze-tanks` |
| `gameDayId` (SK) | String | Foreign key → `tankmaze-gamedays` |
| `points` | Number | Placement points awarded |
| `placement` | Number | Final placement (1 = champion) |
| `expiresAt` | Number | Unix timestamp when points become invalid |
| `ttl` | Number | DynamoDB auto-expire (same value as `expiresAt`) |

---

## 4. Tank Execution Model

### 4.1 Compilation Pipeline

```
[Author saves Go source]
        │
        ▼
[tank-api Lambda]
  ├─ Static validation (stat sum, Tick signature, import allowlist)
  ├─ Upload source to S3 (wasm-artifacts/<tankId>/<version>/source.go)
  ├─ Write tank-versions record (status: "pending")
  └─ Trigger CodeBuild: tank-compiler project
        │
        ▼
[CodeBuild: tank-compiler]
  ├─ Download source from S3
  ├─ go build -o tank.wasm (GOOS=wasip1 GOARCH=wasm)
  │     └─ SDK package (tankmaze) provided as CodeBuild module cache
  ├─ On success: upload tank.wasm to S3, compute SHA-256
  │              update tank-versions (status: "ready", wasmS3Key, wasmSha256)
  └─ On failure: update tank-versions (status: "failed", compileError)
```

Typical CodeBuild compile time: 15–30 s. The editor polls `GET /tanks/{id}/versions/{version}/status` until `ready` or `failed`.

### 4.2 Match Execution (match-runner Lambda)

Each match runs inside a single Lambda invocation. The Lambda handles the complete game loop — no per-tick scheduling is needed because 100 ticks × 100 ms = 10 s, well within Lambda's 15-minute limit.

```
[match-runner invoked with matchId]
  │
  ├─ Load tank WASM binaries from S3 into /tmp (verify SHA-256)
  ├─ Instantiate two Wazero runtime instances (one per tank)
  ├─ Load maze:
  │     if match.mapId is set  → fetch layout from tankmaze-maps by mapId
  │     if match.mazeSeed set  → generate maze via recursive backtracking from seed
  ├─ Initialize match state (positions, HP, tick = 0)
  │
  └─ Game loop (up to tickLimit iterations):
        ├─ Call tankA.Tick(sensors)  — Wazero fuel-limited (50 ms)
        ├─ Call tankB.Tick(sensors)  — Wazero fuel-limited (50 ms)
        ├─ Process actions (move, rotate, fire, scan, idle)
        ├─ Advance projectiles
        ├─ Check collisions and damage
        ├─ Check win condition → break if met
        ├─ Append tick record to in-memory log
        ├─ Broadcast GAME_STATE to observers via API Gateway Management API
        └─ Sleep remainder of 100 ms tick budget

  ├─ Write full tick log to S3 (match-logs/<matchId>/ticks.json.gz)
  ├─ Persist result to tankmaze-matches
  ├─ Update tank-versions stats (winRate, matchesPlayed, etc.)
  └─ If ranked: invoke ranking-updater Lambda (async)
```

### 4.3 WASM Sandbox Guarantees

Wazero is configured with:
- **No WASI filesystem mounts** — tank code cannot read or write files
- **No network host functions** — no socket access
- **Fuel limit per Tick() call** — maps to ~50 ms CPU equivalent; exhausted fuel returns `Idle`
- **Linear memory cap** — 4 MB per module instance
- **No host function imports** — only the `tankmaze` SDK host functions are registered (`sensors_get`, `log_write`)

---

## 5. Lambda Functions

### 5.1 `wss-handler`

Handles observer WebSocket lifecycle (`$connect`, `$disconnect`, `$default`).

- `$connect`: validates `matchId` query param; stores connection in `tankmaze-connections`.
- `$disconnect`: removes connection record.
- `$default`: routes `OBSERVE` action; sends current match snapshot to new observer.

### 5.2 `match-runner`

Single-invocation match executor (see §4.2). Invoked by `tournament-scheduler` for ranked matches and by `tank-api` for test matches.

### 5.3 `tournament-scheduler`

Triggered by EventBridge Scheduler rules — one rule per Game Day phase. Phase logic:

| Trigger | Action |
|---|---|
| `registration_close` | Lock `registeredTanks` list; compute pot seeding by Global Rank |
| `round_robin` | Assign groups; invoke `match-runner` for all group matches (parallel) |
| `elimination_rN` | Read round N results; update bracket; resolve byes; invoke `match-runner` for next-round pairs |
| `final` | Run final match (or award bye-champion if slot is empty); invoke `ranking-updater` |

**Both-lose bracket resolution** (runs after each elimination match completes):
1. If `result.reason == "both_lose"`: mark both bracket slots as `"both_lose"`.
2. Find the opposing bracket slot that would have faced one of the eliminated tanks in the next round.
3. Mark that opposing slot as `"bye"` — its occupant advances without playing.
4. If the `"bye"` slot itself is also `"both_lose"` (cascade), walk up the bracket until a live tank or the root is reached; if no live tanks remain in that half, the other bracket half's survivor is champion.
5. If all remaining slots are `"both_lose"` with no survivors anywhere, set `gameDayId.champion = null` and skip 1st/2nd placement point awards.

### 5.4 `ranking-updater`

Invoked async after each Game Day completes. Computes placements, awards points per §6.6 formula, writes to `tankmaze-rankings`, and recalculates `globalScore` on all participating `tankmaze-tanks` records.

**Score transfer logic** (invoked as part of `POST /tanks/{id}/score-transfer`):
1. Validate source tank has `globalScore > 0` and target tank belongs to the same user.
2. Validate target tank has no existing `tankmaze-rankings` records (score lineages cannot be merged).
3. Atomically (DynamoDB transaction):
   - Copy all `tankmaze-rankings` records from source `tankId` to target `tankId` (same `gameDayId`, `points`, `placement`, `expiresAt`, `ttl`).
   - Set source tank `globalScore = 0`, `bestFinish = null`, `gameDaysCount = 0`, `scoreTransferredTo = targetTankId`.
   - Set target tank `globalScore`, `bestFinish`, `gameDaysCount` from source values; set `scoreTransferredFrom = sourceTankId`.
4. Delete source `tankmaze-rankings` records after successful copy.

**No-champion edge case:** if `gameDay.champion == null` (all elimination matches produced both-lose with no survivors), skip 1st and 2nd placement point awards. All other placements (3rd onwards) are awarded normally based on the round each tank reached.

### 5.5 `tank-api`

HTTP API (REST). All endpoints require Cognito JWT except replay reads.

| Method | Path | Description |
|---|---|---|
| Method | Path | Description |
|---|---|---|
| `POST` | `/tanks` | Create new tank |
| `POST` | `/tanks?forkFrom={tankId}&forkVersion={v}` | Create new tank pre-loaded with source from an existing version |
| `GET` | `/tanks` | List authenticated user's tanks |
| `GET` | `/tanks/{id}` | Tank detail + version history |
| `POST` | `/tanks/{id}/versions` | Submit Go source → triggers CodeBuild |
| `GET` | `/tanks/{id}/versions/{v}/status` | Poll compile status |
| `POST` | `/tanks/{id}/versions/{v}/promote` | Promote minor → next major |
| `POST` | `/tanks/{id}/versions/{v}/register` | Register major for next Game Day |
| `DELETE` | `/tanks/{id}/versions/{v}/register` | Withdraw Game Day registration |
| `POST` | `/tanks/{id}/score-transfer` | Transfer Global Score + ranking history to another tank — body: `{ targetTankId }` |
| `POST` | `/matches` | Start test match (vs AI, vs own tank) — body: `{ tankId, version, opponent, mapId? }` — omit `mapId` for random map |
| `GET` | `/matches/{id}` | Match metadata + result |
| `GET` | `/matches/{id}/ticks` | Full tick log (streams from S3) |
| `GET` | `/rankings` | Global leaderboard |
| `GET` | `/gamedays/{id}` | Game Day bracket and phase status |
| `GET` | `/maps` | List all active static maps (slug, name, description, layout) — no auth required |
| `POST` | `/maps` | Create a new static map — admin only (Cognito group `platform-admin`); body: `{ slug, name, description, layout }` |
| `PATCH` | `/maps/{id}` | Update `name`, `description`, or `isActive` — admin only; `slug` and `layout` are immutable after creation |

---

## 6. Sensor Computation

Run inside `match-runner` after processing each tank's action. Written in Go.

```go
func raycast(maze MazeGrid, origin Point, dir Direction, maxRange int) int {
    for i := 1; i <= maxRange; i++ {
        next := origin.Step(dir, i)
        if maze.IsWall(next) {
            return i - 1
        }
    }
    return maxRange // open corridor up to sensor limit
}

func computeSensors(state MatchState, tankID string) Sensors {
    t := state.Tanks[tankID]
    opp := state.Opponent(tankID)
    dist := euclidean(t.Position, opp.Position)
    return Sensors{
        Facing:          t.Facing,
        Position:        t.Position,
        HP:              t.HP,
        WallDistances:   map[Direction]int{N: raycast(...N), S: ..., E: ..., W: ...},
        ProximityAlert:  dist <= float64(t.SensorRange*2),
        OpponentBearing: bearingIfInRange(t, opp, dist),
        MoveCooldown:    msUntil(t.MoveCooldownUntil),
        FireCooldown:    msUntil(t.FireCooldownUntil),
        Tick:            state.Tick,
    }
}
```

---

## 7. Tick Log Format

Written to S3 as gzip-compressed JSON after each match. Used for replay and data export.

```json
{
  "matchId": "...",
  "mazeSeed": "...",
  "maze": [[true, false, ...], ...],
  "tanks": {
    "a": { "tankId": "...", "version": "v2", "config": { "speed": 3, ... } },
    "b": { "tankId": "...", "version": "v1", "config": { "speed": 5, ... } }
  },
  "ticks": [
    {
      "tick": 0,
      "a": {
        "sensors":    { "facing": "N", "hp": 100, "wallDistances": {...}, ... },
        "action":     { "type": "Move", "direction": "Forward" },
        "durationMs": 12,
        "violation":  false,
        "log":        ["initializing..."]
      },
      "b": { ... }
    }
  ],
  "result": {
    "winner": "a",           // "a" | "b" | null (null when reason = "both_lose")
    "reason": "opponent_destroyed", // "opponent_destroyed" | "code_crash" | "damage_tiebreak"
                                    // | "moves_tiebreak" | "both_lose"
    "damageA": 0,
    "damageB": 60,
    "movesA": 14,
    "movesB": 9,
    "ticksElapsed": 43,
    "flawless": true         // true only when winner destroyed opponent with zero damage received
  }
}
```

---

## 8. WebSocket Protocol (Observer Only)

Tanks are autonomous — no client-to-server actions exist for controlling tanks. WebSocket connections are observer-only.

### 8.1 Client → Server

| Action | Payload | Description |
|---|---|---|
| `OBSERVE` | `{ matchId }` | Request to observe a match (live or triggers replay stream) |
| `REPLAY_SEEK` | `{ tick }` | Jump to a specific tick in replay mode |
| `REPLAY_SPEED` | `{ multiplier }` | Set replay speed (0.25, 0.5, 1, 2, 4, 8, or `"step"`) |

### 8.2 Server → Client

| Event | Payload | Description |
|---|---|---|
| `MATCH_SNAPSHOT` | Full match state | Sent immediately on observer join |
| `TICK_UPDATE` | `{ tick, tankA, tankB, projectiles }` | State update each tick (live) or streamed (replay) |
| `HIT` | `{ victim, damage, remainingHP }` | Damage event |
| `MATCH_OVER` | `{ winner, reason, stats }` | Match ended — `winner` is `null` when `reason` is `"both_lose"`; downstream bracket gives a bye to next opponent |
| `ERROR` | `{ code, message }` | Connection or request error |

---

## 9. Frontend Architecture

```
packages/frontend/src/
├── App.tsx                      # Routing
├── pages/
│   ├── Dashboard.tsx            # Tank list, global rank, Game Day schedule
│   ├── TankEditor.tsx           # Monaco editor + validate/save/promote flow
│   ├── TankDetail.tsx           # Version history, per-version stats
│   ├── Watch.tsx                # Live match + replay viewer
│   ├── Leaderboard.tsx          # Global ranking table
│   └── GameDay.tsx              # Bracket viewer, phase status (bye slots shown for both-lose outcomes)
├── components/
│   ├── ForkDialog.tsx           # Shown on fork creation: "Keep score on source" vs "Transfer score"
│   └── ScoreTransferConfirm.tsx # Standalone confirmation modal — irreversibility warning
├── game/
│   ├── scenes/
│   │   ├── MatchScene.ts        # Phaser: maze + tank rendering (observer view)
│   │   └── ObserverHUD.tsx      # React overlay: HP bars, tick counter, speed controls
│   └── replay/
│       ├── ReplayController.ts  # Tick seek, speed, step logic
│       └── DebugPanel.tsx       # Per-tank sensor/memory/log viewer
├── services/
│   ├── ws.ts                    # WebSocket client (observer)
│   ├── api.ts                   # REST client (tank-api)
│   └── auth.ts                  # Amplify/Cognito helpers
└── store/
    └── matchStore.ts            # Zustand: live tick state, replay position
```

---

## 10. Infrastructure (CDK Stacks)

### `AuthStack`
- Cognito UserPool + UserPoolClient
- Email verification; social login ready
- Exports: `userPoolId`, `userPoolClientId`

### `StorageStack`
- DynamoDB tables: all seven tables in §3, TTL and PITR enabled
- S3 bucket: `wasm-artifacts` (versioned; lifecycle rule deletes minor WASM after 90 days)
- S3 bucket: `match-logs` (versioned; lifecycle rule transitions to Glacier after 1 year)
- Lambda custom resource: seeds built-in static maps into `tankmaze-maps` on first deploy (idempotent — skips records where `isBuiltIn = true` already exists)

### `BuildStack`
- CodeBuild project: `tank-compiler`
  - Managed image with Go 1.22
  - VPC isolated; no internet egress after Go module cache is seeded
  - Artifacts → `wasm-artifacts` S3 bucket
  - Buildspec: `go build -o tank.wasm`, SHA-256 computation, S3 upload, DynamoDB update

### `ApiStack`
- API Gateway WebSocket API + routes (`$connect`, `$disconnect`, `$default`)
- HTTP API with Cognito JWT authorizer
- Lambda functions: `wss-handler`, `match-runner`, `tank-api`, `tournament-scheduler`, `ranking-updater`
- EventBridge Scheduler group: one schedule per Game Day phase (created dynamically when a Game Day is configured)
- IAM roles: least-privilege per Lambda

### `FrontendStack`
- S3 bucket (private) + CloudFront distribution with OAC
- Route53 A record (if domain configured)

---

## 11. Security

| Concern | Control |
|---|---|
| Tank code isolation | WASM sandbox; no FS/network/syscall access; Wazero host functions restricted to SDK only |
| WASM integrity | SHA-256 stored at compile time; verified by `match-runner` before instantiation |
| Compilation isolation | CodeBuild VPC with no internet egress; Go module proxy seeded in advance |
| Auth | Cognito JWT (RS256) on all REST and WebSocket endpoints; observers exempt |
| Tank source privacy | S3 bucket policy: owner only; opponent's source never accessible via API |
| Replay privacy | Opponent `memory` and `log` fields stripped from API responses for non-owners |
| DynamoDB access | IAM roles per Lambda; no wildcard resource permissions |
| HTTPS | CloudFront enforces; S3 bucket not public; API Gateway TLS only |
| Input validation | All REST payloads validated with Go struct tags + explicit range checks |
| Rate limiting | API Gateway throttling: 50 req/s per connection; CodeBuild: 1 concurrent build per user |

---

## 12. CI/CD Pipeline (GitHub Actions)

```
on: push (main), pull_request

jobs:
  build-and-test:
    - go build ./...
    - go test ./...
    - go vet ./...
    - golangci-lint run
    - cd packages/frontend && pnpm install && pnpm build

  cdk-diff:          # PRs only
    - AWS OIDC credentials
    - cdk diff --all

  deploy:            # main branch only
    - AWS OIDC credentials
    - cdk deploy --all --require-approval never
    - cd packages/frontend && pnpm build
    - aws s3 sync dist/ s3://$FRONTEND_BUCKET --delete
    - aws cloudfront create-invalidation --paths "/*"
```

---

## 13. Environment Configuration

| Variable | Where | Description |
|---|---|---|
| `COGNITO_USER_POOL_ID` | Lambda env | Cognito pool ID |
| `COGNITO_CLIENT_ID` | Lambda env | Cognito app client |
| `TANKS_TABLE` | Lambda env | DynamoDB tanks table |
| `TANK_VERSIONS_TABLE` | Lambda env | DynamoDB tank-versions table |
| `MATCHES_TABLE` | Lambda env | DynamoDB matches table |
| `CONNECTIONS_TABLE` | Lambda env | DynamoDB connections table |
| `GAMEDAYS_TABLE` | Lambda env | DynamoDB gamedays table |
| `RANKINGS_TABLE` | Lambda env | DynamoDB rankings table |
| `WASM_BUCKET` | Lambda env | S3 bucket for WASM artifacts |
| `MATCH_LOGS_BUCKET` | Lambda env | S3 bucket for tick logs |
| `APIGW_ENDPOINT` | Lambda env | WS API management endpoint for broadcasting |
| `CODEBUILD_PROJECT` | Lambda env | tank-compiler CodeBuild project name |
| `MAPS_TABLE` | Lambda env | DynamoDB maps table |
| `MAZE_SIZE` | Lambda env | Dimension of randomly generated mazes (default: `25`; must be an odd integer ≥ 5; does not affect static maps loaded from `tankmaze-maps`) |
| `TICK_LIMIT` | Lambda env | Max ticks per match (default: `100`) |
| `POINTS_VALIDITY_DAYS` | Lambda env | Ranking point validity window (default: `365`) |
| `VITE_USER_POOL_ID` | Frontend build | Amplify config |
| `VITE_USER_POOL_CLIENT_ID` | Frontend build | Amplify config |
| `VITE_WS_ENDPOINT` | Frontend build | WebSocket API URL |
| `VITE_API_ENDPOINT` | Frontend build | REST API URL |

---

## 14. Project Structure

```
tankmaze/
├── packages/
│   ├── frontend/                # React + Phaser game client (Vite + TypeScript)
│   │   └── src/
│   ├── backend/                 # Go Lambda functions
│   │   ├── cmd/
│   │   │   ├── wss-handler/
│   │   │   ├── match-runner/
│   │   │   ├── tank-api/
│   │   │   ├── tournament-scheduler/
│   │   │   └── ranking-updater/
│   │   └── internal/
│   │       ├── engine/          # Game loop, collision, sensor computation
│   │       ├── maze/            # Maze generation (recursive backtracking)
│   │       ├── wasm/            # Wazero host functions, WASM loader
│   │       └── db/              # DynamoDB access layer
│   ├── sdk/                     # Tank author SDK (Go module: github.com/tankmaze/sdk)
│   │   └── types.go             # Sensors, Action, TankConfig, Direction constants
│   └── infrastructure/          # AWS CDK stacks (TypeScript)
│       └── lib/
│           ├── auth-stack.ts
│           ├── storage-stack.ts
│           ├── build-stack.ts
│           ├── api-stack.ts
│           └── frontend-stack.ts
├── docs/
│   ├── functional-spec.md
│   ├── technical-spec.md
│   └── architecture.md
├── .github/workflows/
│   └── ci.yml
├── go.work                      # Go workspace (backend + sdk)
└── README.md
```

---

## 15. Non-Functional Requirements

| Requirement | Target |
|---|---|
| WebSocket broadcast latency | < 100 ms per tick (same region) |
| Game tick interval | 100 ms (10 TPS) |
| Tick() execution budget | 50 ms per tank per tick (Wazero fuel limit) |
| Max match duration | 10 s (100 ticks × 100 ms) |
| Tank compilation time | < 60 s (CodeBuild cold); < 20 s (warm module cache) |
| Max concurrent matches | 100 (Lambda concurrency limit; adjustable) |
| Match record retention | Indefinite (S3 + DynamoDB; until account deletion) |
| WASM minor version retention | 90 days (S3 lifecycle) |
| Frontend load time | < 3 s (CloudFront cached) |
| Availability | 99.9% (serverless SLA) |
| Browser support | Chrome, Firefox, Safari (last 2 versions) |
