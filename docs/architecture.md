# TankMaze — Architecture Decision Record

## ADR-001: Serverless over dedicated game server

**Decision:** Use Lambda + API Gateway WebSocket instead of a persistent EC2/ECS game server.

**Rationale:** At the scale of this game (max ~100 concurrent sessions, 2 players each), a serverless approach eliminates idle server costs and ops overhead. The game tick is low-frequency (10 TPS), well within Lambda's cold-start and execution time bounds. If tick frequency needed to go above 50 TPS, we would revisit with ECS Fargate.

---

## ADR-002: Discrete grid movement over free movement

**Decision:** Tank positions are cell-aligned; movement is one-cell-at-a-time.

**Rationale:** Simplifies collision detection, sensor ray-casting, and state serialization to DynamoDB. Free movement would require continuous physics simulation incompatible with serverless tick architecture.

---

## ADR-003: Server-authoritative game state

**Decision:** All game logic (collision, damage, sensors) runs server-side in Lambda.

**Rationale:** Prevents client-side cheating (e.g., reporting fake sensor values or skipping cooldowns). The client only sends intent (`MOVE`, `FIRE`) and renders server-confirmed state.

---

## ADR-004: Phaser 3 for rendering, React for UI

**Decision:** Use Phaser 3 for the Canvas game view and React for HUD/overlays.

**Rationale:** Phaser handles the game loop, sprite rendering, and animation efficiently. React handles the DOM-based UI (health bars, session ID display, lobby). They communicate via a Zustand store and a custom event emitter.

---

## ADR-005: Maze sent only to observers, not players

**Decision:** The maze grid is never transmitted to player clients; only sensor readings are sent.

**Rationale:** If players received the full maze, the client-side fog-of-war would be trivially bypassed by inspecting WebSocket traffic. The observer receives the full map explicitly as part of their role.

---

## ADR-006: pnpm monorepo

**Decision:** Single repo with pnpm workspaces for frontend, backend, shared, and infrastructure packages.

**Rationale:** Shared TypeScript types (PlayerState, MazeGrid, WebSocket messages) live in `packages/shared` and are imported by both frontend and backend without duplication or publishing to npm. CDK infrastructure references the same types.

---

## Game Tick Flow (sequence)

```
EventBridge Scheduler (every 100ms)
  │
  └─► game-tick Lambda
        │
        ├─ Load session from DynamoDB
        ├─ Advance projectiles one cell
        ├─ Check wall collisions → destroy projectile
        ├─ Check tank collisions → apply damage
        ├─ Check win condition
        ├─ Persist updated session
        └─ Broadcast to all connections in session
              ├─ GAME_STATE → observers (full snapshot)
              ├─ HIT → all (if damage occurred)
              └─ GAME_OVER → all (if win condition met)
```

## Player Action Flow (sequence)

```
Browser (Player)
  │  MOVE { direction: 'FORWARD' }
  ▼
API Gateway WebSocket
  │
  └─► wss-handler Lambda ($default route)
        │
        └─► game-action Lambda
              │
              ├─ Load session
              ├─ Validate: game ACTIVE? cooldown elapsed? wall ahead?
              │     └─ REJECTED → send MOVE_REJECTED to player
              ├─ Update player position in session
              ├─ Compute sensor readings for player
              ├─ Persist session
              └─ Broadcast
                    ├─ MOVE_ACCEPTED + SENSOR_UPDATE → acting player
                    └─ GAME_STATE → observers
```

## Observer Join Flow

```
Browser (Observer) → GET /sessions/{id}  →  session-manager Lambda
                       ← { sessionId, status, playerCount }

Browser → WebSocket connect (no JWT)
  │  OBSERVE { sessionId }
  ▼
wss-handler Lambda
  ├─ Store connection (role: OBSERVER)
  ├─ Load full session state
  └─ Send GAME_STATE snapshot → observer
```
