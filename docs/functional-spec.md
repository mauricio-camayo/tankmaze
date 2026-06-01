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
- A fresh tank starts at `v0.0` (before any promotion has ever occurred).
- Every save/validate cycle bumps the minor number (`v0.1`, `v0.2`, …).
- When the Author clicks **Promote to Major** for the first time, `v0.x` becomes `v1` and the minor counter resets to `0` (next save → `v1.1`).
- Subsequent promotions follow the same pattern: `v1.x → v2`, `v2.x → v3`, etc.
- Authors can branch from any previous minor or major version and continue editing — the branch starts a new minor chain off that version.
- Only major versions (`v1`, `v2`, …) are eligible for Game Day registration and ranked statistics.
- `v0.x` and all minor versions can be used for unlimited test matches against AI, against built-in tanks, or against the Author's own other tanks in informal (unranked) matches.

### 5.3 Submission Flow

1. Tank Author opens the in-browser **code editor** (Monaco-based).
2. Author writes or edits their tank module (config + tick function).
3. Author clicks **Save & Validate** — platform runs:
   - Stat points sum to 15.
   - `tick` is a valid exported function.
   - No disallowed globals.
   - Sandbox dry-run against 5 ticks of simulated sensor data.
4. On success, a new minor version is saved (e.g., `v0.3`). No match is started automatically.
5. Author can then:
   - Continue editing (next save → `v0.4`).
   - Click **Test vs. AI** to run an unranked match against a built-in AI tank.
   - Click **Test vs. My Tank** to select any of their own other tanks (any version) as the opponent.
   - Click **Promote to Major** to create the next major version and make it eligible for Game Day.
   - Click **Register for Game Day** to enter the current major version in the next scheduled competition window.

### 5.4 Tank Dashboard (per tank)

Visible to the Tank Author on their profile. Stats are split into two scopes:

- **Tank-level** — accumulated across all versions and never reset by a promotion.
- **Version-level** — tracked independently per major version; reset to zero when a new major version is promoted.

| Field | Scope | Description |
|---|---|---|
| **Name** | Tank | Tank name from config |
| **Active Major Version** | Tank | The major version currently registered (or last used) for Game Day |
| **Global Rank** | Tank | Current position on the global leaderboard — persists across version promotions (see §6.7) |
| **Global Score** | Tank | Sum of all valid placement points across all versions — persists across promotions (links to per-Game-Day breakdown with expiry dates) |
| **Submitted Since** | Tank | Date the first major version was promoted (age shown as "X days ago") |
| **Version History** | Tank | List of all major versions; each expandable to show its minor chain and version-level stats |
| **Win Rate** | Major version | Wins ÷ ranked matches for this version (%) |
| **Matches Played** | Major version | Total Game Day matches for this version |
| **Avg. Damage Dealt** | Major version | Average damage output per ranked match for this version |
| **Avg. Survival Time** | Major version | Average time alive per ranked match for this version |
| **Test Matches** | Minor version | Count of test matches run for that minor version |
| **Last Match** | Tank | Link to most recent replay (any type, any version) |

---

## 6. Game Day & Scheduling

### 6.1 Game Day Schedule

A Game Day is a multi-phase tournament that unfolds over one or more days. Each phase has its own independently scheduled trigger using standard cron syntax. All schedule entries are platform configuration parameters — they can be changed by the administrator without code changes.

```yaml
# Standard cron fields: minute  hour  day-of-month  month  day-of-week
schedule:
  registration_close: "0 18 * * 6"    # Saturday 6 PM  — registration deadline
  round_robin:        "0 20 * * 6"    # Saturday 8 PM  — Phase 1 runs
  elimination_r1:     "0 20 * * 0"    # Sunday   8 PM  — Round of 16 / Quarterfinals
  elimination_r2:     "0 22 * * 0"    # Sunday  10 PM  — Semifinals
  final:              "0 21 * * 6"    # Next Saturday 9 PM — Final
```

- Each phase trigger fires independently. The platform skips a phase trigger if the previous phase has not yet completed (e.g., if Round Robin is still running when `elimination_r1` fires, R1 is postponed to the next `elimination_r1` tick).
- Elimination phases beyond what the bracket requires are silently skipped (e.g., if only 2 tanks advance, R1 is the Final).
- All users see the full phase schedule for the current Game Day on their dashboard, including the status of each phase (upcoming / running / complete).

### 6.2 Registration

- At any point before a Game Day window opens, a Tank Author can **register** their current major version for the next competition.
- Registration is explicit — tanks are never entered automatically.
- The Author can withdraw their registration at any time before the window opens.
- If the Author promotes a new major version after registering, they must re-register the new version; the old registration is not transferred automatically.

### 6.3 Match Execution During Game Day

Game Day runs in two sequential phases: **Round Robin** and **Elimination**.

---

#### Phase 1 — Round Robin (Groups of 8)

1. When the Game Day window opens, the platform collects all registered tanks and ranks them by **Global Rank** (§6.7), best rank first. Win Rate (wins ÷ total ranked matches for that major version) is used as the tiebreaker when two tanks share the same Global Rank.
   - Tanks with no Global Score (e.g., newly promoted major versions that have never competed) are placed at the bottom of the ranking, sorted among themselves by Win Rate, then randomly if Win Rate is also equal.
2. The ranked list is distributed into groups of 8 using **pot seeding**: tanks are divided into pots of equal size (one pot per group slot), and one tank is drawn from each pot per group — ensuring every group contains exactly one tank from each tier of the field.

   ```
   Example: 24 tanks → 3 groups of 8, 8 pots of 3

   Pot 1 (ranks 1–3):   one goes to Group A, one to B, one to C
   Pot 2 (ranks 4–6):   one goes to Group A, one to B, one to C
   ...
   Pot 8 (ranks 22–24): one goes to Group A, one to B, one to C
   ```

   Within each pot, assignment to groups is random. This guarantees each group has a balanced spread of Win Rates — no group is stacked with top-ranked tanks.

   - If the total number of tanks is not a multiple of 8, the last group may have 6 or 7 tanks (never fewer than 6). Pot sizes adjust proportionally for non-uniform group counts.

3. Within each group, every tank plays every other tank exactly once (full round-robin). A group of 8 produces 28 matches.
4. Matches within a group run in parallel up to platform concurrency limits.

**Points per match result:**

| Outcome | Points awarded |
|---|---|
| Win (opponent destroyed or tiebreaker win) | 1 pt |
| Flawless win (opponent destroyed + zero damage received) | 2 pts |
| Loss | 0 pts |
| Both tanks lose (§10.4 rule 5) | 0 pts each |
| Bye (automatic advancement, no match played) | 1 pt |

> Flawless wins are not possible on byes — the 2-point bonus requires an opponent to be destroyed in an actual match.

**Group standings tiebreakers** (applied in order when two tanks have equal points):
1. Total damage dealt across all group matches
2. Total moves made across all group matches
3. Random draw (documented in the Game Day summary)

---

#### Phase 1 → Phase 2 Qualification

The number of tanks that advance to elimination depends on the total field size:

| Total registered tanks | Advancement rule |
|---|---|
| **≤ 64** | All tanks advance (entire field enters elimination) |
| **> 64** | Top **⌊2/3⌋** of tanks from each group advance |

For the standard group of 8 with > 64 total tanks: `⌊8 × 2/3⌋ = 5` tanks advance per group. The bottom 3 in each group are eliminated.

All advancing tanks are **globally re-ranked** by their round-robin points (then by the tiebreakers above) before the elimination bracket is seeded.

---

#### Phase 2 — Elimination Bracket (Single Elimination)

1. Advancing tanks are seeded globally: rank 1 (highest points) through rank N (lowest points).
2. The bracket pairs **best against worst**: seed 1 vs seed N, seed 2 vs seed N−1, seed 3 vs seed N−2, and so on.
3. If the number of advancing tanks is not a power of 2, the top-seeded tanks receive **byes** in round 1 (they advance automatically to round 2). Byes are assigned to the highest seeds first.
4. Each elimination match is a single game. Losers are out; winners advance.
5. The last tank standing is the **Game Day Champion**.

**Elimination bracket example (8 advancing tanks):**

```
Round 1          Semifinal        Final
Seed 1 ──┐
          ├── winner ──┐
Seed 8 ──┘             │
                        ├── winner ──┐
Seed 4 ──┐             │            │
          ├── winner ──┘            ├── Champion
Seed 5 ──┘                         │
                                    │
Seed 2 ──┐                         │
          ├── winner ──┐            │
Seed 7 ──┘             │            │
                        ├── winner ──┘
Seed 3 ──┐             │
          ├── winner ──┘
Seed 6 ──┘
```

---

#### Phase 2 — Scoring for Tank Stats

- Elimination round wins and losses are recorded against the tank's ranked stats separately from round-robin results.
- The final ranking for a Game Day is: Champion first, then finalists, then semi-finalists, then by round-robin seed for all first-round eliminations.
- A tank that qualifies but receives a bye is credited with an unplayed round-1 win for stat purposes.

---

#### End of Game Day

When all elimination matches are complete (or the scheduled window closes — whichever comes first), the platform publishes a **Game Day summary** containing full bracket, all match replays, and updated tank stats.

### 6.4 Match Types

| Type | Trigger | Affects Ranked Stats |
|---|---|---|
| **Ranked** | Game Day execution, two registered user tanks | Yes |
| **Test vs. AI** | Author clicks "Test vs. AI" against a built-in tank | No |
| **Test vs. Own Tank** | Author selects one of their other tanks as opponent | No |
| **Informal** | Author invites another author to an unranked match | No |
| **Rematch** | Both authors agree to re-run a previous Game Day matchup | No |

### 6.5 Match Notification

When a Game Day match involving a tank starts, both Tank Authors receive a notification (browser push notification or email, configurable per user). A live watch link is included. Authors are not required to watch — the match runs regardless of whether anyone is observing.

---

### 6.6 Game Day Placement Points

At the end of each Game Day, every participating tank receives placement points based on its final standing and the number of competitors **n** in that Game Day.

**Points formula:**

| Placement | Points |
|---|---|
| 1st | n |
| 2nd | n − 2 |
| 3rd | n − 4 |
| 4th | n − 8 |
| kth (k ≥ 2) | max(0,  n − 2^(k−1)) |

The subtraction doubles with each successive placement. Points are floored at 0 — no placement ever yields negative points.

**Example — n = 32 competitors:**

| Placement | Points |
|---|---|
| 1st (Champion) | 32 |
| 2nd (Runner-up) | 30 |
| 3rd–4th (Semifinalists) | 28 |
| 5th–8th (Quarterfinalists) | 24 |
| 9th–16th (R1 losers) | 16 |
| 17th–32nd (Round-robin eliminated) | 0 |

**Shared placements:** tanks eliminated at the same round share the highest placement available to that group. Both semifinal losers receive 3rd-place points; all quarterfinal losers receive 5th-place points, and so on. Ties within a shared placement tier do not subdivide further — all tanks in the group receive the same points.

**Byes:** a tank that received a bye in Round 1 of elimination and then lost in Round 2 is placed alongside the other Round 2 losers (no separate treatment).

**Both-lose in elimination:** both tanks receive the loser's placement for that round — same as a normal loss.

---

### 6.7 Global Ranking

Each tank's **Global Ranking score** is the sum of all valid placement points it has accumulated across Game Days, regardless of which major version competed in each Game Day. Promoting to a new major version does not reset or affect the Global Score or Global Rank.

**Points validity:** placement points from a Game Day are valid for a configurable period (default: **1 year**) from the date of that Game Day. Once expired, those points are dropped from the score and the global ranking is recalculated automatically.

```
Global Score = Σ placement_points(game_day)
               for all game_days where (today − game_day.date) < validity_period
```

**Global Ranking display** (public, visible to all users):

| Column | Description |
|---|---|
| Rank | Position in the global leaderboard (1 = highest score) |
| Tank Name | Name from config |
| Author | Tank Author's username |
| Active Version | The major version contributing to current ranked stats |
| Global Score | Sum of valid placement points |
| Best Finish | Highest placement ever achieved (all-time) |
| Game Days | Number of Game Days participated in (within validity window) |
| Last Active | Date of most recent Game Day participation |

**Ranking tiebreaker** (equal Global Score): tank with the higher Best Finish wins; if still tied, the one with more Game Days participated.

**Decay visibility:** on a tank's profile, authors can see a breakdown of points per Game Day with an expiry date for each entry — so they know when a high-scoring result is about to drop off their total.

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

### 10.1 Game Day Lifecycle (on schedule)

Each phase is triggered independently by its own cron entry (§6.1). Authors can review results and replays between phases.

```
[registration_close trigger]
        │
        ▼
[Round Robin trigger] ──→ [Pot seeding by Win Rate] ──→ [Groups of 8 run]
        │                                                        │
        │                                              [Standings published]
        │                                              [Authors review replays]
        ▼
[elimination_r1 trigger] ──→ [Global re-rank] ──→ [Best-vs-worst bracket seeded]
        │                                                  │
        │                                       [Round 1 matches run]
        │                                       [Results published]
        ▼
[elimination_r2 trigger] ──→ [Remaining matches run] ──→ [Results published]
        │
       ...
        ▼
[final trigger] ──→ [Final match runs] ──→ [Champion crowned]
        │
        ▼
[Game Day summary published — full bracket, all replays, updated stats]
```

If the field is small enough that fewer elimination rounds are needed, later phase triggers are skipped automatically. If a phase trigger fires before the previous phase is complete, it waits for completion before proceeding.

### 10.2 Match States

| State | Description |
|---|---|
| Scheduled | Match is queued within the Game Day window; not yet started |
| Countdown | 3-second countdown; observers can join |
| Active | Server calls `tick()` on both tanks every 100 ms |
| Match Over | Win condition met; stats persisted; replay available |

### 10.3 Tick Limit

Every match has a configurable **maximum tick count** (default: **100 ticks**). This is a platform parameter, not a per-tank setting, and can be tuned per match type (e.g., ranked matches may use a different limit than test matches).

When the tick limit is reached without either tank being destroyed, the **tiebreaker sequence** below determines the result. There are no draws in TankMaze — every match ends with either one winner, or both tanks losing.

### 10.4 Win Conditions & Tiebreaker

Resolution is evaluated in order; the first rule that distinguishes the tanks decides the outcome.

| Priority | Condition | Outcome |
|---|---|---|
| 1 | A tank's HP reaches 0 during the match | The surviving tank wins |
| 2 | A tank's code crashes unrecoverably | The surviving tank wins |
| 3 | Tick limit reached — one tank dealt more total damage | Higher-damage tank wins |
| 4 | Tick limit reached — damage is equal — one tank made more moves | Higher-movement tank wins |
| 5 | Tick limit reached — damage and moves are both equal | Both tanks lose |

**Design rationale for rule 4:** movement is the tiebreaker over passivity. A tank that actively hunts its opponent is rewarded over one that sits in place hoping to get shot at. Tanks that wait are penalized.

---

## 11. Post-Match Summary

Displayed on the match result page (accessible to both Authors and any observer):

| Metric | Description |
|---|---|
| Winner / Loser | Tank name, author, version; outcome reason (destroyed / tick limit / both lose) |
| Final HP | Both tanks |
| Ticks elapsed | Total ticks run (≤ tick limit) |
| Damage dealt / received | Per tank — primary tiebreaker if tick limit reached |
| Moves made | Per tank — secondary tiebreaker if damage is equal |
| Shots fired / hits / accuracy | Per tank |
| Match duration | Wall-clock time |
| Tick violations | Timeout or exception count per tank |
| Replay link | Full match replay (permanent URL) |

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
- Mobile native app
- Tank-to-tank communication (multi-tank alliances)
