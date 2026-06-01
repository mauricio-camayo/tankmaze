# TankMaze — Architecture Decision Records

## ADR-001: Serverless over dedicated game server

**Decision:** Use Lambda + API Gateway WebSocket instead of a persistent EC2/ECS game server.

**Rationale:** Tanks are autonomous and matches last at most 10 seconds (100 ticks × 100 ms). A single Lambda invocation runs the full game loop from start to finish with no idle time between matches. At the target scale of ~100 concurrent matches, serverless eliminates idle server costs entirely. If matches were longer or tick rates higher (>50 TPS), ECS Fargate would be the right revisit point.

---

## ADR-002: Discrete grid movement over free movement

**Decision:** Tank positions are cell-aligned; movement is one cell at a time per tick.

**Rationale:** Simplifies collision detection (integer coordinate comparison), sensor ray-casting (integer DFS), and state serialization to DynamoDB. Free movement would require continuous physics simulation — incompatible with a WASM-sandboxed decision model where each `Tick()` call returns a discrete action.

---

## ADR-003: Tank code runs server-side, never on the client

**Decision:** Tank `Tick()` functions execute inside Wazero (server-side WASM runtime) within the `match-runner` Lambda. The compiled WASM binary is never sent to the browser.

**Rationale:** Sending the WASM to the browser would expose the tank's strategy to the opponent (inspect the binary, reverse-engineer the algorithm). Running server-side also enforces the sandbox guarantees uniformly — no reliance on browser security boundaries.

---

## ADR-004: Go for all backend Lambda functions

**Decision:** All Lambda functions are written in Go. The frontend remains TypeScript/React.

**Rationale:** Go cold starts (~100 ms) are 3–5× faster than Node.js for Lambda. The game engine (sensor computation, maze generation, collision detection) is CPU-bound — Go's compiled performance is a meaningful advantage. Wazero, the chosen WASM runtime, is a pure-Go library, so the entire backend is a single language without CGO. The frontend stays TypeScript because it shares no runtime with the backend and benefits from the React/Phaser ecosystem.

---

## ADR-005: Go → WebAssembly for tank code, executed by Wazero

**Decision:** User tank programs are written in Go and compiled to WASM (`GOOS=wasip1 GOARCH=wasm`). The `match-runner` Lambda executes them via Wazero.

**Rationale:** WASM provides the strongest sandboxing model available — no filesystem, no network, no syscalls by design. Wazero enforces a fuel-based CPU limit per `Tick()` call (equivalent to ~50 ms), making timeouts deterministic rather than relying on OS-level process signals. Pure-Go Wazero requires no CGO, which means the Lambda binary is a standard static binary with no native dependencies. Choosing Go as the tank language aligns it with the backend — only one Go SDK package needs to be maintained, and users writing tanks benefit from the same toolchain as the platform.

---

## ADR-006: CodeBuild for tank compilation, not Lambda

**Decision:** Tank source code is compiled to WASM by an AWS CodeBuild project, not inside a Lambda function.

**Rationale:** Running `go build` inside Lambda would require embedding the Go toolchain (~500 MB) in a Lambda layer and risks hitting the 15-minute timeout on slow builds or cold module fetches. CodeBuild provides a purpose-built, isolated build environment with Go pre-installed, configurable compute size, VPC isolation with no internet egress (after the Go module proxy is seeded), and no time pressure. It also produces a clean audit trail of every build. Typical compile time is 15–30 s — acceptable for an async workflow where the editor polls for status.

---

## ADR-007: Single-invocation match-runner (no per-tick scheduling)

**Decision:** Each match runs inside a single Lambda invocation that executes the full game loop internally, sleeping between ticks. EventBridge is not used for per-tick scheduling.

**Rationale:** EventBridge Scheduler has a minimum resolution of 1 minute — far too coarse for a 100 ms tick. Alternatives (Step Functions, recursive Lambda) add complexity and latency. Since a match lasts at most 10 seconds, a single Lambda invocation running a `time.Sleep`-based loop is the simplest correct solution and fits well within Lambda's 15-minute limit. The WASM modules for both tanks stay loaded in memory for the full match, avoiding repeated S3 fetches and Wazero instantiation overhead.

---

## ADR-008: Package-level Go variables as tank memory

**Decision:** Tanks retain state across ticks via Go package-level variables, not via an explicit `memory` parameter passed to `Tick()`.

**Rationale:** The WASM module for each tank is instantiated once per match and stays resident. Go's package-level state persists naturally across calls into the module — this is idiomatic Go. Passing an explicit `memory` map would require serializing and deserializing it on every tick call across the host/WASM boundary, adding overhead and complexity. Package-level variables are simpler, faster, and more natural for Go authors.

---

## ADR-009: Maze grid sent only to observers, never to tank code

**Decision:** The maze grid is never passed to `Tick()`. Tank code can only learn about the maze through sensor readings (wall distances, proximity).

**Rationale:** If the full maze were available to `Tick()`, it would negate the sensor-range stat entirely — every tank would have perfect information regardless of stat allocation. The fog-of-war is a core game mechanic, not just a rendering effect. Enforcing it at the data boundary (the Wazero host function interface) makes it structurally impossible to bypass, unlike a client-side rendering approach.

---

## ADR-010: Six DynamoDB tables with explicit separation of concerns

**Decision:** Use six tables (`tanks`, `tank-versions`, `matches`, `connections`, `gamedays`, `rankings`) rather than a smaller number of multipurpose tables.

**Rationale:** Each table models a distinct entity with a different lifecycle and access pattern. Tank-level stats (`globalScore`, `bestFinish`) are updated after every Game Day regardless of version. Version-level stats (`winRate`, `matchesPlayed`) reset on promotion. Ranking records need TTL-based expiry independent of the tank record. Conflating these into fewer tables would require complex conditional updates and make GSI design awkward. DynamoDB costs per table are negligible; clarity is worth it.

---

## ADR-011: Rolling ranking via DynamoDB TTL on ranking records

**Decision:** Placement points expire by setting a `ttl` attribute on `tankmaze-rankings` records. Global Score is recomputed from surviving records.

**Rationale:** DynamoDB TTL provides automatic, zero-cost record expiry without a scheduled cleanup job. When a ranking record expires, the next read of a tank's global score simply sums fewer records. This makes the validity window (default: 1 year) a configuration parameter (`POINTS_VALIDITY_DAYS`) with no infrastructure changes required.

---

## Sequence Diagrams

### Tank Submission & Compilation

```
Browser (Author)
  │  POST /tanks/{id}/versions  { source: "package tank..." }
  ▼
tank-api Lambda
  ├─ Static validation (stat sum, Tick signature, import allowlist)
  ├─ Upload source → S3 wasm-artifacts/<tankId>/<version>/source.go
  ├─ Write tankmaze-tank-versions (status: "pending")
  ├─ Trigger CodeBuild: tank-compiler project
  └─ Return 202 Accepted { version: "v0.3", status: "compiling" }

[async — typically 15–30 s]

CodeBuild: tank-compiler
  ├─ Download source.go from S3
  ├─ go build -o tank.wasm (GOOS=wasip1 GOARCH=wasm)
  ├─ Compute SHA-256 of tank.wasm
  ├─ Upload tank.wasm → S3 wasm-artifacts/<tankId>/<version>/tank.wasm
  └─ Update tankmaze-tank-versions
        ├─ On success: { status: "ready", wasmS3Key, wasmSha256 }
        └─ On failure: { status: "failed", compileError }

Browser polls GET /tanks/{id}/versions/v0.3/status
  └─ Until status = "ready" or "failed"
```

---

### Match Execution (single Lambda invocation)

```
[Invoker: tournament-scheduler or tank-api (test match)]
  │  Invoke match-runner { matchId }
  ▼
match-runner Lambda
  ├─ Load tankA WASM from S3 → verify SHA-256 → cache in /tmp
  ├─ Load tankB WASM from S3 → verify SHA-256 → cache in /tmp
  ├─ Instantiate two Wazero runtimes (one per tank)
  ├─ Generate maze from mazeSeed (recursive backtracking)
  ├─ Initialize: positions at opposite corners, HP=100, tick=0
  │
  └─ Game loop (tick 0 → tickLimit):
        ├─ Compute sensorsA, sensorsB (raycast, proximity, cooldowns)
        ├─ Call tankA.Tick(sensorsA) via Wazero — fuel-limited 50 ms
        ├─ Call tankB.Tick(sensorsB) via Wazero — fuel-limited 50 ms
        ├─ Process actions (move validation, rotation, fire, scan, idle)
        ├─ Advance projectiles one cell
        ├─ Detect wall hits → destroy projectile
        ├─ Detect tank hits → apply damage → check HP ≤ 0
        ├─ Check win condition → break if met
        ├─ Append tick record to in-memory log []TickRecord
        ├─ Broadcast TICK_UPDATE to observers
        │     └─ GET tankmaze-connections where matchId = X
        │        POST ApiGateway Management API for each connectionId
        └─ Sleep remainder of 100 ms tick budget

  ├─ Apply tiebreaker if tick limit reached (damage → moves → both lose)
  ├─ Write tick log → S3 match-logs/<matchId>/ticks.json.gz
  ├─ Update tankmaze-matches { status: "ended", result, tickLogS3Key }
  ├─ Update tankmaze-tank-versions stats (winRate, avgDamage, etc.)
  └─ If ranked: async invoke ranking-updater { gameDayId }
```

---

### Observer Join (live match)

```
Browser (Observer)
  │  GET /matches/{matchId}   (REST — no auth required)
  ▼
tank-api Lambda
  └─ Return { status, tankA, tankB, mazeSeed } — no maze grid, no WASM

Browser
  │  WebSocket connect ?matchId=<id>   (no JWT required)
  ▼
API Gateway → wss-handler Lambda ($connect)
  ├─ Validate matchId exists in tankmaze-matches
  ├─ Store { connectionId, matchId } in tankmaze-connections
  └─ (connection established)

Browser
  │  { action: "OBSERVE", matchId }
  ▼
wss-handler Lambda ($default)
  ├─ Load current match state from tankmaze-matches
  ├─ Send MATCH_SNAPSHOT → browser (full maze + tank positions + HP)
  └─ Future TICK_UPDATEs arrive from match-runner broadcasts
```

---

### Observer Replay (past match)

```
Browser
  │  WebSocket connect ?matchId=<id>&replay=true
  ▼
wss-handler Lambda
  ├─ Store connection (role: OBSERVER, mode: REPLAY)
  └─ Stream tick log from S3 at requested speed

Browser  →  { action: "REPLAY_SPEED", multiplier: 0.5 }
Browser  →  { action: "REPLAY_SEEK",  tick: 42 }
              └─ wss-handler adjusts stream position / delay
```

---

### Game Day Tournament Phase

```
EventBridge Scheduler (registration_close cron)
  │
  └─► tournament-scheduler Lambda
        ├─ Lock tankmaze-gamedays.registeredTanks
        ├─ Sort by Global Rank (tiebreak: Win Rate)
        └─ Compute pot seeding → write groups to tankmaze-gamedays

EventBridge Scheduler (round_robin cron)
  │
  └─► tournament-scheduler Lambda
        ├─ Read groups from tankmaze-gamedays
        ├─ Generate all within-group match pairs
        ├─ For each pair: write tankmaze-matches record
        ├─ Async invoke match-runner for each match (parallel)
        └─ Poll until all matches status = "ended"
              └─ Compute group standings → write to tankmaze-gamedays

EventBridge Scheduler (elimination_rN cron)
  │
  └─► tournament-scheduler Lambda
        ├─ Read current bracket from tankmaze-gamedays
        ├─ Seed next round: best vs worst (global re-rank)
        ├─ Invoke match-runner for each bracket pair
        └─ Update bracket with results

[Final phase completes]
  └─► tournament-scheduler → async invoke ranking-updater { gameDayId }

ranking-updater Lambda
  ├─ Compute final placements from bracket
  ├─ Apply points formula: 1st=n, kth=max(0, n−2^(k−1))
  ├─ Write tankmaze-rankings records (one per tank, with TTL = expiresAt)
  └─ Recompute globalScore for each participating tank
        └─ Sum tankmaze-rankings where expiresAt > now()
           Update tankmaze-tanks.globalScore, bestFinish, gameDaysCount
```
