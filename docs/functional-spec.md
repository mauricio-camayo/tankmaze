# TankMaze — Functional Specification

## 1. Overview

TankMaze is a real-time, browser-based tactical game where one or two players navigate a randomly generated labyrinth inside armored vehicles (tanks). Players have limited, sensor-based awareness of their surroundings — no full-map view. A third-party observer role provides a god's-eye view of the entire arena in real time.

---

## 2. User Roles

| Role | Description | Auth Required |
|---|---|---|
| **Player** | Controls a tank inside the labyrinth | Yes (Cognito) |
| **Observer** | Watches the game with full map visibility | No (session link) |

---

## 3. Game Modes

| Mode | Players | Description |
|---|---|---|
| Solo | 1 | Player vs AI-controlled opponent |
| Duel | 2 | Player vs Player |

---

## 4. Authentication & Session Flow

1. Player signs up / logs in via AWS Cognito (email + password or social provider).
2. Authenticated player creates a **game session** (generates a unique Session ID).
3. Second player joins using the Session ID, or AI is spawned for Solo mode.
4. Observer joins by navigating to `/?session=<ID>` — no login required.
5. Session is destroyed when all players disconnect or the game ends.

---

## 5. Labyrinth

- Grid-based: configurable size (default **25 × 25 cells**).
- Randomly generated per session using the **Recursive Backtracking** algorithm.
- Guaranteed to be fully connected (every cell reachable from every other).
- Start positions for both tanks are placed at opposite corners of the maze.
- The full maze layout is **never sent to players** — only sensor readings are sent.
- Observers receive the full maze layout on join.

### 5.1 Cell Types

| Cell | Description |
|---|---|
| Open | Passable space |
| Wall | Impassable; blocks movement and sensor beams |
| Spawn | Designated starting cell per player |

---

## 6. Vehicle System

Players choose one of three tank archetypes before the session starts. Each archetype distributes 15 stat points differently across five attributes.

### 6.1 Tank Archetypes

| Archetype | Speed | Sensor Range | Damage | Armor | Fire Rate | Playstyle |
|---|---|---|---|---|---|---|
| **Scout** | 5 | 3 | 2 | 2 | 3 | Hit-and-run; outmaneuver the opponent |
| **Ranger** | 3 | 5 | 3 | 2 | 2 | Intel advantage; control space with information |
| **Bruiser** | 2 | 2 | 5 | 5 | 1 | Absorb hits; eliminate with brute force |

Stat scale: 1 (lowest) → 5 (highest).

### 6.2 Derived Stat Values

| Stat | Scale (per point) |
|---|---|
| Speed | 1 pt = 1.0 cell/s movement rate |
| Sensor Range | 1 pt = 2 cells of ray-cast distance |
| Damage | 1 pt = 10 HP damage per projectile |
| Armor | 1 pt = 10% damage reduction |
| Fire Rate | 1 pt = 0.5 shots/s |

All tanks start with **100 HP**.

---

## 7. Movement & Controls

### 7.1 Player Input Actions

| Action | Effect |
|---|---|
| Move Forward | Advance 1 cell in facing direction (blocked by wall) |
| Move Backward | Retreat 1 cell opposite to facing direction |
| Rotate Left | Turn 90° counter-clockwise |
| Rotate Right | Turn 90° clockwise |
| Fire | Launch projectile in facing direction |

Movement is **discrete** (cell-to-cell). Speed stat controls the cooldown between moves (lower cooldown = faster movement).

### 7.2 Collision

- A move into a wall is rejected server-side; client receives a `MOVE_REJECTED` event.
- A move into the opponent's cell results in a collision — both tanks stop, and each takes 5 HP of contact damage.

---

## 8. Sensors

Players see only what their sensors reveal — not the full map.

### 8.1 Sensor Outputs (sent each tick)

| Sensor | Description |
|---|---|
| **Wall Distance** | Distances to nearest wall in 4 cardinal directions (in cells) |
| **Proximity Alert** | Boolean: opponent tank within sensor range |
| **Opponent Bearing** | Direction of opponent relative to own heading (if in range) |
| **Own Health** | Current HP |
| **Cooldowns** | Move cooldown remaining (ms), fire cooldown remaining (ms) |

Sensor data is computed server-side and sent only to the respective player. Observers receive all sensor data for both players plus the full map state.

---

## 9. Combat

- **Projectiles** travel cell-by-cell each server tick in the direction the tank was facing when fired.
- A projectile is destroyed upon hitting a wall or an opponent tank.
- On hit: `damage = archetype_damage × (1 − opponent_armor_reduction)`.
- A tank reaching 0 HP is **destroyed**. The session ends immediately.
- **Friendly fire**: not applicable in 2-player mode; in Solo mode the AI can also be hit.

---

## 10. Observer Mode

Observers connect via a shareable URL: `https://<domain>/observe?session=<ID>`

Observer receives (real-time via WebSocket):
- Full maze layout (walls and open cells)
- Both tanks: position, facing direction, HP, archetype
- Projectiles in flight: position, direction
- Game events: shots fired, hits, game start/end

Observers cannot interact with the game session.

---

## 11. Game Lifecycle

```
[Lobby] → [Tank Selection] → [Countdown 3s] → [Active Game] → [Game Over] → [Rematch? / Exit]
```

| State | Description |
|---|---|
| Lobby | Players join; waiting for both players ready |
| Tank Selection | Each player picks archetype (30s timer) |
| Countdown | 3-second start countdown |
| Active Game | Game ticks running; inputs accepted |
| Game Over | Winner announced; session stats displayed |

### 11.1 Win Conditions

| Condition | Winner |
|---|---|
| Opponent tank destroyed | Surviving player |
| Opponent disconnects | Remaining player (after 10s grace period) |
| Timeout (10 min) | Player with highest remaining HP |

---

## 12. Post-Game Stats

Displayed to both players and observers at game end:

- Winner / Loser
- Damage dealt / received
- Shots fired / hits landed (accuracy %)
- Total moves made
- Game duration

---

## 13. Out of Scope (v1)

- Persistent leaderboards
- More than 2 players
- Power-ups or collectibles
- Non-grid (free-movement) navigation
- Mobile native app
