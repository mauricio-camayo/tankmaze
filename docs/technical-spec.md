# TankMaze — Technical Specification

## 1. Technology Stack

| Layer | Technology | Rationale |
|---|---|---|
| Frontend | React 18 + TypeScript | Component model; strong typing |
| Game Rendering | Phaser 3 (Canvas) | Purpose-built 2D game framework; WebSocket-friendly |
| Auth Client | AWS Amplify v6 | First-class Cognito integration |
| Monorepo | pnpm workspaces | Shared types across packages; fast installs |
| Backend Runtime | Node.js 20 + TypeScript | Lambda-native; shares types with frontend |
| Real-time Transport | API Gateway WebSocket API | Fully managed; scales to concurrent sessions |
| Game State Store | DynamoDB | Single-digit ms latency; TTL for session cleanup |
| Auth Provider | AWS Cognito | Managed user pool; JWT tokens; social login ready |
| Static Hosting | S3 + CloudFront | CDN-distributed; HTTPS by default |
| Infrastructure as Code | AWS CDK v2 (TypeScript) | Type-safe; same language as the rest of the stack |
| CI/CD | GitHub Actions | Native GitHub integration |

---

## 2. Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                        Browser                              │
│  ┌──────────────┐  WebSocket  ┌────────────────────────┐   │
│  │  React + Phaser│◄──────────►│ API Gateway (WS API)  │   │
│  └──────┬───────┘             └──────────┬─────────────┘   │
│         │ HTTPS (REST)                   │ Lambda invoke    │
│         ▼                                ▼                  │
│  ┌──────────────┐          ┌────────────────────────────┐  │
│  │   Cognito    │          │      Lambda Functions       │  │
│  │  (auth)      │          │  ┌─────────────────────┐   │  │
│  └──────────────┘          │  │ connect / disconnect │   │  │
│                            │  │ game-action          │   │  │
│                            │  │ game-tick (scheduled)│   │  │
│                            │  │ create-session       │   │  │
│                            │  │ join-session         │   │  │
│                            │  └──────────┬──────────┘   │  │
│                            └─────────────┼──────────────┘  │
│                                          │                  │
│                                          ▼                  │
│                            ┌────────────────────────────┐  │
│                            │         DynamoDB            │  │
│                            │  Sessions | Connections     │  │
│                            │  GameState | PlayerStats    │  │
│                            └────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

---

## 3. DynamoDB Table Design

### 3.1 `tankmaze-sessions`

| Attribute | Type | Description |
|---|---|---|
| `sessionId` (PK) | String | UUID v4 |
| `status` | String | `LOBBY \| SELECTING \| ACTIVE \| ENDED` |
| `maze` | String | JSON-serialized maze grid |
| `players` | Map | `{ p1: PlayerState, p2: PlayerState }` |
| `projectiles` | List | Active projectile objects |
| `tickCount` | Number | Game tick counter |
| `createdAt` | Number | Unix timestamp |
| `ttl` | Number | Auto-expire 2h after creation |

### 3.2 `tankmaze-connections`

| Attribute | Type | Description |
|---|---|---|
| `connectionId` (PK) | String | API Gateway connection ID |
| `sessionId` | String | GSI key — maps connection to session |
| `role` | String | `PLAYER1 \| PLAYER2 \| OBSERVER` |
| `userId` | String | Cognito sub (null for observers) |
| `ttl` | Number | Auto-expire 2h |

GSI: `sessionId-index` on `sessionId` for broadcasting to all session connections.

### 3.3 PlayerState (embedded in session)

```typescript
interface PlayerState {
  archetype: 'SCOUT' | 'RANGER' | 'BRUISER';
  position: { x: number; y: number };   // cell coordinates
  facing: 'N' | 'S' | 'E' | 'W';
  hp: number;
  moveCooldownUntil: number;             // epoch ms
  fireCooldownUntil: number;             // epoch ms
}
```

---

## 4. WebSocket Message Protocol

All messages are JSON. Direction: C = Client → Server, S = Server → Client.

### 4.1 Client → Server Actions

| Action | Payload | Description |
|---|---|---|
| `MOVE` | `{ direction: 'FORWARD'\|'BACKWARD'\|'LEFT'\|'RIGHT' }` | Move or rotate tank |
| `FIRE` | `{}` | Fire projectile |
| `SELECT_ARCHETYPE` | `{ archetype: string }` | Choose tank type during selection phase |
| `READY` | `{}` | Signal ready in lobby |
| `OBSERVE` | `{ sessionId: string }` | Join as observer |

### 4.2 Server → Client Events

| Event | Recipient | Payload |
|---|---|---|
| `SESSION_CREATED` | P1 | `{ sessionId }` |
| `PLAYER_JOINED` | P1, P2 | `{ role, archetype? }` |
| `GAME_START` | All | `{ countdownMs: 3000 }` |
| `SENSOR_UPDATE` | Player only | `{ wallDistances, proximityAlert, bearing, hp, cooldowns }` |
| `GAME_STATE` | Observer | Full `{ maze, players, projectiles }` snapshot |
| `MOVE_ACCEPTED` | Player | `{ newPosition, newFacing }` |
| `MOVE_REJECTED` | Player | `{ reason }` |
| `HIT` | All | `{ victim, damageDone, remainingHp }` |
| `GAME_OVER` | All | `{ winner, stats }` |
| `ERROR` | Sender | `{ code, message }` |

---

## 5. Lambda Functions

### 5.1 `wss-handler` — WebSocket lifecycle

Handles `$connect`, `$disconnect`, `$default` routes from API Gateway.

- `$connect`: Validates JWT (Cognito token in query string); stores connection in `tankmaze-connections`.
- `$disconnect`: Removes connection; triggers disconnect-timeout logic for active games.
- `$default`: Routes to action handlers by `action` field.

### 5.2 `game-action` — Player input processing

Invoked synchronously from `$default` for `MOVE` and `FIRE` actions.

Sequence:
1. Load session from DynamoDB.
2. Validate action legality (cooldown, game state, wall collision).
3. Apply state mutation.
4. Run sensor computation for the acting player.
5. Persist new session state.
6. Broadcast appropriate events to connections in session.

### 5.3 `game-tick` — Projectile physics

Triggered by **EventBridge Scheduler** every **100 ms** per active session.

- Advances all projectiles one cell.
- Detects wall hits (destroy projectile).
- Detects tank hits (apply damage, check win condition).
- Broadcasts `HIT` and updated `GAME_STATE` (to observers).
- Terminates when session status = `ENDED`.

### 5.4 `session-manager`

REST endpoint (HTTP API):
- `POST /sessions` — Create session; generate maze; return `sessionId`.
- `GET /sessions/{id}` — Public session metadata (for observer join page).

### 5.5 `maze-generator`

Internal library (not a standalone Lambda). Exports:
```typescript
function generateMaze(width: number, height: number, seed?: string): MazeGrid
```
Algorithm: Recursive Backtracking (DFS). Returns a 2D boolean array (`true` = wall).

---

## 6. Sensor Computation

Run server-side inside `game-action` after every accepted player move.

```
wallDistances = {
  N: raycast(position, 'N', sensorRange),
  S: raycast(position, 'S', sensorRange),
  E: raycast(position, 'E', sensorRange),
  W: raycast(position, 'W', sensorRange),
}
```

`raycast(origin, direction, maxRange)` walks cells in direction until hitting a wall or reaching `maxRange`. Returns distance in cells (capped at `maxRange` if no wall found — meaning open corridor).

Proximity alert: `euclideanDistance(myPos, opponentPos) ≤ sensorRange`.
Bearing: 8-direction compass from `myPos` to `opponentPos` (only sent if proximity alert = true).

---

## 7. Frontend Architecture

```
packages/frontend/src/
├── App.tsx                  # Routing: /lobby, /game, /observe
├── scenes/
│   ├── LobbyScene.ts        # Phaser scene: waiting room
│   ├── SelectionScene.ts    # Tank archetype picker
│   ├── GameScene.ts         # Main game (player view — fog of war)
│   └── ObserverScene.ts     # Full-map observer view
├── components/
│   ├── HUD.tsx              # React overlay: HP, cooldowns, session ID
│   └── GameOver.tsx         # End screen
├── services/
│   ├── ws.ts                # WebSocket client singleton
│   └── auth.ts              # Amplify/Cognito helpers
├── store/
│   └── gameStore.ts         # Zustand state (sensor data, game events)
└── shared/                  # Symlinked from packages/shared
```

Phaser scenes handle the canvas rendering. React handles all UI overlays (HUD, modals). They communicate via a shared Zustand store and a custom event bus.

---

## 8. Infrastructure (CDK Stacks)

### Stack: `AuthStack`
- Cognito UserPool + UserPoolClient
- Email verification enabled
- Exports: `userPoolId`, `userPoolClientId`

### Stack: `StorageStack`
- DynamoDB tables: `tankmaze-sessions`, `tankmaze-connections`
- TTL attributes configured on both
- Point-in-time recovery enabled

### Stack: `ApiStack`
- API Gateway WebSocket API + routes
- HTTP API for REST endpoints
- Lambda functions (with DynamoDB + API Gateway invoke permissions)
- EventBridge Scheduler rule for game-tick

### Stack: `FrontendStack`
- S3 bucket (private) + CloudFront distribution
- OAC (Origin Access Control) for S3
- Route53 A record (if domain configured)

---

## 9. Security

- JWT verification on `$connect` (RS256, Cognito JWKS endpoint)
- Observers exempt from JWT; session ID validated server-side
- DynamoDB access via IAM roles (no hardcoded credentials)
- CloudFront enforces HTTPS; S3 bucket not public
- CORS: API Gateway restricted to CloudFront domain
- Input validation on all WebSocket action payloads (Zod schemas)
- Rate limiting: API Gateway throttling (50 req/s per connection)

---

## 10. CI/CD Pipeline (GitHub Actions)

```
on: push (main), pull_request

Jobs:
  build-and-test
    - Install pnpm
    - pnpm install
    - pnpm -r typecheck
    - pnpm -r test
    - pnpm -r build

  cdk-diff (PRs only)
    - AWS credentials via OIDC
    - cdk diff

  deploy (main only)
    - cdk deploy --all
    - pnpm frontend build
    - aws s3 sync dist/ s3://<bucket>
    - CloudFront invalidation
```

---

## 11. Environment Configuration

| Variable | Where | Description |
|---|---|---|
| `COGNITO_USER_POOL_ID` | Lambda env | Cognito pool ID |
| `COGNITO_CLIENT_ID` | Lambda env | Cognito app client |
| `SESSIONS_TABLE` | Lambda env | DynamoDB sessions table name |
| `CONNECTIONS_TABLE` | Lambda env | DynamoDB connections table name |
| `APIGW_ENDPOINT` | Lambda env | WS API management endpoint for broadcasting |
| `VITE_USER_POOL_ID` | Frontend build | Amplify config |
| `VITE_USER_POOL_CLIENT_ID` | Frontend build | Amplify config |
| `VITE_WS_ENDPOINT` | Frontend build | WebSocket API URL |

---

## 12. Project Structure

```
tankmaze/
├── packages/
│   ├── frontend/            # React + Phaser game client (Vite)
│   ├── backend/             # Lambda handlers + game logic
│   ├── shared/              # Types, constants, maze generator
│   └── infrastructure/      # AWS CDK stacks
├── docs/
│   ├── functional-spec.md
│   ├── technical-spec.md
│   └── architecture.md
├── .github/
│   └── workflows/
│       └── ci.yml
├── package.json             # Root (pnpm workspace)
├── pnpm-workspace.yaml
└── README.md
```

---

## 13. Non-Functional Requirements

| Requirement | Target |
|---|---|
| WebSocket message latency | < 100 ms (same region) |
| Game tick interval | 100 ms (10 TPS) |
| Max concurrent sessions | 100 (soft limit; adjustable) |
| Session auto-expiry | 2 hours (DynamoDB TTL) |
| Frontend load time | < 3 s (CloudFront cached) |
| Availability | 99.9% (serverless SLA) |
| Browser support | Chrome, Firefox, Safari (last 2 versions) |
