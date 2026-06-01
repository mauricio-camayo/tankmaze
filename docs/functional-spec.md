# TankMaze — Functional Specification

## 1. Overview

TankMaze is a **code-battle platform** built on AWS. Users write autonomous tank programs in JavaScript, test them freely against built-in AI opponents, and — when ready — register them for **Game Day**: a scheduled competition window (configured via a cron-like parameter) when ranked matches between registered tanks are run automatically.

The tank's code decides everything — when to scan, when to move, when to fire — without any real-time input from the user once a match begins. Users compete through the quality of their code, not their reflexes.

Third-party observers can watch any live match or replay any past match with full map visibility, sensor overlays, and per-tank debug output.

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

### 5.1 Workflow Overview

```
[Edit code] → [Validate] → [Save minor version] → [Test vs. AI]
     ↑                                                    ↓
     └──────────────── iterate ──────────────── [Watch replay / debug]

[Promote to major version] → [Register for Game Day] → [Game Day runs]
```

### 5.2 Versioning

TankMaze uses a two-tier version scheme:

| Tier | Format | Created by | Ranked? |
|---|---|---|---|
| **Major** | `v1`, `v2`, `v3`, … | Author explicitly clicks **Promote** | Yes — eligible for Game Day |
| **Minor** | `v1.1`, `v1.2`, … | Auto-incremented on each save during development | No — test-only |

**Rules:**
- A fresh tank starts at `v1.0` (the first working save after initial validation).
- Every subsequent save/validate cycle bumps the minor number (`v1.1`, `v1.2`, …).
- When the Author clicks **Promote to Major**, the current minor becomes the next whole version (`v1.x → v2`), and the minor counter resets to `0`.
- Authors can branch from any previous minor or major version and continue editing — the branch starts a new minor chain off that version.
- Only major versions are eligible for Game Day registration and ranked statistics.
- Minor versions can be used for unlimited test matches against AI or in informal (unranked) matches.

### 5.3 Submission Flow

1. Tank Author opens the in-browser **code editor** (Monaco-based).
2. Author writes or edits their tank module (config + tick function).
3. Author clicks **Save & Validate** — platform runs:
   - Stat points sum to 15.
   - `tick` is a valid exported function.
   - No disallowed globals.
   - Sandbox dry-run against 5 ticks of simulated sensor data.
4. On success, a new minor version is saved (e.g., `v1.3`). No match is started automatically.
5. Author can then:
   - Continue editing (next save → `v1.4`).
   - Click **Test vs. AI** to run an unranked match immediately.
   - Click **Promote to Major** to create `v2` and make it eligible for Game Day.
   - Click **Register for Game Day** to enter the current major version in the next scheduled competition window.

### 5.4 Tank Dashboard (per tank)

Visible to the Tank Author on their profile. Stats are shown per major version; minor versions are listed in a collapsible history.

| Field | Description |
|---|---|
| **Name** | Tank name from config |
| **Active Major Version** | The major version currently registered (or last used) for Game Day |
| **Version History** | List of all major versions; each expandable to show its minor chain |
| **Submitted Since** | Date the first major version was promoted (age shown as "X days ago") |
| **Win Rate** *(per major)* | Wins ÷ ranked matches for that version (%) |
| **Matches Played** *(per major)* | Total Game Day matches for that version |
| **Avg. Damage Dealt** *(per major)* | Average damage output per ranked match |
| **Avg. Survival Time** *(per major)* | Average time alive per ranked match |
| **Test Matches** *(per minor)* | Count of AI test matches run for that minor version |
| **Last Match** | Link to most recent replay (any type) |

---

## 6. Game Day & Scheduling

### 6.1 Game Day Schedule

Game Day is a configurable scheduled window during which all registered tanks compete. The schedule is controlled by a **cron-like parameter** in the platform configuration — it can be set to any combination of days and hours without code changes.

```
# Examples (standard cron syntax: minute hour day-of-month month day-of-week)
0 20 * * 6       # Every Saturday at 8 PM
0 18 * * 2,4     # Every Tuesday and Thursday at 6 PM
0 14 * * 0,6     # Every Saturday and Sunday at 2 PM
```

This parameter is managed by the platform administrator and can be changed at any time. All users see the next scheduled Game Day on their dashboard.

### 6.2 Registration

- At any point before a Game Day window opens, a Tank Author can **register** their current major version for the next competition.
- Registration is explicit — tanks are never entered automatically.
- The Author can withdraw their registration at any time before the window opens.
- If the Author promotes a new major version after registering, they must re-register the new version; the old registration is not transferred automatically.

### 6.3 Match Execution During Game Day

1. When the Game Day window opens (cron trigger fires), the platform collects all registered tanks.
2. Tanks are paired round-robin (each tank plays every other registered tank once per Game Day).
3. Matches run sequentially or in parallel (up to platform concurrency limits).
4. Match results and stats are recorded after each match.
5. When the window closes (or all matches are complete), a **Game Day summary** is published.

### 6.4 Match Types

| Type | Trigger | Affects Ranked Stats |
|---|---|---|
| **Ranked** | Game Day execution, two registered user tanks | Yes |
| **Test vs. AI** | Author clicks "Test vs. AI" at any time | No |
| **Informal** | Author invites another author to an unranked match | No |
| **Rematch** | Both authors agree to re-run a previous Game Day matchup | No |

### 6.5 Match Notification

When a Game Day match involving a tank starts, both Tank Authors receive a notification (browser push notification or email, configurable per user). A live watch link is included. Authors are not required to watch — the match runs regardless of whether anyone is observing.

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

## 9. Observer & Replay Mode

### 9.1 Live Observer

When a match is actively running, observers connect via a shareable link:  
`https://<domain>/watch?match=<matchId>`

Live observer receives in real time (WebSocket):
- Full maze layout
- Both tanks: position, facing direction, current HP, stat profile, version
- Projectiles in flight
- Game events: moves, shots, hits, match end

### 9.2 Replay Mode

Every completed match (ranked, test, or informal) can be replayed via its permanent URL:  
`https://<domain>/watch?match=<matchId>&replay=true`

Replay is the primary tool for **debugging and iterating** on tank code. Key capabilities:

**Playback controls:**

| Control | Description |
|---|---|
| Play / Pause | Start or stop playback |
| Step Forward | Advance exactly one tick |
| Step Backward | Rewind exactly one tick |
| Jump to tick | Enter a tick number to seek directly |
| Speed selector | Choose playback rate (see §9.3) |

**What replay shows** (same as live observer plus):
- Tick counter (current tick / total ticks)
- Full timeline scrubber
- Ability to jump to any tick instantly

### 9.3 Playback Speeds

Tick speed is adjustable independently of the original match speed. This is especially useful for data analysis and debugging slow or subtle decision patterns.

| Mode | Speed | Use case |
|---|---|---|
| Step-by-step | Manual | Deep debugging; inspect every sensor value and action |
| 0.25× | 1 tick / 400 ms | Slow motion; trace complex movement sequences |
| 0.5× | 1 tick / 200 ms | Careful review |
| 1× | 1 tick / 100 ms | Original match speed |
| 2× | 1 tick / 50 ms | Quick review |
| 4× | 1 tick / 25 ms | Scan for patterns across a long match |
| 8× | 1 tick / 12 ms | Fast scrub |

### 9.4 Debug Panel (per tank, togglable)

Available in both live and replay mode. Shows the internal state of each tank at the current tick:

| Field | Description |
|---|---|
| Action returned | The exact object `tick()` returned this tick |
| Sensor snapshot | Full `sensors` object passed to `tick()` this tick |
| Memory snapshot | Current state of the `memory` object |
| Console output | `console.log` lines emitted this tick (up to 10) |
| Tick duration | Time (ms) the `tick()` function took to execute |
| Violations | Whether this tick timed out or threw an exception |

### 9.5 Data Export

From any replay, the Tank Author (and only the Author of a participating tank) can export the full match data as a JSON file:

```json
{
  "matchId": "...",
  "mazeSeed": "...",
  "maze": [[...], ...],
  "tanks": { "a": { "version": "v2", "config": {...} }, "b": {...} },
  "ticks": [
    {
      "tick": 0,
      "a": { "sensors": {...}, "memory": {...}, "action": {...}, "durationMs": 12 },
      "b": { "sensors": {...}, "memory": {...}, "action": {...}, "durationMs": 8 }
    },
    ...
  ]
}
```

This export enables offline analysis with any external tool (spreadsheets, Python notebooks, etc.).

### 9.6 Access Rules

| Content | Tank Author | Opponent Author | Any Observer |
|---|---|---|---|
| Live match (Game Day / Test) | ✓ | ✓ (Game Day only) | ✓ (via link) |
| Replay — own tank's debug panel | ✓ | — | — |
| Replay — opponent's debug panel | — | — | — |
| Replay — map + positions + HP | ✓ | ✓ | ✓ |
| Data export | ✓ (own tank's data only) | ✓ (own tank's data only) | — |

> Opponent's `memory` and `console.log` are never exposed to the other author — these may contain proprietary strategy logic.

---

## 10. Game Lifecycle

### 10.1 Development Lifecycle (any time)

```
[Edit / Save] → [minor version saved] → [Test vs. AI]
      ↑                                        ↓
      └──────── iterate ───────── [Replay & debug] ──────┐
                                                          ↓
                                              [Promote to Major version]
                                                          ↓
                                              [Register for Game Day]
```

### 10.2 Game Day Lifecycle (on schedule)

```
[Game Day window opens] → [Collect registered tanks] → [Generate pairings]
      → [Run matches] → [Record stats] → [Publish Game Day summary]
      → [Window closes] → [Authors review replays]
```

### 10.3 Match States

| State | Description |
|---|---|
| Scheduled | Match is queued within the Game Day window; not yet started |
| Countdown | 3-second countdown; observers can join |
| Active | Server calls `tick()` on both tanks every 100 ms |
| Match Over | Win condition met; stats persisted; replay available |

### 10.4 Win Conditions

| Condition | Winner |
|---|---|
| Condition | Winner |
|---|---|
| Opponent tank HP reaches 0 | Surviving tank |
| Opponent tank code crashes unrecoverably | Surviving tank |
| Timeout (10 min) | Tank with higher remaining HP |
| Tie (equal HP at timeout) | Draw — no ranked stat change |

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

## 12. Match Recording

Every match (ranked, test, or informal) is recorded server-side as the maze seed plus a full tick-by-tick log of sensor inputs, memory snapshots, actions returned, and execution timing. Recordings are:
- Stored indefinitely (or until the Author deletes their account).
- Accessible via permanent URL immediately after the match ends.
- The primary debugging tool for tank authors — see §9 for full replay capabilities.
- Exportable as JSON for offline analysis (§9.5).

---

## 13. Out of Scope (v1)

- Languages other than JavaScript (Python, TypeScript compilation, WASM — future)
- More than 2 tanks per match
- Power-ups or collectibles
- Non-grid (free-movement) navigation
- Public leaderboard / global ranking system
- Mobile native app
- Tank-to-tank communication (multi-tank alliances)
