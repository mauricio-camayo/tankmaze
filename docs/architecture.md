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

## ADR-010: One DynamoDB table per distinct entity, not multipurpose tables

**Decision:** Use a separate table per entity (originally six — `tanks`, `tank-versions`, `matches`, `connections`, `gamedays`, `rankings` — grown since to thirteen as features were added: `maps`, `user-settings`, `friendships`, `messages`, `platform-config`, `gameday-series`, `oidc-shim-codes`; see `technical-spec.md` §3 for the current full list) rather than a smaller number of multipurpose tables.

**Rationale:** Each table models a distinct entity with a different lifecycle and access pattern. Tank-level stats (`globalScore`, `bestFinish`) are updated after every Game Day regardless of version. Version-level stats (`winRate`, `matchesPlayed`) reset on promotion. Ranking records need TTL-based expiry independent of the tank record. Conflating these into fewer tables would require complex conditional updates and make GSI design awkward. DynamoDB costs per table are negligible; clarity is worth it. This decision has held up well as the platform grew — every new feature (friends, chat, ad config, recurring series) added its own table rather than needing to be shoehorned into an existing one.

---

## ADR-011: Rolling ranking via DynamoDB TTL on ranking records

**Decision:** Placement points expire by setting a `ttl` attribute on `tankmaze-rankings` records. Global Score is recomputed from surviving records.

**Rationale:** DynamoDB TTL provides automatic, zero-cost record expiry without a scheduled cleanup job. When a ranking record expires, the next read of a tank's global score simply sums fewer records. This makes the validity window (default: 1 year) a configuration parameter (`POINTS_VALIDITY_DAYS`) with no infrastructure changes required.

---

## ADR-012: Score transfer via DynamoDB transactional write

**Decision:** Moving Global Score and ranking history from a source tank to a forked tank is executed as a single DynamoDB `TransactWriteItems` call covering all affected records.

**Rationale:** A score transfer touches multiple tables (`tankmaze-tanks` for both source and target, all rows in `tankmaze-rankings` for the source). Without atomicity, a partial failure would leave both tanks in an inconsistent state — e.g., the source zeroed but the target not yet updated. DynamoDB transactions guarantee all-or-nothing across up to 100 items, which is sufficient for any realistic ranking history. The transfer is also gated by a precondition check (target must have zero existing rankings) enforced as a `ConditionExpression` inside the same transaction, making it impossible to accidentally merge two score lineages.

---

## ADR-013: Elimination bracket as an explicit slot state machine

**Decision:** Each bracket slot carries an explicit `status` field (`playing`, `won`, `lost`, `both_lose`, `bye`) rather than inferring slot state from match results at query time.

**Rationale:** The both-lose outcome creates a structural gap in the bracket — a slot that has no winner. Inferring this at query time (joining match results to bracket position) requires complex logic that must be re-evaluated every time the bracket is read. Storing status explicitly on each slot makes the bracket self-describing: `tournament-scheduler` can walk the bracket tree once per phase, detect `both_lose` slots, propagate `bye` to the opposing next-round slot, and detect the no-survivors edge case (all remaining slots are `both_lose`) without re-querying match records. It also makes the bracket display on the frontend a straightforward read with no server-side inference.

---

## ADR-014: OIDC shim for OAuth2-only identity providers (GitHub, Discord)

**Decision:** Front GitHub and Discord with a single self-hosted Lambda (`cmd/oidc-shim`, parameterized by `PROVIDER=github|discord`) that terminates each provider's OAuth2 flow and re-presents it to Cognito as a standards-compliant OIDC provider (`cognito.UserPoolIdentityProviderOidc` with explicit, non-discovery endpoints), rather than adding either as a native CDK social-provider construct.

**Rationale:** CDK's built-in Cognito social-provider constructs are Google, Facebook, Amazon, and Apple — no more. GitHub and Discord OAuth Apps only speak classic OAuth2: no `/.well-known/openid-configuration` discovery document, no `id_token`, just an authorization code redeemable for a bearer token plus a REST call to fetch the profile. Cognito's federation model requires an `id_token` it can verify, so *something* has to bridge the gap. Two options were considered: (a) a Lambda-based OIDC shim that performs the OAuth2 exchange and mints a signed `id_token` Cognito can consume, matching the existing Google/Facebook attribute-mapping shape once shimmed; or (b) a fully custom Cognito Lambda auth-challenge flow that bypasses federation entirely. (a) was chosen because it keeps GitHub/Discord consistent with how Google/Facebook already work (same attribute-mapping shape, same durable-name/avatar resync rules), rather than diverging into a second, harder-to-maintain auth path. Both providers share one Lambda (parameterized, not duplicated), one DynamoDB table for the ephemeral authorization-code hand-off (`tankmaze-oidc-shim-codes`), and the same hand-rolled JWT signer (ADR-015) — Discord's addition reused essentially all of GitHub's shim infrastructure.

**Consequence:** each provider's OAuth App must redirect to the *shim's own* `/callback` URL (a generated API Gateway endpoint, output by CDK as `<Provider>OidcShimCallbackUrl`), not directly to Cognito's `idpresponse` endpoint — the shim sits one hop upstream of Cognito. This is easy to get backwards when configuring the OAuth App by hand, since Cognito's own callback URL is the more "obvious" value to reach for.

---

## ADR-015: Hand-rolled RS256 JWT signing in the OIDC shim, no library dependency

**Decision:** The OIDC shim signs its `id_token`s with a small hand-written RS256 implementation (`crypto/rsa`, `crypto/sha256`, stdlib `encoding/json`/`encoding/base64` only) rather than pulling in a JWT library.

**Rationale:** This repo pins Go toolchain versions carefully (`GOTOOLCHAIN=local`, explicit version checks in CI) because of a prior incident where `go get` silently bumped the `go` directive in `go.mod` and broke the pinned toolchain. Adding a third-party JWT dependency risks pulling in a wider transitive dependency tree and its own Go-version constraints, for what is fundamentally a small, well-specified piece of code (RFC 7515 compact JWS serialization: base64url-encode header + payload, sign with `rsa.SignPKCS1v15`, concatenate with dots). The signing key itself is generated on the shim's first cold start directly into a dedicated Secrets Manager secret — no manual `openssl`-and-paste key-generation step at deploy time. Verified with unit tests that actually execute the signer end-to-end (sign → verify with the derived public key, plus a negative case confirming a different key's signature does *not* verify) — the highest-risk part of this code, and the one part exercisable without a live deployed OAuth round-trip.

---

## ADR-016: Rolling materialization for recurring Game Day series, not pre-created occurrences

**Decision:** A recurring Game Day series (functional spec §6.8) only ever has its *next* occurrence materialized as a real Game Day record. A dedicated Lambda (`series-materializer`) runs hourly and creates the next occurrence once it's within a configurable lead time of firing *and* no earlier occurrence of the series is still open — its Final phase hasn't reached a terminal status (`"complete"` or `"cancelled"`) — then advances the series forward. Gating on "no earlier occurrence still open," not the lead-time window alone, matters once a series' recurrence interval is shorter than the lead time (e.g. a daily series with the default 7-day lead time): without it, `nextOccurrenceAt` re-qualifies as due again immediately after each materialization, so a single tick's worth of lead time turns into the whole series being fast-forwarded and pre-created several occurrences deep (item 262) instead of pre-creating every future occurrence up front when the series is defined. Gating on the occurrence's *actual phase status* rather than its scheduled round-robin timestamp matters too: round-robin starting is not the same as the occurrence finishing — elimination rounds and the final still run afterward — so a timestamp-only check would let the next occurrence materialize while the current one is still running. The first version of this fix used the round-robin timestamp; the user caught the gap by reasoning about a still-open recurring occurrence (`059dcbb7…`, 2026-09-04) before it was merged/deployed, and it was corrected to the phase-status check before shipping.

**Rationale:** Pre-creating every occurrence works cleanly for a fixed repeat count, but breaks down for indefinite recurrence — there's no upper bound to materialize toward. Rolling materialization handles both cases uniformly with one mechanism, and keeps the `tankmaze-gameday-series`/`tankmaze-gamedays` tables free of far-future speculative rows that might never need to exist (e.g. if the series is cancelled). The tradeoff is that the *first* occurrence still needs to appear immediately for a usable admin experience — that one is materialized synchronously in the `POST /gameday-series` handler itself, using the same `internal/scheduling` package the rolling job calls, so the two code paths can't drift apart. An optimistic-lock conditional update (`AdvanceGameDaySeries`) plus a dedup check against existing occurrences (matching `seriesId` + round-robin time) guards against double-materializing if a prior tick's advance step failed after its materialize step had already succeeded — the two aren't in the same transaction, so this had to be handled explicitly rather than assumed atomic.

---

## Lambda account concurrency limit: root cause of the full-platform 503 outage

Not an architecture *decision* so much as an operational lesson worth recording here for anyone investigating a similar symptom in the future.

**Symptom:** every `tank-api` REST route failed simultaneously with `503` during two separate incidents (2026-07-10 and 2026-07-14), rather than one handler misbehaving while others kept working.

**Root cause:** this AWS account's Lambda **concurrent-execution limit was 10** (`aws lambda get-account-settings` → `ConcurrentExecutions: 10`), far below AWS's standard default of 1000 — apparently never raised from a new-account starting quota. `tank-api` alone serves nearly every REST route (single-Lambda design, ADR-004), and a single Dashboard page load fires roughly ten parallel requests; CloudWatch metrics confirmed account-wide `ConcurrentExecutions` hitting 9/10 in the same one-minute window `tank-api` recorded throttles — ordinary traffic, not a bug or an attack, was enough to exhaust the account-wide ceiling shared by all ~16 Lambda functions in the account.

**Fix:** a Service Quotas request to raise the "Concurrent executions" quota (`L-B99A9384`) from 10 to 1000 — the standard default, and the same one nearly every AWS account starts with. This has no direct cost: the limit is a ceiling, not reserved/pre-paid capacity, and Lambda bills only for invocations that actually run (throttled requests were never billed either way).

**Lesson:** when every route behind a single-Lambda REST API fails at once, check the account-wide concurrency limit before looking for a code defect — `aws lambda get-account-settings` takes one call to check and rules out (or confirms) an entire class of "not a bug" outages.

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
        ├─ For each bracket pair: invoke match-runner
        │
        └─ For each completed match: resolve bracket slots
              ├─ result.reason != "both_lose"
              │     └─ winner slot → "won", loser slot → "lost"
              │
              └─ result.reason == "both_lose"
                    ├─ Both slots → "both_lose"
                    ├─ Find opposing next-round slot → set to "bye"
                    │     (opposing slot's occupant advances without playing)
                    └─ If all remaining slots are "both_lose" (no survivors)
                          └─ Set gameday.champion = null
                             Skip to ranking-updater (no Final played)

[Final phase — or champion already determined by both-lose cascade]
  └─► tournament-scheduler
        ├─ If two live finalists: invoke match-runner for Final
        ├─ If one finalist + one "bye" slot: that finalist is champion
        └─ If gameday.champion == null: no champion, skip 1st/2nd points
              └─ Async invoke ranking-updater { gameDayId }

ranking-updater Lambda
  ├─ Compute final placements from bracket slot states
  ├─ Apply points formula: 1st=n, kth=max(0, n−2^(k−1))
  │     └─ Skip 1st and 2nd if gameday.champion == null
  ├─ Write tankmaze-rankings records (one per tank, TTL = expiresAt)
  └─ Recompute globalScore for each participating tank
        └─ Sum tankmaze-rankings where expiresAt > now()
           Update tankmaze-tanks.globalScore, bestFinish, gameDaysCount
```

---

### Tank Fork & Score Transfer

```
Browser (Author)
  │  POST /tanks?forkFrom={sourceTankId}&forkVersion={v}
  ▼
tank-api Lambda
  ├─ Load source tank + version from DynamoDB
  ├─ Copy source.go from S3 → new tankId S3 path
  ├─ Create tankmaze-tanks record { tankId: newId, globalScore: 0,
  │    forkedFromTankId: sourceTankId, forkedFromVersion: v }
  ├─ Create tankmaze-tank-versions record (status: "pending")
  ├─ Trigger CodeBuild for new tank
  │
  ├─ If source tank globalScore > 0:
  │     Return 201 Created { tankId: newId, scorePending: true }
  │     → Frontend shows ForkDialog: "Keep score on source" (default)
  │                                   or "Transfer score to new tank"
  │
  └─ If source tank globalScore == 0:
        Return 201 Created { tankId: newId, scorePending: false }
        → No dialog needed; new tank starts at 0 normally

[Author selects "Transfer score" in ForkDialog]
  │
  │  POST /tanks/{sourceTankId}/score-transfer { targetTankId: newId }
  ▼
tank-api Lambda
  ├─ Validate: same userId, target has no existing tankmaze-rankings
  ├─ Load all tankmaze-rankings records for sourceTankId
  │
  └─ DynamoDB TransactWriteItems (all-or-nothing):
        ├─ PUT tankmaze-rankings for each record (tankId → newId, same data)
        ├─ UPDATE tankmaze-tanks (source): globalScore=0, bestFinish=null,
        │    gameDaysCount=0, scoreTransferredTo=newId
        ├─ UPDATE tankmaze-tanks (target): globalScore, bestFinish,
        │    gameDaysCount from source; scoreTransferredFrom=sourceTankId
        └─ DELETE tankmaze-rankings records for sourceTankId

  └─ Return 200 OK { transferred: true }
        → Frontend dismisses dialog; both tank dashboards refresh
```
