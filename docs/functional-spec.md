# TankMaze — Functional Specification

## 1. Overview

TankMaze is a **code-battle platform** built on AWS. Users write autonomous tank programs in JavaScript, submit them to the platform, and watch them fight inside a randomly generated labyrinth. The tank's code decides everything — when to scan, when to move, when to fire — without any real-time input from the user after submission. Users compete through the quality of their code, not their reflexes.

A match starts automatically as soon as two tanks are available (either two user-submitted tanks or one user-submitted tank paired with a built-in AI opponent). Third-party observers can watch any match live with full map visibility.

---

## 2. User Roles

| Role | Description | Auth Required |
|---|---|---|
| **Tank Author** | Writes, submits, and manages tank programs | Yes (Cognito) |
| **Observer** | Watches a live match with full map visibility | No (session link) |

> There is no "manual player" role. Tank Authors never control their tanks in real time.

---

## 3. Tank Programming Model

### 3.1 What a Tank Is

A tank submission is a JavaScript module that exports two things:

- **`config`** — declares the tank's stat allocation.
- **`tick(sensors, memory)`** — a function called by the server every game tick. It receives sensor data and must return a single action.

The `memory` object persists across ticks within a single match, giving the tank a private scratchpad to build internal state (e.g., a partial map, a turn counter, a direction history).

### 3.2 Tank Module Format

```javascript
// config: allocate exactly 15 points across 5 stats (each 1–5)
export const config = {
  name: "My Tank",
  speed:       3,   // movement rate
  sensorRange: 4,   // ray-cast distance
  damage:      2,   // damage per projectile
  armor:       3,   // damage reduction
  fireRate:    3,   // shots per second
  // speed + sensorRange + damage + armor + fireRate must equal 15
};

// tick: called once per server game tick (~100 ms)
// sensors: current environment data (see §3.3)
// memory:  plain object — read/write freely, persists across ticks
// returns: one Action (see §3.4)
export function tick(sensors, memory) {
  if (!memory.initialized) {
    memory.initialized = true;
    memory.stepsTaken = 0;
  }

  if (sensors.proximityAlert && sensors.fireCooldown === 0) {
    return { action: "FIRE" };
  }

  if (sensors.wallDistances[sensors.facing] > 1 && sensors.moveCooldown === 0) {
    memory.stepsTaken++;
    return { action: "MOVE", direction: "FORWARD" };
  }

  return { action: "ROTATE", direction: "RIGHT" };
}
```

### 3.3 Sensor Data Object

Passed to `tick()` as `sensors` each tick. Contains only what the tank's hardware can detect — not the full maze.

| Field | Type | Description |
|---|---|---|
| `facing` | `"N"\|"S"\|"E"\|"W"` | Current heading |
| `position` | `{ x: number, y: number }` | Tank's own cell coordinates |
| `hp` | `number` | Current hit points (0–100) |
| `wallDistances` | `{ N, S, E, W: number }` | Cells to nearest wall in each direction (capped at sensor range) |
| `proximityAlert` | `boolean` | Opponent tank is within sensor range |
| `opponentBearing` | `string \| null` | 8-compass direction to opponent (`"NE"`, `"W"`, etc.) — `null` if not in range |
| `moveCooldown` | `number` | Milliseconds until next move is allowed (0 = ready) |
| `fireCooldown` | `number` | Milliseconds until next shot is allowed (0 = ready) |
| `tick` | `number` | Current tick counter (monotonically increasing) |

**What sensors cannot reveal:**
- Maze layout beyond sensor range
- Opponent's HP, facing direction, or archetype
- Opponent's position coordinates (only bearing and proximity)

### 3.4 Actions

`tick()` must return exactly one action object per call. Returning `null`, `undefined`, or an invalid action defaults to `IDLE`.

| Action | Object | Effect |
|---|---|---|
| Move | `{ action: "MOVE", direction: "FORWARD"\|"BACKWARD" }` | Advance or retreat one cell |
| Rotate | `{ action: "ROTATE", direction: "LEFT"\|"RIGHT" }` | Turn 90° |
| Fire | `{ action: "FIRE" }` | Launch projectile in current facing direction |
| Scan | `{ action: "SCAN" }` | Explicit sensor refresh; returns updated `sensors` on next tick at no action cost |
| Idle | `{ action: "IDLE" }` | Do nothing this tick |

> **Note:** Sensors are always refreshed each tick regardless of whether `SCAN` is used. `SCAN` does not consume the move or fire cooldown slot — it is an explicit no-op that guarantees updated data arrives before the next decision.

### 3.5 Sandbox Constraints

User code runs inside an isolated server-side sandbox. The following constraints are enforced:

| Constraint | Limit |
|---|---|
| Execution time per tick | 50 ms (tank auto-IDLEs if exceeded) |
| `memory` object size | 64 KB |
| Code size | 100 KB |
| Network access | None |
| File system access | None |
| Globals available | `Math`, `JSON`, `Array`, `Object`, `Map`, `Set`, `console.log` (capped at 10 lines/tick) |

Violations (timeout, exception) are logged and result in an `IDLE` action for that tick. Repeated timeouts (>20% of ticks) disqualify the tank from ranked matchmaking.

### 3.6 Stat System

Each tank allocates exactly **15 points** across 5 stats. No stat may be below 1 or above 5. Submissions that don't sum to 15 are rejected.

| Stat | Effect (per point) |
|---|---|
| `speed` | Move cooldown: `1000 / (speed × 2)` ms between moves |
| `sensorRange` | Ray-cast max distance: `speed × 2` cells |
| `damage` | Damage per projectile: `speed × 10` HP |
| `armor` | Damage reduction: `speed × 10`% |
| `fireRate` | Fire cooldown: `1000 / (fireRate × 0.5)` ms between shots |

All tanks start with **100 HP**.

---

## 4. Built-in AI Tanks

Three reference tank implementations are built into the platform. They serve two purposes:

1. **Opponent for testing** — a submitted tank can be matched against a built-in AI to run immediately without waiting for another user's submission.
2. **Inspiration / learning resource** — their source code is publicly readable in the platform's documentation.

| Name | Speed | Sensor | Damage | Armor | Fire Rate | Strategy |
|---|---|---|---|---|---|---|
| **Scout** | 5 | 3 | 2 | 2 | 3 | Evades walls; circles opponent once detected |
| **Ranger** | 3 | 5 | 3 | 2 | 2 | Patrols until opponent in range; precision firing |
| **Bruiser** | 2 | 2 | 5 | 5 | 1 | Straight-line approach; fires on contact |

Built-in tanks do not appear in ranked leaderboards. They cannot be beaten by the system to claim a rank.

---

## 5. Tank Submission & Lifecycle

### 5.1 Submission Flow

1. Tank Author opens the in-browser **code editor** (Monaco-based).
2. Author writes or pastes their tank module (config + tick function).
3. Author clicks **Validate** — platform runs a static check:
   - Stat points sum to 15.
   - `tick` is a valid exported function.
   - No disallowed globals.
   - Sandbox dry-run against 3 ticks of simulated sensor data.
4. If validation passes, Author clicks **Submit**. A new tank version is created under their account.
5. Tank enters **matchmaking queue**.
6. Author may optionally click **Test vs. AI** to immediately start a match against a built-in AI tank.

### 5.2 Versioning

- Each submission creates a new **version** (v1, v2, v3, …).
- All versions are stored and their stats are tracked independently.
- The Author's **active version** is the one currently queued for ranked matchmaking.
- Authors can switch their active version at any time (takes effect on the next queued match).
- Previous versions can be viewed, re-edited as a starting point, and re-submitted.

### 5.3 Tank Dashboard (per tank, per version)

Visible to the Tank Author on their profile:

| Stat | Description |
|---|---|
| **Name** | Tank name from config |
| **Version** | v1, v2, … |
| **Submitted** | Date first submitted (age shown as "X days ago") |
| **Win Rate** | Wins ÷ total completed matches (%) |
| **Matches Played** | Total completed matches for this version |
| **Avg. Damage Dealt** | Average damage output per match |
| **Avg. Survival Time** | Average time alive per match |
| **Last Match** | Link to last match replay |

---

## 6. Matchmaking

### 6.1 Queue

- When a tank is submitted (or its active version is changed), it is placed in the **ranked queue**.
- The server pairs the two longest-waiting tanks. Match starts within seconds.
- If the queue has only one tank, it is offered the option to **play vs. AI** immediately or wait up to 5 minutes for a human opponent before being auto-matched to a built-in AI.

### 6.2 Match Types

| Type | Trigger | Affects Rank Stats |
|---|---|---|
| Ranked | Two user tanks matched from queue | Yes |
| vs. AI (Test) | User clicks "Test vs. AI" | No |
| Rematch | Both authors agree to rematch after a ranked game | Yes |

### 6.3 Match Notification

When a match is found, both Tank Authors receive a notification (browser push or email, per preference). A live watch link is included. Authors are not required to watch — the match runs regardless.

---

## 7. Labyrinth

- Grid-based: **25 × 25 cells** (configurable per match type in future versions).
- Randomly generated per match using the **Recursive Backtracking** algorithm with a random seed.
- Guaranteed to be fully connected (every cell reachable from every other).
- Both tanks spawn at diagonally opposite corners.
- The full maze is **never sent to tank code** — only sensor readings are passed to `tick()`.
- The maze seed is recorded with the match for replay purposes.

### 7.1 Cell Types

| Cell | Description |
|---|---|
| Open | Passable space |
| Wall | Impassable; blocks movement and projectiles |
| Spawn | Starting cell per tank |

---

## 8. Combat

- **Projectiles** travel one cell per server tick in the tank's facing direction at fire time.
- A projectile is destroyed on hitting a wall or an opponent tank.
- On hit: `effective_damage = attacker_damage_stat × 10 × (1 − defender_armor_reduction)`.
- A tank reaching 0 HP is **destroyed**; the match ends immediately.
- A move into the opponent's cell causes a **collision**: both tanks are pushed back, each takes 5 HP contact damage.

---

## 9. Observer Mode

Observers connect via a shareable link generated at match start:  
`https://<domain>/watch?match=<matchId>`

Observer receives in real time (WebSocket):
- Full maze layout
- Both tanks: position, facing direction, current HP, stat profile
- Projectiles in flight
- Game events: moves, shots, hits, match end

Observers also see a **debug panel** per tank (togglable):
- Last action returned by `tick()`
- Current sensor readings
- `console.log` output from tank code (last 10 lines)

Observers cannot interact with the match.

---

## 10. Game Lifecycle

```
[Submission / Queue] → [Match Found] → [Countdown 3s] → [Active Match]
      → [Match Over] → [Stats Recorded] → [Rematch? / Back to Queue]
```

| State | Description |
|---|---|
| Queued | Tank waiting for opponent |
| Match Found | Opponent paired; maze generated; notification sent |
| Countdown | 3-second start countdown visible to observers |
| Active | Server calls `tick()` on both tanks every 100 ms |
| Match Over | Win condition met; stats persisted |

### 10.1 Win Conditions

| Condition | Winner |
|---|---|
| Opponent tank HP reaches 0 | Surviving tank |
| Opponent tank code crashes unrecoverably | Surviving tank |
| Timeout (10 min) | Tank with higher remaining HP |
| Tie (equal HP at timeout) | Draw — no rank change |

---

## 11. Post-Match Summary

Displayed on the match result page (accessible to both Authors and any observer):

| Metric | Description |
|---|---|
| Winner / Loser | Tank name, author, version |
| Final HP | Both tanks |
| Damage dealt / received | Per tank |
| Shots fired / hits / accuracy | Per tank |
| Moves made | Per tank |
| Match duration | Wall-clock time |
| Tick violations | Timeout or exception count per tank |
| Replay link | Full match replay (persistent) |

---

## 12. Match Replay

Every match is recorded server-side (maze seed + full action log per tick). Replays are:
- Stored indefinitely (or until the Author deletes their account).
- Accessible via permanent URL.
- Playable at 1×, 2×, 4× speed or step-by-step.
- Include the debug panel (sensor data + console output per tick).

---

## 13. Out of Scope (v1)

- Languages other than JavaScript (Python, TypeScript compilation, WASM — future)
- More than 2 tanks per match
- Power-ups or collectibles
- Non-grid (free-movement) navigation
- Public leaderboard / global ranking system
- Mobile native app
- Tank-to-tank communication (multi-tank alliances)
