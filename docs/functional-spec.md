# TankMaze — Functional Specification

## 1. Overview

TankMaze is a **code-battle platform** built on AWS. Users write autonomous tank programs in **Go**, compile them to WebAssembly, and submit them to the platform. They test freely against built-in AI opponents or their own other tanks, and — when ready — register them for **Game Day**: a scheduled, multi-phase tournament (configured via cron-like parameters) where registered tanks compete in round-robin groups followed by a single-elimination bracket.

The tank's code decides everything — when to scan, when to move, when to fire — without any real-time input from the user once a match begins. Users compete through the quality of their code, not their reflexes.

Game Day results feed a **global ranking** where tanks accumulate placement points that remain valid for a configurable period (default: 1 year), creating a rolling competitive leaderboard.

Third-party observers can watch any live match or replay any past match with full map visibility, sensor overlays, and per-tank debug output.

---

## 2. User Roles

| Role | Description | Auth Required |
|---|---|---|
| **Tank Author** | Writes, submits, and manages tank programs | Yes (Cognito) |
| **Observer** | Watches a live match with full map visibility | No (session link) |
| **Platform Admin** | Tank Author additionally granted the `platform-admin` role — see §18 Administration | Yes (Cognito) |

> There is no "manual player" role. Tank Authors never control their tanks in real time.

### 2.1 Sign-in Methods

A Tank Author account is a single Cognito identity that can be created and signed into via any of the following. All methods resolve to the same underlying user model (§13.3 subscription/tier fields, tank ownership, etc.) — which sign-in method was used has no gameplay effect.

| Method | Status | Notes |
|---|---|---|
| Email + password | Live | Native Cognito sign-up with email verification |
| Google | Live | Standard OAuth2/OIDC federation |
| Facebook | **Disabled** | Fully built and previously live; the sign-in button is currently hidden by a feature flag pending a business decision — all backend/CDK plumbing remains in place for a fast re-enable |
| GitHub | Live | GitHub's OAuth App speaks classic OAuth2 only (no OIDC discovery document, no `id_token`) — a small platform-operated shim terminates GitHub's OAuth2 exchange and re-presents it to Cognito as a standards-compliant OIDC provider. See ADR in `docs/architecture.md` for why this was necessary. |
| Discord | Live | Same OIDC-shim approach as GitHub, sharing the same shim infrastructure (parameterized by provider) |

**Profile name and picture:** for federated sign-ins, the provider's name/photo populate the account on first sign-in. If the Author later sets a custom display name or avatar, that choice is durable and is never silently overwritten by a subsequent federated re-login re-syncing the provider's original name/photo.

---

## 3. Tank Programming Model

### 3.1 What a Tank Is

A tank submission is a **Go package** compiled to **WebAssembly (WASM)**. The platform provides a typed SDK package (`tankmaze`) that the author imports. The author implements two things:

- **`Config`** — a package-level variable declaring the tank's stat allocation.
- **`Tick(sensors Sensors) Action`** — a function called by the server every game tick. It receives sensor data and must return a single action.

Because the WASM module is loaded once per match and stays resident between tick calls, **package-level variables naturally persist across ticks** — they are the tank's memory. No explicit memory parameter is needed.

### 3.2 Tank Module Format

```go
package tank

import . "github.com/tankmaze/sdk"

// Config: allocate exactly 15 points across 5 stats (each 1–5).
var Config = TankConfig{
    Name:        "My Tank",
    Speed:       3, // movement rate
    SensorRange: 4, // ray-cast distance
    Damage:      2, // damage per projectile
    Armor:       3, // damage reduction
    FireRate:    3, // shots per second
    // Speed + SensorRange + Damage + Armor + FireRate must equal 15
}

// Package-level state persists across ticks for the duration of a match.
var (
    stepsTaken  int
    initialized bool
)

// Tick is called once per server game tick (~100 ms).
// It receives the current sensor readings and must return exactly one Action.
func Tick(s Sensors) Action {
    if !initialized {
        initialized = true
    }

    if s.ProximityAlert && s.FireCooldown == 0 {
        return Action{Type: Fire}
    }

    if s.WallDistances[s.Facing] > 1 && s.MoveCooldown == 0 {
        stepsTaken++
        return Action{Type: Move, Direction: Forward}
    }

    return Action{Type: Rotate, Direction: Right}
}
```

### 3.3 Sensor Data (`Sensors` struct)

Passed to `Tick()` each tick. Contains only what the tank's hardware can detect — not the full maze.

| Field | Go Type | Description |
|---|---|---|
| `Facing` | `Direction` (`N\|S\|E\|W`) | Current heading |
| `Position` | `Point{X, Y int}` | Tank's own cell coordinates |
| `HP` | `int` | Current hit points (0–100) |
| `WallDistances` | `map[Direction]int` | Cells to nearest wall in each direction (capped at sensor range) |
| `ProximityAlert` | `bool` | Opponent tank is within sensor range |
| `OpponentBearing` | `*Direction` | 8-compass direction to opponent — `nil` if not in range |
| `MoveCooldown` | `int` | Milliseconds until next move is allowed (0 = ready) |
| `FireCooldown` | `int` | Milliseconds until next shot is allowed (0 = ready) |
| `Tick` | `int` | Current tick counter (monotonically increasing) |

**What sensors cannot reveal:**
- Maze layout beyond sensor range
- Opponent's HP, facing direction, or stat profile
- Opponent's position coordinates (only bearing and proximity)

### 3.4 Actions

`Tick()` must return exactly one `Action`. An invalid or zero-value return defaults to `Idle`.

| Action | Value | Effect |
|---|---|---|
| Move forward/backward | `Action{Type: Move, Direction: Forward\|Backward}` | Advance or retreat one cell |
| Rotate | `Action{Type: Rotate, Direction: Left\|Right}` | Turn 90° |
| Fire | `Action{Type: Fire}` | Launch projectile in current facing direction |
| Scan | `Action{Type: Scan}` | Explicit sensor refresh; no cooldown consumed |
| Idle | `Action{Type: Idle}` | Do nothing this tick |

> **Note:** Sensors are refreshed every tick regardless of `Scan`. `Scan` is an explicit no-op that consumes no move or fire cooldown — useful when the tank wants to observe one more tick before committing to an action.

### 3.5 Sandbox Constraints

Tank code is compiled to WASM and executed by **Wazero** (a pure-Go WASM runtime) inside the game-tick Lambda. WASM's architecture enforces the most critical constraints by design — tank code has no access to the host filesystem, network, or OS. Additional limits enforced by the platform:

| Constraint | Limit |
|---|---|
| Execution time per tick | 50 ms (enforced by Wazero fuel limit; tank auto-IDLEs if exceeded) |
| WASM linear memory | 4 MB per match instance |
| Compiled WASM binary size | 5 MB |
| Go source code size | 200 KB |
| Filesystem access | None (WASM default) |
| Network access | None (WASM default) |
| Allowed imports | `tankmaze` SDK (required); optional: `fmt`, `log`, `math`, `math/rand`, `sort` (capped at 10 log lines/tick) |
| Syscall access | None — WASI syscalls are blocked by Wazero host configuration |

Violations (timeout, panic) are logged and result in an `Idle` action for that tick. Repeated timeouts (>20% of ticks in a match) disqualify the tank from ranked Game Days until the Author submits a fixed version.

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
| **Randy** | 3 | 3 | 3 | 3 | 3 | Wanders randomly; pursues and fires once opponent enters sensor range |

**Randy** uses balanced stats (all 3s) and a two-phase decision function:

- **Wander phase** (no opponent in sensor range): each tick Randy applies a random move *and* a random rotation simultaneously — moving forward or backward at random while also rotating left or right at random. This combined move+rotate action produces erratic, unpredictable movement across the arena. Randy checks its sensor data for walls before moving and rotates away if a wall is directly ahead, preventing it from getting stuck. Randy does not fire during this phase.
- **Pursuit phase** (opponent detected in sensor range): Randy moves toward the detected opponent's position each tick and fires while doing so. It tracks the opponent as long as sensor contact is maintained.
- **Lost contact**: if the opponent leaves sensor range, Randy immediately reverts to the wander phase.

Randy requires the `math/rand` standard library. Its purpose is to be a noticeably harder baseline than pure randomness — authors must actively outmanoeuvre a pursuing enemy, not just outlast random fire.

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

**Forking into a new tank:**
An Author can create a new tank by loading any version of an existing tank as a starting point (forking). The new tank is an independent identity — it always starts at Global Score = 0 and has no ranking history, regardless of the source tank's score.

If the source tank has a non-zero Global Score, the platform presents a one-time **Score Transfer** option at fork time:
- **Keep score on source tank** (default) — the new tank starts at 0; the source tank retains its score and ranking history.
- **Transfer score to new tank** — the source tank's Global Score and ranking history move to the new tank permanently; the source tank is reset to 0. This is irreversible.

Only one tank per Author can hold a given score lineage. The platform enforces this by preventing a Score Transfer if the new tank already has its own accumulated points.

### 5.3 Submission Flow

1. Tank Author opens the in-browser **code editor** (Monaco-based, Go syntax).
2. Author writes or edits their tank Go source (Config + Tick function).
3. Author clicks **Save & Validate** — platform runs these checks in order:

   | Step | Check | On failure |
   |---|---|---|
   | Static | `Config` stat points sum to 15 | Rejected immediately; no compilation |
   | Static | `Tick` function signature matches SDK | Rejected immediately |
   | Static | No imports other than `tankmaze` SDK and approved optional stdlib (`fmt`, `log`, `math`, `math/rand`, `sort`) | Rejected immediately |
   | Compile | `go build` → WASM (`GOOS=wasip1 GOARCH=wasm`) | Compilation error shown in editor |
   | Runtime | Wazero dry-run: 5 ticks of simulated sensor data | Panic / timeout shown in editor |

4. On success, the compiled WASM binary is stored in S3 and a new minor version is saved (e.g., `v0.3`). No match is started automatically.
5. Author can then:
   - Continue editing (next save → `v0.4`).
   - Click **Test vs. AI** to run an unranked match against a built-in AI tank.
   - Click **Test vs. My Tank** to select any of their own other tanks (any version) as the opponent.
   - Click **Promote to Major** to create the next major version and make it eligible for Game Day.
   - Click **Register for Game Day** to enter the current major version in the next scheduled competition window.

   When starting a test match (either vs. AI or vs. own tank), a **map picker** is shown before the match launches. The Author can select a static map (§7.2) or leave the default (random generation). The selection persists as the user's last-used preference until they change it.

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

### 5.5 Tank Avatars

Each tank can display a custom avatar sprite in the Observer and Replay views. Authors choose an avatar from a built-in set of 16 sprites or upload their own image (PNG/JPEG, max 512 KB). The selected sprite is rendered in the Phaser canvas in place of the default colored rectangle, rotated to match the tank's current heading each tick.

- **Default avatar**: if no avatar is selected, a deterministic sprite is chosen from the built-in set based on the tank's ID hash, so every tank always has a distinct visual identity.
- **Built-in AI avatars**: each built-in tank (Scout, Bruiser, Ranger, Randy) has its own dedicated sprite.
- **Fork inheritance**: forking a tank copies the source tank's avatar selection to the new tank.
- **Upload** (via `PUT /tanks/{id}/avatar`): stored in S3 and served via CloudFront.

---

## 6. Game Day & Scheduling

### 6.1 Game Day Schedule

A Game Day is a multi-phase tournament that unfolds over one or more days. Each phase has its own independently scheduled trigger using standard cron syntax. All schedule entries are platform configuration parameters — they can be changed by the administrator without code changes.

```yaml
# Standard cron fields: minute  hour  day-of-month  month  day-of-week
schedule:
  registration_close: "0 18 * * 6"    # Saturday 6 PM  — registration deadline
  round_robin:        "0 20 * * 6"    # Saturday 8 PM  — Phase 1 runs
  elimination_r1:     "0 20 * * 0"    # Sunday   8 PM  — Round 1 (up to 64 tanks)
  elimination_r2:     "0 22 * * 0"    # Sunday  10 PM  — Round 2
  elimination_r3:     "0 18 * * 1"    # Monday   6 PM  — Round 3 (if needed)
  elimination_r4:     "0 20 * * 1"    # Monday   8 PM  — Round 4 (if needed)
  elimination_r5:     "0 22 * * 1"    # Monday  10 PM  — Round 5 (if needed, ≤64-tank field)
  final:              "0 21 * * 6"    # Next Saturday 9 PM — Final
```

- Each phase trigger fires independently. The platform skips a phase trigger if the previous phase has not yet completed (e.g., if Round Robin is still running when `elimination_r1` fires, R1 is postponed to the next `elimination_r1` tick).
- Elimination phases beyond what the bracket requires are silently skipped. The number of required rounds depends on the field size — see §6.3 Phase 1 → Phase 2 Qualification for the round count table.
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

**Elimination rounds when ≤ 64 tanks register:**

When all tanks advance, the number of elimination rounds (not counting the Final) is `⌊log₂(N)⌋ − 1`, where N is the number of advancing tanks. The Final always uses the last two surviving tanks. Specifically:

| Advancing tanks (N) | Elimination rounds before Final | Total rounds incl. Final | Round sizes |
|---|---|---|---|
| 4 | 1 | 2 | R1: 4 → Final: 2 |
| 8 | 2 | 3 | R1: 8 → R2: 4 → Final: 2 |
| 16 | 3 | 4 | R1: 16 → R2: 8 → R3: 4 → Final: 2 |
| 32 | 4 | 5 | R1: 32 → R2: 16 → R3: 8 → R4: 4 → Final: 2 |
| 64 | 5 | 6 | R1: 64 → R2: 32 → R3: 16 → R4: 8 → R5: 4 → Final: 2 |

If N is not a power of 2, the highest seeds receive **byes** in Round 1 so that the number of remaining tanks after Round 1 is the next lower power of 2 (see Phase 2 — rule 3 below).

The platform schedule defines cron entries for each elimination round up to the maximum needed (one entry per possible round). Rounds that are not required for a given Game Day are silently skipped by the platform at runtime (see §6.1).

---

#### Phase 2 — Elimination Bracket (Single Elimination)

1. Advancing tanks are seeded globally: rank 1 (highest points) through rank N (lowest points).
2. The bracket pairs **best against worst**: seed 1 vs seed N, seed 2 vs seed N−1, seed 3 vs seed N−2, and so on.
3. If the number of advancing tanks is not a power of 2, the top-seeded tanks receive **byes** in round 1 (they advance automatically to round 2). Byes are assigned to the highest seeds first.
4. Each elimination match is a single game. The loser is eliminated; the winner advances.
5. **Both-lose outcome in elimination:** if a match ends with both tanks losing (§10.4 rule 5), both are eliminated. The slot they would have filled in the next round becomes a **Bye** — the next opponent on that bracket path advances automatically without playing.
6. The last tank standing is the **Game Day Champion**.

**Both-lose cascade example (8 tanks, semifinal stage):**
```
Semifinal 1: Seed 1 vs Seed 4 → both lose → neither advances
Semifinal 2: Seed 2 vs Seed 3 → Seed 2 wins

Final: Seed 2 would face the winner of Semifinal 1
       → no winner exists → Seed 2 receives a Bye → Seed 2 is Champion
```
If both semifinal matches produce a both-lose result, no Final is played. The champion is determined by the highest round-robin seed among all surviving finalists; if none survive, the Game Day ends with no champion and no placement points are awarded for 1st or 2nd place.

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

#### Phase 1 — Round Robin Standings Display

During and after the round-robin phase, each group's results are shown in a standings table:

| Column | Description |
|---|---|
| Rank | Position within the group (color-coded: gold / silver / bronze for top 3) |
| Tank | Tank name with placement-point badge |
| Pts | Total points accumulated in the group |
| W | Wins |
| L | Losses |

The table is sorted by points descending, with the group tiebreakers applied in order (§6.3). The page auto-refreshes every 10 seconds while the round-robin phase is running.

> **Planned enhancement:** replace the aggregate table with a per-match cross-table grid (rows = tanks 1–N, columns = opponent numbers, cells show W/L/B/pending from the row-tank's perspective) to expose individual head-to-head results. Tracked as a future improvement.

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

**Map selection for test matches:** when starting a Test vs. AI or Test vs. Own Tank match, the Author can choose between:
- **Random map** (default) — a new maze generated per match from a random seed, using the Recursive Backtracking algorithm.
- **Static map** — one of the platform's built-in static maps (§7.2), or any other map made available by the administrator. Selected by slug (e.g., `donut`, `x`).

Ranked and Informal matches always use randomly generated mazes. Map selection is not available for those match types.

### 6.5 Match Notification

When a Game Day match involving a tank starts, both Tank Authors receive a notification (browser push notification or email, configurable per user). A live watch link is included. Authors are not required to watch — the match runs regardless of whether anyone is observing.

---

### 6.6 Game Day Placement Points

At the end of each Game Day, every participating tank receives placement points based on its final standing and the number of competitors **n** in that Game Day.

**Points formula:**

| Placement | Points |
|---|---|
| 1st | n |
| 2nd | n / 2 |
| 3rd | n / 4 |
| 4th | n / 8 |
| kth (k ≥ 2) | floor(n / 2^(k−1)) |

Each successive placement halves the points of the previous one. Points are floored to the nearest integer — no placement ever yields negative points.

**Example — n = 32 competitors:**

| Placement | Points |
|---|---|
| 1st (Champion) | 32 |
| 2nd (Runner-up) | 16 |
| 3rd–4th (Semifinalists) | 8 |
| 5th–8th (Quarterfinalists) | 4 |
| 9th–16th (R1 losers) | 2 |
| 17th–32nd (Round-robin eliminated) | 1 |

**Shared placements:** tanks eliminated at the same round share the highest placement available to that group. Both semifinal losers receive 3rd-place points; all quarterfinal losers receive 5th-place points, and so on. Ties within a shared placement tier do not subdivide further — all tanks in the group receive the same points.

**Byes:** a tank that received a bye in Round 1 of elimination and then lost in Round 2 is placed alongside the other Round 2 losers (no separate treatment).

**Both-lose in elimination:** both tanks are eliminated and receive the loser's placement for that round. The next opponent in their bracket path receives a Bye (see §6.3 Phase 2, rule 5).

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

### 6.8 Recurring Game Day Series

Rather than only creating one-off dated Game Days, an admin can define a **recurring series** — a template that automatically produces a new Game Day occurrence on a schedule, without an admin manually creating each one.

**Recurrence rule** — chosen when the series is created, alongside the same fields as a one-off Game Day (round robin time, registration-close lead time, final lead time, autofill/forced-maps/random-maps settings — these become the template reapplied to every occurrence):

| Frequency | Meaning |
|---|---|
| Weekly | Same weekday and time as the series' first occurrence |
| Monthly | A fixed day-of-month (clamped to the last day of the month if that day doesn't exist, e.g. day 31 in a 30-day month) |
| Every N days | A fixed interval from the previous occurrence |

**Ending a series:** either indefinite (repeats until an admin cancels it) or a fixed occurrence count set at creation time.

**Materialization:** only the *next* occurrence is ever pre-created as a real Game Day — not the whole future series at once. The first occurrence is created immediately when the series is set up, so admins see it right away; each following occurrence is created automatically as its turn approaches, following the same registration/round-robin/elimination scheduling as any other Game Day (§6.1). Roster, registration, and results are entirely independent per occurrence — nothing carries over from one occurrence of a series to the next.

**Cancelling a series** stops future occurrences from being created. It does **not** retroactively affect any occurrence already created — those continue on their own schedule and results independently, exactly as if they'd been created one-off. Cancelling one specific occurrence (rather than the whole series) uses the same cancellation as any other Game Day and likewise has no effect on the series' future occurrences.

**Admin UI:** the Game Day creation form has a "Repeats" section (frequency, day/interval, and an "ends never / after N occurrences" choice); the Game Day list shows a recurring badge on occurrences that belong to a series, with a "Cancel series" action distinct from cancelling that single occurrence.

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

### 7.2 Static Testing Maps

In addition to randomly generated mazes, the platform ships a set of **static maps** designed for user testing of basic robot movement. These are fixed layouts — not seeded random generation — intended to give tank authors a predictable, readable arena before entering tournament play.

Static maps are stored in the platform's database and can be selected when starting any test match. New maps can be added by the platform administrator without code changes.

**Built-in maps (v1):**

| Slug | Name | Description |
|---|---|---|
| `open` | Open | Only the outer boundary walls. Completely free interior — maximum movement freedom. |
| `donut` | Donut | A single open corridor running 1 cell inside every outer wall, forming a ring. Tests wall-hugging and turn logic. |
| `x` | X | Only the two diagonals are open passage; all other interior cells are walls. The diagonal paths connect directly from corner spawn points. Tests diagonal navigation and corner decisions. |
| `rooms` | Rooms | Four open quadrant rooms — one per corner spawn — connected by a single-cell doorway at the midpoint of each shared interior wall. Tests navigation through chokepoints. |
| `double-spiral` | Double Spiral | Two mirror-image inward spirals sharing a center junction. Each tank starts at the outer end of one spiral arm, must wind inward through the center, then follow the other spiral outward to reach the opponent. Tests sustained path-following and forces both tanks through the same center crossing. |

All static maps are designed with corner spawn points in mind — tank start positions are always reachable and connected to the rest of the layout.

---

## 8. Combat

- **Projectiles** travel one cell per server tick in the tank's facing direction at fire time.
- A projectile is destroyed on hitting a wall or an opponent tank.
- On hit: `effective_damage = attacker_damage_stat × 10 × (1 − defender_armor_reduction)`.
- A tank reaching 0 HP is **destroyed**; the match ends immediately.
- A move into the opponent's cell causes a **collision**: both tanks are pushed back to their pre-move position, and each takes contact damage looked up by its own armor (§8.1).

### 8.1 Collision Damage

Collision damage is **looked up from each tank's own armor** — a tank's own armor determines what *it* personally takes in a collision, unlike weapon damage's attacker/defender split. The table is deliberately **non-linear**: higher armor gives more-than-proportional protection, and Armor 5 reproduces the flat 5 HP every armor level took before this change.

| Own Armor | Damage taken per collision |
|---|---|
| 1 | 15 |
| 2 | 12 |
| 3 | 9 |
| 4 | 7 |
| 5 | 5 |

A collision is not counted as a "hit" — it doesn't affect `shotsFired`/`hits` accuracy stats — but the damage taken does count toward each tank's `damageDealt` total, since collisions can decide the tick-limit damage tiebreak (§10.4 rule 3, §11).

**This table is configurable per environment, not hardcoded.** Set `COLLISION_DAMAGE_TABLE` to five comma-separated non-negative integers, one per own-armor level 1 through 5 in order — e.g. `COLLISION_DAMAGE_TABLE=15,12,9,7,5` (the default above). Any other shape — wrong count, a non-integer, a negative value — falls back to the default table rather than partially applying a bad one. Unset in production as of this writing; the default table above is what's live.

**Rationale:**
- **Rammer viability.** A fast, high-armor "rammer" archetype that intentionally seeks collisions previously got zero benefit from Armor against collision damage — pure ramming was a symmetric wash regardless of build. This makes Armor a real, consistent defensive stat across *every* damage source (weapon fire and collision alike), not just weapon fire.
- **Collision stays secondary to a dedicated gun.** Sanity check: Damage 5 vs. Armor 5 on a direct hit is `effective_damage(5, 5) = 25`. The worst case for collision damage, even at max armor, is 5 — one-fifth of a top-tier weapon hit. Collision is a supplement or denial tool; by design it should never be a standalone win condition.
- **Armor 5 is the deliberate anchor point.** Every table considered keeps max armor (5) reproducing today's flat 5 HP value unchanged — every lower armor level takes *more* than it did before. This is an intentional two-sided tradeoff for skipping Armor, not a one-way buff.
- **Non-linear on purpose.** Early armor points matter more than later ones (drops of 3, 3, 2, 2 rather than a flat step) — a deliberate design choice over a straight-line curve, made configurable specifically so it can be retuned without a code change if the curve needs adjusting after real play.
- **Accepted tradeoff for low-armor tanks.** This is a nerf, relative to before this change, for any tank below Armor 5 in *any* collision — intentional ram or accidental bump alike. Existing low-armor "glass cannon" builds (e.g. the community Sniper tank, Armor 1) take more incidental collision damage than before, cutting against recent work to help such tanks avoid accidental collisions. This consequence was accepted deliberately rather than watering down the mechanic to pre-emptively protect specific existing tanks; balance fallout for individual tanks will be addressed later if it becomes a real problem.

---

## 9. Observer & Replay Mode

### 9.1 Live Observer

When a match is actively running, observers connect via a shareable link:  
`https://<domain>/watch?match=<matchId>`

Live observer receives in real time (WebSocket):
- Full maze layout
- Both tanks: position, facing direction, current HP, stat profile, version, avatar sprite
- Sensor range overlay: each tank's sensor range is drawn as a translucent filled circle on the canvas, redrawn every tick as the tank moves
- Projectiles in flight with directional tracer lines
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
| Sensor snapshot | Full `sensors` object passed to `tick()` this tick with colored directional indicators (red = wall, green = clear; red dot = opponent in range) |
| Memory snapshot | Current state of package-level variables as formatted JSON (collapsed by default, expandable) |
| Console output | `log.Println` / `fmt.Println` lines emitted this tick (up to 10; scrollable) |
| Tick duration | Time (ms) the `tick()` function took to execute |
| Violations | Whether this tick timed out or threw an exception |

### 9.4a Elimination Bracket Display

The elimination bracket is displayed on the Game Day page as a multi-column grid, one column per round. Bracket slot behavior:

- **Connector lines**: SVG or CSS "elbow" lines connect each pair of Round N slots to the corresponding Round N+1 winner slot, making bracket advancement visually clear.
- **Watch links**: each elimination slot that has an associated match ID shows a "Watch" link, allowing observers to replay that match directly from the bracket. Bye slots and upcoming slots show no link.
- **Long tank names**: names longer than 40 characters wrap to two lines within the slot; names longer than 80 characters are truncated with "…".
- **Round navigation**: when more than 3 elimination rounds are present, the bracket shows exactly 3 rounds at a time with left/right navigation controls (one-round overlap between pages preserves context; the Final round is always reachable on the last page).

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
[elimination_r3 trigger] ──→ [Remaining matches run] ──→ [Results published]  (skipped if not needed)
        │
[elimination_r4 trigger] ──→ [Remaining matches run] ──→ [Results published]  (skipped if not needed)
        │
[elimination_r5 trigger] ──→ [Remaining matches run] ──→ [Results published]  (skipped if not needed)
        │
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
- Stored for **7 days** for ranked matches (DynamoDB TTL + S3 lifecycle rule on `match-logs/`); test match storage duration is configurable.
- Accessible via permanent URL immediately after the match ends (while within the retention window).
- The primary debugging tool for tank authors — see §9 for full replay capabilities.
- Exportable as JSON for offline analysis (§9.5).

---

## 13. Subscription Tiers & Monetization

### 13.1 Rationale

Each tank compilation triggers a **CodeBuild run** (the `tank-compiler` pipeline), which has a direct, per-build AWS cost. Players who compile more consume more infrastructure. The subscription system recovers that cost proportionally — it is explicitly **not pay-to-win**: higher tiers unlock more compilations and more registered tanks, but confer **zero in-game advantage**. Stat limits, match rules, and scoring are identical across all tiers.

### 13.2 Tier Definitions

| Tier | Monthly Cost | Max Registered Tanks | Max Compilations / Month |
|---|---|---|---|
| **Free** | $0 | 2 | 10 |
| **Builder** | $5 | 5 | 50 |
| **Pro** | $15 | 15 | 200 |

**Design notes:**
- A **Free tier** always exists so new players can try the platform without a payment commitment.
- Limits apply per user account, not per tank.
- "Max Registered Tanks" is the number of tanks a user may have in their library at any time (across all versions). Deleting a tank frees a slot.
- "Max Compilations / 30 days" counts every successful or failed CodeBuild invocation triggered by that user's **Save & Validate** action. The counter resets on a 30-day rolling window from the start of the current window (see §13.4.3).
- Tier names and limits are platform configuration — they can be adjusted by an administrator without code changes.

### 13.3 Subscription Field on User Record

A `subscriptionTier` field is added to the user record. Possible values: `"free"`, `"builder"`, `"pro"`. Default for all new accounts: `"free"`.

Storage options (TBD at implementation — see Open Questions §13.7):
- Cognito custom attribute (`custom:subscriptionTier`) — simple, but requires Cognito schema change.
- New `tankmaze-user-settings` DynamoDB table (already planned for match notifications, item 34) — preferred; avoids Cognito schema coupling.

### 13.4 Enforcement

#### 13.4.1 Tank Registration Limit

When a user attempts to **create a new tank** (`POST /tanks`) or **fork** an existing tank, the backend checks the current tank count for that user against the tier limit:

- If `userTankCount >= tierLimit.maxTanks` → respond **403** with `{ "reason": "tank_limit_reached", "limit": N, "tier": "free" }`.
- The frontend surfaces an inline message: *"You've reached your Free tier limit of 2 tanks. Upgrade to Builder to register up to 5."*
- The `+ New Tank` button on the Dashboard is disabled (with tooltip) when the limit is reached.

#### 13.4.2 Compilation Limit

When **Save & Validate** is triggered (`POST /tanks/{id}/versions`), the backend checks the user's `compilationsThisMonth` counter before invoking CodeBuild:

- If `compilationsThisMonth >= tierLimit.maxCompilations` → respond **429** with `{ "reason": "compile_limit_reached", "limit": N, "resetsAt": "<ISO date of compilationsWindowStart + 30 days>" }`.
- The frontend surfaces a banner: *"Compilation limit reached (10/10). Resets in X days. Upgrade to Builder for 50 compilations / 30 days."*
- The **Save & Validate** button remains enabled (so the user can still see their source and potentially export it), but the submit action is blocked client-side after receiving 429, with the error banner displayed.

#### 13.4.3 Compilation Counter

- A `compilationsThisWindow` counter and a `compilationsWindowStart` timestamp (ISO 8601) are stored alongside the user's subscription data (same record as `subscriptionTier`).
- On any compile request, if `now - compilationsWindowStart >= 30 days`, reset `compilationsThisWindow` to 0 and set `compilationsWindowStart = now`. This lazy check avoids a scheduled reset job.
- The counter is incremented **when CodeBuild is successfully invoked** (not on static validation failures, which don't trigger a build).
- **Window-start model:** the 30-day clock starts from when the current window opened (first compile after a reset), not from the calendar month boundary. This means two users can have windows that reset on different dates.

### 13.5 Usage & Tier Display (Account Page)

A new **Account** page (or section within the Dashboard) shows the Tank Author their current subscription status:

| Field | Description |
|---|---|
| **Current Tier** | e.g. "Free" with a badge |
| **Tanks** | "2 / 2 used" with a progress bar |
| **Compilations (30-day window)** | "7 / 10 used" with a progress bar; "Resets in X days" (derived from `compilationsWindowStart + 30 days`) |
| **Upgrade** | CTA button → payment flow (TBD) |

The same limits are surfaced inline at enforcement points (Dashboard new-tank button, TankEditor Save & Validate) so the user is never surprised.

### 13.6 Upgrade Path

Payment integration is **TBD** (Stripe or AWS Marketplace are candidates). The upgrade flow should:
1. Present the tier comparison table.
2. Collect payment (external; not in scope for v1 of this spec).
3. On successful payment confirmation, update `subscriptionTier` on the user record.
4. The change takes effect immediately — limits are re-evaluated on the next request.

Downgrade behavior: if a user downgrades and currently exceeds the new tier's tank limit, existing tanks are **not deleted** automatically. Instead, a warning is shown and new tank creation is blocked until the count falls within the new limit. Compilations already used this month are not refunded.

### 13.7 Open Questions

| # | Question |
|---|---|
| 1 | Store `subscriptionTier` + `compilationsThisMonth` in Cognito custom attributes or in the `tankmaze-user-settings` DynamoDB table (planned for item 34)? |
| 2 | Should compilation counter increments use DynamoDB atomic counters or conditional writes to prevent double-counting under retries? |
| 3 | Which payment provider? (Stripe, AWS Marketplace, manual invoice for Pro?) |
| 4 | The 30-day window reset is lazy (on next request). Should a scheduled Lambda also sweep and reset stale windows for users who haven't compiled in >30 days? (Low priority — affects display accuracy only, not enforcement.) |
| 5 | Should exceeding the tank limit block **all** new tanks (including forks from AI templates)? Currently yes — all tank creation paths share one limit. |

---

## 15. Responsive UI

The TankMaze frontend must be usable on phone and tablet viewports in addition to desktop. This is a UI-only change — no backend or CDK modifications are required.

### 15.1 Breakpoints

| Name | Width range | Target devices |
|---|---|---|
| **Mobile** | < 640 px | Phones (portrait and landscape) |
| **Tablet** | 640 px – 1023 px | Tablets, landscape phones |
| **Desktop** | ≥ 1024 px | Current default experience |

### 15.2 Navigation / Header

- On **mobile and tablet**, the global nav bar in `components/Layout.tsx` collapses to a hamburger menu (or a bottom navigation bar). The hamburger opens a slide-in drawer or drop-down containing the same links currently shown inline: Leaderboard, live clock, username, Sign out.
- On **desktop**, the existing inline nav is preserved unchanged.

### 15.3 Pages & Views

#### Dashboard (tank list) — `pages/Dashboard.tsx`

- Tank cards reflow from a multi-column grid to a single-column stack on mobile.
- The AI template chip row (`AiTemplateRow`) wraps horizontally or becomes vertically scrollable.
- The `+ New Tank` button and GameDayCard remain full-width.

#### TankDetail — `pages/TankDetail.tsx`

- Stat pip section and version history collapse to a single column on mobile.
- Action buttons (Register, Test vs AI, Delete) stack vertically on narrow viewports.
- The Game Day History collapsible section continues to use client-side pagination.

#### TankEditor — `pages/TankEditor.tsx`

The code editor is the hardest surface. Two options are acceptable:

| Option | When to use | Behaviour |
|---|---|---|
| **Full-screen mobile editor mode** | Mobile (< 640 px) | Monaco editor expands to fill the viewport; stat config panel slides in from the bottom or is accessed via a tab; the preamble banner is hidden by default (accessible via a toggle); Save & Validate button is fixed at the bottom. |
| **View-only / read-only mode** | Mobile (< 640 px) | Editor renders as a non-editable code block; all write actions (Save & Validate, Promote, Register) are disabled with an explanatory message ("Use a desktop browser to edit tank code"). |

Either option must be explicitly chosen during implementation and documented inline. On **tablet** viewports the editor may retain full functionality at reduced width; horizontal scroll within Monaco is acceptable.

The stat config panel and compile status bar remain visible alongside the editor on tablet and desktop.

#### Watch / ObserverHUD — `pages/Watch.tsx`, `game/ObserverHUD.tsx`

- The Phaser arena canvas in `game/ObserverScene.ts` must **scale to fit** the available viewport width while preserving the maze aspect ratio. On mobile the canvas fills the full screen width; on tablet it fills the available column width.
- The HUD overlay (HP bars, tick counter, speed controls, debug panel toggle) repositions below the canvas on mobile, rather than overlaying it. Alternatively, the HUD can be rendered as a semi-transparent floating bar that does not obscure the center of the canvas.
- Playback control buttons (play/pause, step, speed selector) must meet the 44 × 44 px minimum touch target size (§15.5).
- The debug panel remains collapsible; on mobile it opens in a bottom sheet or modal overlay rather than inline beside the canvas.

#### Replay — same URL as Watch with `&replay=true`

Same responsive rules as Watch / ObserverHUD apply. The scrubber timeline should scroll horizontally on narrow viewports rather than compress to unreadable size.

#### Leaderboard — `pages/Leaderboard.tsx`

- The full leaderboard table has too many columns to display on a single mobile screen. Two acceptable approaches:
  - **Horizontal scroll**: wrap the table in a `overflow-x: auto` container so the user can swipe to see all columns.
  - **Card layout**: on mobile, replace each table row with a card showing Tank name, Author, Score, and Rank; hide secondary columns (Best Finish, Game Days, Last Active).
- Client-side pagination controls remain at the bottom.

#### Game Day — `pages/GameDay.tsx`

- The round-robin cross-table grid (item 168) should scroll horizontally on mobile (`overflow-x: auto`) rather than compress cells below readability.
- The elimination bracket (items 169, 142) retains its existing horizontal pagination (3 rounds per page) which already limits width; each `SlotCell` shrinks to a narrower fixed width on mobile (minimum 120 px per slot).
- The phase timeline stacks vertically on mobile (already likely a single column).

#### Account — `pages/Account.tsx` (item 184)

- Single-column layout on all viewports; progress bars are full-width.
- The tier comparison table (if shown on the Upgrade path) scrolls horizontally on mobile.

### 15.4 Tables — General Rule

All multi-column tables throughout the app (leaderboard, round-robin standings, admin panels) must not overflow their container on narrow viewports. The default approach is `overflow-x: auto` on the table wrapper. Card layout is an acceptable alternative where it improves readability.

### 15.5 Touch Targets

All interactive elements — buttons, links, tab controls, checkboxes, radio buttons — must meet a minimum hit area of **44 × 44 px** on touch devices. Elements that are visually smaller (e.g. compact checkboxes in the preamble banner) must have an invisible padding zone that brings their touch target up to the minimum.

### 15.6 Scope Constraints

- This is a **frontend-only** change. No backend Lambda, DynamoDB schema, CDK stack, or API contract changes are required or permitted under this feature.
- The desktop experience must be preserved unchanged at ≥ 1024 px.
- The Monaco editor WASM bundle size is not affected; the responsive feature gates whether Monaco is shown, not whether it is loaded.

---

## 16. Google AdSense Integration

TankMaze displays Google AdSense ad units on all public-facing pages to generate ad revenue. Ads are injected only when enabled via the admin configuration panel — no code deployment is required to change ad settings.

### 16.1 Ad Placements

Two ad slots appear on every public-facing page:

| Slot | Position | Format |
|---|---|---|
| **Top bar** | Horizontal leaderboard bar, below the main navigation header | Horizontal (e.g. 728×90 or responsive leaderboard) |
| **Right rail** | Vertical rectangle, right side of page content | Vertical (e.g. 160×600 or 300×600) |

**Pages that show ads (public-facing):**

| Page | Route |
|---|---|
| Dashboard | `/` or `/dashboard` |
| TankDetail | `/tanks/{id}` |
| TankEditor | `/tanks/{id}/edit` |
| Watch / ObserverHUD | `/watch` |
| Replay | `/watch?…&replay=true` |
| Leaderboard | `/leaderboard` |
| GameDay | `/gamedays/{id}` |
| Account | `/account` |

**Pages that never show ads:**

- All admin routes (`/admin/*`)
- Login page (`/login`)

### 16.2 Ad Rendering

Ads use the standard Google AdSense asynchronous script tag and ad unit `<div>` elements:

```html
<!-- AdSense script — injected once per page load when ads are enabled -->
<script async
  src="https://pagead2.googlesyndication.com/pagead/js/adsbygoogle.js?client=<publisherId>"
  crossorigin="anonymous">
</script>

<!-- Ad unit div example -->
<ins class="adsbygoogle"
  data-ad-client="<publisherId>"
  data-ad-slot="<slotId>"
  data-ad-format="auto"
  data-full-width-responsive="true">
</ins>
<script>(adsbygoogle = window.adsbygoogle || []).push({});</script>
```

**Enable/disable behavior:**

- When the global `enabled` toggle is **on**: the AdSense `<script>` tag is injected into the document `<head>` on every public page load; both ad unit `<div>` elements are rendered in the layout.
- When the global `enabled` toggle is **off**: **no** AdSense script is injected and **no** ad unit `<div>` elements are rendered. The page layout is unchanged (the slots collapse to zero height).

The frontend reads ad configuration once at load time (or via a lightweight config endpoint) and makes a single conditional decision per page render. There is no real-time ad toggle — a page reload is required after an admin changes the setting.

### 16.3 Responsive Behavior

Per the responsive spec (§15):

| Slot | Mobile (< 640 px) | Tablet (640–1023 px) | Desktop (≥ 1024 px) |
|---|---|---|---|
| **Top bar** | Shown (full width) | Shown (full width) | Shown (full width) |
| **Right rail** | Hidden | Shown | Shown |
| **Bottom bar** | Shown (full width) | Hidden | Hidden |

On **mobile**, the right rail is replaced by two full-width horizontal units: the existing top bar (retained) and a new bottom bar rendered at the end of the page content, above the footer. The right-rail `<div>` is hidden via CSS media query (`display: none` at < 640 px); the bottom bar `<div>` is shown only on mobile (`display: none` at ≥ 640 px). The bottom bar uses a separate ad slot ID (`bottomSlotId`) configured in the admin CRUD.

The right-rail ad must not cause horizontal overflow or layout shifts on narrow viewports.

### 16.4 Ad Configuration (Admin CRUD)

Ad settings are managed exclusively through the admin area. There is no way to change ad configuration via environment variables or code deployment.

**Configuration fields:**

| Field | Type | Description |
|---|---|---|
| `publisherId` | string | Google AdSense publisher ID (`data-ad-client`), e.g. `ca-pub-XXXXXXXXXXXXXXXX` |
| `topSlotId` | string | Ad slot ID for the top bar unit (`data-ad-slot`) — shown on all viewports |
| `rightSlotId` | string | Ad slot ID for the right rail unit (`data-ad-slot`) — tablet and desktop only |
| `bottomSlotId` | string | Ad slot ID for the bottom bar unit (`data-ad-slot`) — mobile only, replaces right rail |
| `enabled` | boolean | Global toggle — `true` renders and scripts ads; `false` suppresses all ad output |

**Admin interface:**

A CRUD panel in the admin area (`/admin/ads` or as a section within the admin dashboard) allows `platform-admin` users to:

- **View** current ad configuration
- **Update** any field (publisher ID, slot IDs, enabled toggle)
- **Reset** to a disabled/blank state

Non-admin users have no access to this interface.

### 16.5 Storage

Ad configuration is stored in a DynamoDB table. If the existing `tankmaze-admin` or `tankmaze-user-settings` table has a suitable general-purpose config key-value structure, the ad config may be stored there as a single item (key: `"ad_config"`). Otherwise, a dedicated `tankmaze-platform-config` table is used.

**DynamoDB item shape:**

```json
{
  "configKey": "ad_config",
  "publisherId": "ca-pub-XXXXXXXXXXXXXXXX",
  "topSlotId": "1234567890",
  "rightSlotId": "0987654321",
  "bottomSlotId": "1122334455",
  "enabled": true
}
```

### 16.6 Frontend Config Endpoint

A new lightweight public endpoint `GET /config/ads` returns the ad configuration for the frontend:

```json
{
  "enabled": true,
  "publisherId": "ca-pub-XXXXXXXXXXXXXXXX",
  "topSlotId": "1234567890",
  "rightSlotId": "0987654321",
  "bottomSlotId": "1122334455"
}
```

The frontend fetches this endpoint at app load time (alongside auth init) and caches the result in React context or a Zustand store for the session. No subsequent refetch is performed within the same page session.

When `enabled` is `false`, the endpoint still returns the full config object with `enabled: false`; the frontend simply skips rendering the ad script and slot divs.

### 16.7 Scope Constraints

- Ad slots appear only on public-facing pages — never on `/admin/*` or `/login`.
- Ad slots cannot be individually toggled per-page in v1 — the global `enabled` flag controls all slots at once. On desktop, top bar and right rail render together; on mobile, top bar and bottom bar render together; the right rail is never shown on mobile.
- Payment to Google AdSense (billing, account creation) is outside the platform's scope — this spec covers only the technical integration.
- Ad performance analytics (click-through rates, revenue tracking) are handled entirely by the AdSense dashboard — no in-platform analytics are required.

---

## 14. Out of Scope (v1)

- Languages other than Go (Python, Rust, TypeScript — future via WASM compilation)
- More than 2 tanks per match
- Power-ups or collectibles
- Non-grid (free-movement) navigation
- Mobile native app
- Tank-to-tank communication (multi-tank alliances)
- Subscription payment processing (payment integration is deferred; tier limits and enforcement are in scope, payment collection is not)

---

## 17. Friends & Social

TankMaze includes a lightweight social layer independent of Game Day competition: Tank Authors can befriend each other, block unwanted contact, and exchange direct messages with accepted friends. This is a companion feature to the public Tank Author profile (`pages/UserProfile.tsx`) — friend actions and messaging both originate from that page and from the dedicated Friends page (`pages/Friends.tsx`).

### 17.1 Relationship Model

A relationship between two users is stored as **one row per direction** in the `tankmaze-friendships` table — `(userId=A, friendId=B)` and `(userId=B, friendId=A)` — rather than a single canonicalized pair-key. This lets a lookup from either side of the relationship be a plain `GetItem`/`Query` against that user's own partition, with no GSI required (`packages/backend/internal/db/friendships.go`).

```
friendships[userId][friendId] = {
  userId, friendId,
  status:      "pending" | "accepted" | "blocked",
  requestedBy: "<userId who initiated the request, or who placed the block>",
  createdAt:   <unix timestamp>
}
```

| Status | Meaning | Set by |
|---|---|---|
| `pending` | A friend request has been sent and not yet answered | `SendFriendRequest` — writes both directions |
| `accepted` | Both users are friends | `AcceptFriendRequest` — flips both directions |
| `blocked` | One user has blocked the other | `BlockUser` — reuses the same table/dual-item model rather than a separate blocks table |

`requestedBy` is dual-purpose: for a `pending` row it records who sent the request (so the recipient's side can be told apart from the sender's side); for a `blocked` row it is repurposed to record **who placed the block**, which is what lets `UnblockUser` enforce that only that person may lift it.

Deleting both direction-rows is the single underlying operation (`RemoveFriendship`) behind three different user actions: rejecting an incoming request, cancelling an outgoing request, and unfriending an accepted friend. All three are "delete the pairing" from the data model's point of view.

### 17.2 Friend Requests

**Send** — `POST /friends/requests { toUserId }` (`sendFriendRequest`, from a Tank Author's profile page):
- Rejected with `400` if `toUserId` is empty or equal to the caller's own ID (no self-friending).
- The backend looks up the existing relationship from the caller's side first:
  - If a `blocked` row exists (in **either** direction — see §17.3), the request is rejected with a generic `403 "unable to send friend request"`. The error is deliberately non-specific: it never reveals *which* of the two users placed the block.
  - If already `accepted`, rejected with `409 "already friends"`.
  - If already `pending` (either direction), rejected with `409 "friend request already pending"`.
  - Otherwise, `SendFriendRequest` writes a `pending` row on both sides with `requestedBy` set to the sender.

**Accept / Reject** — `POST /friends/requests/{fromUserId}/accept` and `.../reject` (`respondFriendRequest` handles both, sharing one code path):
- The caller must have a `pending` relationship with `fromUserId` where `requestedBy != caller` — i.e. it must be an **incoming** request. Responding to your own outgoing request, or to a non-existent request, fails (`404` if no relationship exists at all, `409 "no pending incoming request from this user"` otherwise).
- Accept flips both direction-rows to `accepted` (`AcceptFriendRequest`).
- Reject deletes both direction-rows (`RemoveFriendship`) — a rejected request leaves no trace of ever having existed.

**Cancel** (an outgoing request the caller sent) — reuses the same endpoint as removing a friend: `DELETE /friends/{friendId}` (`removeFriend`, see §17.4). The handler doesn't distinguish "cancel a pending outgoing request" from "unfriend an accepted friend" — both are `RemoveFriendship` on the caller's pairing with that user.

### 17.3 Removing a Friend

`DELETE /friends/{friendId}` deletes both direction-rows of the relationship regardless of its current status (`pending` or `accepted`). This single endpoint backs three UI actions:
- **Remove friend** on an accepted friendship (Friends page and UserProfile).
- **Cancel** an outgoing request the caller sent (Friends page "Sent requests" section).
- **Cancel request** from the target's UserProfile page while `friendStatus === 'outgoing'`.

Removing a friendship is unilateral — either party can end it, and it takes effect immediately with no confirmation step from the other side.

### 17.4 Blocking & Unblocking

**Block** — `POST /friends/block { targetUserId }` (`blockUser`):
1. Rejected with `400` if `targetUserId` is empty or equal to the caller's own ID.
2. `BlockUser` first calls `RemoveFriendship` to delete **any existing relationship** between the two users — an existing friendship, or a pending request in either direction, is torn down as a side effect of blocking (equivalent to an implicit unfriend).
3. It then writes a fresh `blocked`-status pair on both direction-rows, with `requestedBy` set to the blocker.
4. Once blocked, the target cannot send a new friend request (§17.2) and neither user can message the other (§17.5) — `canMessage` requires `accepted` status, which a block precludes by construction.

**Unblock** — `POST /friends/unblock { targetUserId }` (`unblockUser`):
- Looks up the relationship from the caller's side; `404 "no block exists"` if there is none, or if it exists but isn't `blocked` status.
- **Only the user who placed the block may lift it.** If `requestedBy != caller`, the backend returns `403` with `db.ErrNotBlocker` ("only the user who placed the block can unblock") — the blocked party has no way to remove the block themselves.
- Otherwise `RemoveFriendship` deletes both rows, returning the pair to a clean, relationship-free state. A new friend request can then be sent normally.

**Visibility asymmetry:** `listFriends` (§17.6) only surfaces a `blocked` row in the caller's own `blocked` bucket **if `requestedBy == caller`** — i.e. a user only ever sees blocks *they* placed. The person who got blocked never sees "blocked" reflected anywhere in their own friend list; their friend-request and message attempts against that user simply fail with the same generic errors any stranger would get, so the block itself is never revealed to them.

On `UserProfile.tsx`, this asymmetry drives the `friendStatus` state machine: `'blocked'` is only ever set from the *viewer's* perspective (checking whether the profile's `sub` appears in the viewer's own `blocked` list). When blocked, the profile shows a single **"Blocked · Unblock"** button in place of the normal Add/Remove/Block controls.

### 17.5 Direct Messaging

Messaging is restricted to **accepted, non-blocked friends only**. Because blocking always tears down any existing friendship first (§17.4), an `accepted` status is sufficient proof that no block exists between the two users — `canMessage` does not need a separate block check:

```go
// canMessage — packages/backend/cmd/tank-api/main.go
func (h *handler) canMessage(ctx, uid, otherID) (bool, error) {
    f, err := h.store.GetFriendship(ctx, uid, otherID)
    if errors.Is(err, db.ErrNotFound) { return false, nil }
    if err != nil { return false, err }
    return f.Status == db.FriendshipAccepted, nil
}
```

**Send** — `POST /messages { toUserId, body }` (`sendMessage`):
- `400` if `toUserId` or `body` (after trimming) is empty.
- `400` if `body` exceeds **2000 characters** (`maxMessageBodyLen`).
- `403 "you can only message accepted friends"` if `canMessage` returns false.
- On success, `Store.SendMessage` writes the message and returns it (`201`).

**List** — `GET /messages/{userId}?since=<messageId>` (`listMessages`):
- Same `canMessage` gate as send; `403` if the two users aren't accepted friends.
- Without `since`: returns the most recent page (limit **50**), fetched by scanning backward and re-sorting ascending so the response is always chronological.
- With `since=<messageId>`: returns everything strictly after that cursor (limit **200**, to avoid missing a burst of messages between polling intervals).
- Chat is **polling-based, not WebSocket push** — `pages/Chat.tsx` calls `listMessages(userId, since)` on an interval while a conversation is open, and calls `sendMessage` on submit.

**Message data model** (`packages/backend/internal/db/messages.go`):

| Field | Description |
|---|---|
| `conversationId` | Partition key — the two participants' user IDs, sorted and joined with `#` (`ConversationID(a, b)`), so either side computes the same key regardless of who sends |
| `messageId` | Sort key — zero-padded millisecond timestamp + random hex suffix, so lexicographic order matches chronological order even for same-millisecond messages |
| `senderId` / `recipientId` | The two participants |
| `body` | Message text (≤ 2000 chars) |
| `sentAt` | Unix timestamp |
| `ttl` | `sentAt + 30 days` — messages TTL-expire automatically via DynamoDB TTL; there is no manual delete UI |

**Unread indicator:** `listFriends` (§17.6) populates each accepted-friend entry with `lastMessageAt` / `lastMessageFromMe` via `GetLatestMessage` (the single most recent message in that conversation). The frontend (`utils/chatUnread.ts`, `isUnread()`) uses this to render a small orange unread-dot on the "Message" link on the Friends page — there is no dedicated read-receipt schema; "unread" is inferred purely from "the latest message wasn't from me."

### 17.6 Listing Friends (`GET /friends`)

`listFriends` queries the caller's own partition (`ListFriendships`) — every direction-row involving the caller, of any status — and buckets it into four groups in a single pass:

| Bucket | Condition |
|---|---|
| `blocked` | `status == blocked` **and** `requestedBy == caller` (blocks the caller placed only — see §17.4 asymmetry) |
| `friends` | `status == accepted` — includes `lastMessageAt` / `lastMessageFromMe` when a conversation exists |
| `outgoing` | `status == pending` and `requestedBy == caller` (requests the caller sent) |
| `incoming` | `status == pending` and `requestedBy != caller` (requests the caller received) |

Each entry resolves the other user's display name and picture (`resolveUserDisplay`) so the frontend never needs a separate profile lookup per row.

### 17.7 API Reference

| Method & Path | Handler | Purpose |
|---|---|---|
| `GET /friends` | `listFriends` | Bucketed list: friends, incoming, outgoing, blocked |
| `POST /friends/requests` | `sendFriendRequest` | Send a friend request — body `{ toUserId }` |
| `POST /friends/requests/{fromUserId}/accept` | `respondFriendRequest(accept=true)` | Accept an incoming request |
| `POST /friends/requests/{fromUserId}/reject` | `respondFriendRequest(accept=false)` | Reject an incoming request |
| `DELETE /friends/{friendId}` | `removeFriend` | Remove a friend, or cancel an outgoing request |
| `POST /friends/block` | `blockUser` | Block a user — body `{ targetUserId }` |
| `POST /friends/unblock` | `unblockUser` | Unblock a user (blocker only) — body `{ targetUserId }` |
| `POST /messages` | `sendMessage` | Send a direct message — body `{ toUserId, body }` |
| `GET /messages/{userId}?since=<messageId>` | `listMessages` | Fetch conversation history, or new messages since a cursor |

All endpoints require an authenticated caller (`401` if `userID(req)` is empty); none are accessible to Observers.

### 17.8 UI Surfaces

**Friends page** (`pages/Friends.tsx`) — three sections, shown only when non-empty:
- **Friend requests** (incoming) — each row has **Accept** / **Decline** buttons.
- **Friends** — each row has a **Message** link (routes to `/chat/{userId}`, shows the unread dot from §17.5) and a **Remove** button.
- **Sent requests** (outgoing) — each row has a **Cancel** button (calls `removeFriend`).

**UserProfile page** (`pages/UserProfile.tsx`) — the profile of any other Tank Author shows a friend-status–dependent action cluster, derived by cross-referencing `listFriends()` against the viewed `sub`:

| `friendStatus` | Buttons shown |
|---|---|
| `none` | **Add friend** |
| `outgoing` | **Cancel request** |
| `incoming` | **Accept** / **Decline** |
| `friends` | **Remove friend** |
| `blocked` | **Blocked · Unblock** (replaces all other controls) |

A low-key **Block user** text action is always available alongside the primary buttons (except when already `blocked`, where Unblock is the only control shown). Friend-action errors surface inline beneath the action cluster rather than as a toast.

### 17.9 Access & Privacy Rules

| Rule | Enforcement point |
|---|---|
| A user cannot send a friend request to, or message, someone who has blocked them (or whom they've blocked) | `sendFriendRequest` (generic 403), `canMessage` (accepted-only gate) |
| Only the blocker can unblock | `UnblockUser` / `db.ErrNotBlocker` |
| A blocked user is never told they've been blocked | `listFriends` blocked-bucket filter (`requestedBy == caller` only); generic error messages on request/message attempts |
| Messages are only ever readable by the two participants | `canMessage` gate on both `sendMessage` and `listMessages`; there is no admin or observer read path for chat content |
| Messages are not retained indefinitely | DynamoDB TTL, 30 days from `sentAt`, no manual delete |

---

## 18. Administration

Users in the Cognito `platform-admin` group get an `/admin` area with tools for user support, tank moderation, ad configuration, and Game Day roster management. Nothing here confers any in-game advantage — it's operational tooling, not gameplay.

### 18.1 User Management (`/admin/users`)

| Column / Action | Description |
|---|---|
| Name / Email | The user's display name and account email |
| Status | Active / Disabled, toggled by the admin (a disabled user cannot sign in) |
| Admin | Checkbox toggling `platform-admin` membership — an admin cannot demote themselves |
| Tier | Subscription tier (§13), directly editable by an admin |
| **IdP** | Which sign-in method the account uses (§2.1) — `Email/Password`, `Google`, `Facebook`, `GitHub`, or `Discord` |
| **First seen** | Account creation date |
| **Last seen** | Most recent sign-in date. Falls back to displaying "First seen" until a genuine second sign-in has occurred — a brand-new federated account's very first sign-in is a signup event from Cognito's point of view, not an authentication event, so there is nothing to distinguish it from account creation until the *next* sign-in |
| **Tanks** | Tank count vs. the account's tier limit, e.g. `3/5` |
| Delete | Force-deletes the user and all of their tanks |

### 18.2 Tank Management (`/admin/tanks`)

Admins can rename or force-delete any tank regardless of owner, and force-reset a tank version stuck in a `"compiling"` state back to a resolvable status.

### 18.3 Game Day Roster Management

From a Game Day's page, an admin can manually add or remove individual tanks from the roster (§6.2 covers an Author's own registration; this is the admin override for building out a field). A **"Add all tanks"** action registers every eligible user-owned tank in a single click instead of adding them one at a time:

- **Eligible**: the tank has at least one promoted major version with `compileStatus == "ready"` — its highest such version is used.
- **Skipped**: tanks already on the roster, and tanks with no ready major version (shown as a count in a confirmation step before submitting, along with how many tanks will actually be added).
- Built-in AI tanks (Scout/Bruiser/Ranger/Randy) have their own separate one-click quick-add list and aren't affected by this action.

### 18.4 Ad Configuration

See §16.4 — the AdSense publisher ID, per-placement slot IDs, and the global enabled toggle are managed entirely through `/admin/ads`.

---
