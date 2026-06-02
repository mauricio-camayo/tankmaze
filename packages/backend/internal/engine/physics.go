package engine

import (
	"os"
	"strconv"

	tankmaze "github.com/tankmaze/sdk"
	"github.com/tankmaze/backend/internal/maze"
)

// ProjSpeedFromEnv reads PROJ_SPEED and returns the number of cells a
// projectile travels per tick. Falls back to 4 when unset or invalid.
func ProjSpeedFromEnv() int {
	v := os.Getenv("PROJ_SPEED")
	if v == "" {
		return 4
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 4
	}
	return n
}

// WallHitDamageFromEnv reads WALL_HIT_DAMAGE and returns the HP a tank loses
// when it attempts to move into a wall. Falls back to 1 when unset or invalid.
func WallHitDamageFromEnv() int {
	v := os.Getenv("WALL_HIT_DAMAGE")
	if v == "" {
		return 1
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 1
	}
	return n
}

// dirDelta maps a Direction to its (Δrow, Δcol) step.
// N = row-1, S = row+1, E = col+1, W = col-1.
var dirDelta = [4][2]int{
	tankmaze.N: {-1, 0},
	tankmaze.S: {1, 0},
	tankmaze.E: {0, 1},
	tankmaze.W: {0, -1},
}

// rotRight[d] is the direction after a clockwise (right) 90° turn from d.
var rotRight = [4]tankmaze.Direction{
	tankmaze.N: tankmaze.E,
	tankmaze.S: tankmaze.W,
	tankmaze.E: tankmaze.S,
	tankmaze.W: tankmaze.N,
}

// rotLeft[d] is the direction after a counter-clockwise (left) 90° turn from d.
var rotLeft = [4]tankmaze.Direction{
	tankmaze.N: tankmaze.W,
	tankmaze.S: tankmaze.E,
	tankmaze.E: tankmaze.N,
	tankmaze.W: tankmaze.S,
}

// opposite[d] is the direction directly behind d (used for Backward moves).
var opposite = [4]tankmaze.Direction{
	tankmaze.N: tankmaze.S,
	tankmaze.S: tankmaze.N,
	tankmaze.E: tankmaze.W,
	tankmaze.W: tankmaze.E,
}

// ---- Stat formulas --------------------------------------------------------

// moveCooldownFor returns the move cooldown in milliseconds for the given speed.
// Formula: 1000 / (speed × 2) = 500 / speed ms.
func moveCooldownFor(speed int) int { return 500 / speed }

// fireCooldownFor returns the fire cooldown in milliseconds for the given fireRate.
// Formula: 1000 / (fireRate × 0.5) = 2000 / fireRate ms.
func fireCooldownFor(fireRate int) int { return 2000 / fireRate }

// effectiveDamage computes damage after armor reduction.
// Formula: attackerDamage × 10 × (1 − defenderArmor × 10%).
func effectiveDamage(attackerDamage, defenderArmor int) int {
	return int(float64(attackerDamage*10) * (1.0 - float64(defenderArmor)*0.1))
}

// ---- Action processing ----------------------------------------------------

// applyAction applies the action for tank idx, recording the previous position
// before any movement so resolveCollision can detect swap-collisions.
// Returns any projectiles created this tick.
func (e *Engine) applyAction(idx int, action tankmaze.Action, crashed bool) []projectile {
	t := &e.tanks[idx]
	t.prevPos = t.pos // snapshot before potential move

	if t.hp <= 0 {
		return nil
	}
	if crashed {
		t.crashed = true
		return nil
	}

	switch action.Type {
	case tankmaze.Rotate:
		switch action.Direction {
		case tankmaze.Right:
			t.facing = rotRight[t.facing]
		case tankmaze.Left:
			t.facing = rotLeft[t.facing]
		}

	case tankmaze.Move:
		if t.moveCooldownMs > 0 {
			break
		}
		dir := t.facing
		if action.Direction == tankmaze.Backward {
			dir = opposite[t.facing]
		}
		d := dirDelta[dir]
		next := [2]int{t.pos[0] + d[0], t.pos[1] + d[1]}
		if isOpen(e.grid, next) {
			t.pos = next
			t.moveCount++
			t.moveCooldownMs = moveCooldownFor(t.cfg.Speed)
		} else if e.wallHitDamage > 0 {
			// Tank attempted to move into a wall — deal self-inflicted damage.
			t.hp = max(0, t.hp-e.wallHitDamage)
		}

	case tankmaze.Fire:
		if t.fireCooldownMs > 0 {
			break
		}
		opp := &e.tanks[1-idx]
		dmg := effectiveDamage(t.cfg.Damage, opp.cfg.Armor)
		t.fireCooldownMs = fireCooldownFor(t.cfg.FireRate)
		return []projectile{{pos: t.pos, facing: t.facing, owner: idx, damage: dmg}}
	}

	return nil
}

func isOpen(g maze.MazeGrid, pos [2]int) bool {
	r, c := pos[0], pos[1]
	return r >= 0 && r < g.Size && c >= 0 && c < g.Size && g.Cells[r][c]
}

// ---- Projectile movement --------------------------------------------------

// advanceProjectiles moves every in-flight projectile e.projSpeed cells and
// checks every intermediate cell for wall and tank impacts — a projectile
// cannot tunnel through a target by moving faster than 1 cell per tick.
// Projectiles fired this tick are not yet in the list (added in Step after
// this call), so they don't move until next tick.
func (e *Engine) advanceProjectiles() {
	live := e.projectiles[:0]
	for _, p := range e.projectiles {
		d := dirDelta[p.facing]
		destroyed := false
		for range e.projSpeed {
			next := [2]int{p.pos[0] + d[0], p.pos[1] + d[1]}
			if !isOpen(e.grid, next) {
				destroyed = true
				break
			}
			for ti := range e.tanks {
				if e.tanks[ti].hp <= 0 {
					continue
				}
				if e.tanks[ti].pos == next {
					e.tanks[ti].hp = max(0, e.tanks[ti].hp-p.damage)
					e.tanks[p.owner].damageDealt += p.damage
					destroyed = true
					break
				}
			}
			if destroyed {
				break
			}
			p.pos = next
		}
		if !destroyed {
			live = append(live, p)
		}
	}
	e.projectiles = live
}

// ---- Collision ------------------------------------------------------------

// resolveCollision detects and resolves tank-tank collisions after both
// tanks have applied their actions this tick. Two cases trigger a collision:
//
//   - Both tanks occupy the same cell (converged).
//   - The tanks swapped cells (passed through each other).
//
// On collision both tanks are pushed back to their pre-move positions and
// each takes 5 HP contact damage (ignoring armor).
func (e *Engine) resolveCollision() {
	posA, posB := e.tanks[0].pos, e.tanks[1].pos
	prevA, prevB := e.tanks[0].prevPos, e.tanks[1].prevPos

	converge := posA == posB
	swap := posA == prevB && posB == prevA

	if !converge && !swap {
		return
	}

	e.tanks[0].pos = prevA
	e.tanks[1].pos = prevB

	e.tanks[0].hp = max(0, e.tanks[0].hp-5)
	e.tanks[1].hp = max(0, e.tanks[1].hp-5)

	// Each tank dealt 5 HP to the other via contact.
	e.tanks[0].damageDealt += 5
	e.tanks[1].damageDealt += 5
}

// ---- Cooldowns ------------------------------------------------------------

func (e *Engine) decrementCooldowns() {
	for i := range e.tanks {
		if e.tanks[i].moveCooldownMs > 0 {
			e.tanks[i].moveCooldownMs = max(0, e.tanks[i].moveCooldownMs-TickMs)
		}
		if e.tanks[i].fireCooldownMs > 0 {
			e.tanks[i].fireCooldownMs = max(0, e.tanks[i].fireCooldownMs-TickMs)
		}
	}
}
