// Package engine implements the TankMaze game loop, physics, and win conditions.
// The match-runner Lambda drives the engine one tick at a time: it calls Sensors
// to get each tank's view of the world, invokes the WASM modules to obtain actions,
// then calls Step to advance the match.
package engine

import (
	tankmaze "github.com/tankmaze/sdk"
	"github.com/tankmaze/backend/internal/maze"
)

const TickMs = 100 // milliseconds per game tick

// Reason identifies how a match ended.
type Reason string

const (
	ReasonDestroyed      Reason = "opponent_destroyed"
	ReasonCodeCrash      Reason = "code_crash"
	ReasonDamageTiebreak Reason = "damage_tiebreak"
	ReasonMovesTiebreak  Reason = "moves_tiebreak"
	ReasonBothLose       Reason = "both_lose"
)

// Result is returned by Step when the match has ended.
// Winner is 0 (tank A), 1 (tank B), or -1 (both lose).
type Result struct {
	Winner       int
	Reason       Reason
	DamageA      int  // total damage dealt by A to B
	DamageB      int  // total damage dealt by B to A
	MovesA       int
	MovesB       int
	TicksElapsed int
	Flawless     bool // true only when winner destroyed opponent with zero damage received
}

// State is a read-only snapshot of the match at one tick, used by the match-runner
// to broadcast to observers and write tick logs.
type State struct {
	Tick        int
	Tanks       [2]TankSnapshot
	Projectiles []ProjSnapshot
}

// TankSnapshot is the observable state of one tank at one tick.
type TankSnapshot struct {
	Position [2]int
	Facing   tankmaze.Direction
	HP       int
}

// ProjSnapshot is the observable state of one in-flight projectile.
type ProjSnapshot struct {
	Position [2]int
	Facing   tankmaze.Direction
	Owner    int // 0 = tank A, 1 = tank B
}

type projectile struct {
	pos    [2]int
	facing tankmaze.Direction
	owner  int
	damage int // pre-computed effective damage
}

type tankState struct {
	cfg            tankmaze.TankConfig
	pos            [2]int
	prevPos        [2]int
	facing         tankmaze.Direction
	hp             int
	damageDealt    int
	moveCount      int
	moveCooldownMs int
	fireCooldownMs int
	crashed        bool
}

// Engine drives the match. Create with New; call Sensors then Step each tick.
type Engine struct {
	grid          maze.MazeGrid
	tanks         [2]tankState
	projectiles   []projectile
	tick          int
	tickLimit     int
	projSpeed     int // cells a projectile travels per tick
	wallHitDamage int // HP lost when a tank attempts to move into a wall
}

// New initialises a match. Tank A spawns at SpawnA facing South; tank B spawns
// at SpawnB facing North.
func New(grid maze.MazeGrid, cfgA, cfgB tankmaze.TankConfig, tickLimit, projSpeed, wallHitDamage int) *Engine {
	return &Engine{
		grid: grid,
		tanks: [2]tankState{
			newTank(cfgA, grid.SpawnA, tankmaze.S),
			newTank(cfgB, grid.SpawnB, tankmaze.N),
		},
		tickLimit:     tickLimit,
		projSpeed:     projSpeed,
		wallHitDamage: wallHitDamage,
	}
}

func newTank(cfg tankmaze.TankConfig, spawn [2]int, facing tankmaze.Direction) tankState {
	return tankState{cfg: cfg, pos: spawn, prevPos: spawn, facing: facing, hp: 100}
}

// Sensors computes the sensor reading for tank idx (0=A, 1=B) at the current state.
func (e *Engine) Sensors(idx int) tankmaze.Sensors {
	return computeSensors(e.grid, &e.tanks[idx], &e.tanks[1-idx], e.tick)
}

// State returns a snapshot of the current match state.
func (e *Engine) State() State {
	s := State{
		Tick: e.tick,
		Tanks: [2]TankSnapshot{
			{Position: e.tanks[0].pos, Facing: e.tanks[0].facing, HP: e.tanks[0].hp},
			{Position: e.tanks[1].pos, Facing: e.tanks[1].facing, HP: e.tanks[1].hp},
		},
	}
	for _, p := range e.projectiles {
		s.Projectiles = append(s.Projectiles, ProjSnapshot{p.pos, p.facing, p.owner})
	}
	return s
}

// Step advances the match by one tick.
//
// Tick processing order:
//  1. Advance existing projectiles; resolve wall/tank impacts.
//  2. Check for destruction from projectile damage.
//  3. Process both tanks' actions simultaneously (rotate / move / fire).
//  4. Resolve tank-tank collision.
//  5. Check for unrecoverable crashes.
//  6. Check for destruction from collision damage.
//  7. Decrement all cooldowns.
//  8. Increment tick counter; check tick limit.
//
// Projectiles fired this tick are added after step 3 and do not move until
// the following tick (projectiles advance in step 1 only).
//
// crashedA / crashedB should be true when the WASM module for that tank has
// failed unrecoverably (not just timed out on a single tick).
func (e *Engine) Step(actionA, actionB tankmaze.Action, crashedA, crashedB bool) *Result {
	// 1. Advance existing projectiles.
	e.advanceProjectiles()

	// 2. Check destruction from projectile impacts.
	if r := e.checkDestroyed(); r != nil {
		r.TicksElapsed = e.tick
		return r
	}

	// 3. Apply actions (both tanks simultaneously; prevPos saved inside).
	var newProj []projectile
	newProj = append(newProj, e.applyAction(0, actionA, crashedA)...)
	newProj = append(newProj, e.applyAction(1, actionB, crashedB)...)
	e.projectiles = append(e.projectiles, newProj...)

	// 4. Resolve tank-tank collision.
	e.resolveCollision()

	// 5. Check unrecoverable crashes.
	if r := e.checkCrashed(); r != nil {
		r.TicksElapsed = e.tick
		return r
	}

	// 6. Check destruction from collision damage.
	if r := e.checkDestroyed(); r != nil {
		r.TicksElapsed = e.tick
		return r
	}

	// 7. Decrement cooldowns.
	e.decrementCooldowns()

	// 8. Advance tick; check limit.
	e.tick++
	if e.tick >= e.tickLimit {
		return e.tiebreak()
	}
	return nil
}

// checkDestroyed returns a Result if at least one tank has HP ≤ 0.
func (e *Engine) checkDestroyed() *Result {
	aDown := e.tanks[0].hp <= 0
	bDown := e.tanks[1].hp <= 0
	switch {
	case aDown && bDown:
		return e.result(-1, ReasonBothLose)
	case aDown:
		return e.result(1, ReasonDestroyed)
	case bDown:
		return e.result(0, ReasonDestroyed)
	}
	return nil
}

// checkCrashed returns a Result if at least one tank is marked crashed.
func (e *Engine) checkCrashed() *Result {
	aC := e.tanks[0].crashed
	bC := e.tanks[1].crashed
	switch {
	case aC && bC:
		return e.result(-1, ReasonCodeCrash)
	case aC:
		return e.result(1, ReasonCodeCrash)
	case bC:
		return e.result(0, ReasonCodeCrash)
	}
	return nil
}

// tiebreak resolves a match that reached the tick limit.
func (e *Engine) tiebreak() *Result {
	dA, dB := e.tanks[0].damageDealt, e.tanks[1].damageDealt
	switch {
	case dA > dB:
		return e.result(0, ReasonDamageTiebreak)
	case dB > dA:
		return e.result(1, ReasonDamageTiebreak)
	case e.tanks[0].moveCount > e.tanks[1].moveCount:
		return e.result(0, ReasonMovesTiebreak)
	case e.tanks[1].moveCount > e.tanks[0].moveCount:
		return e.result(1, ReasonMovesTiebreak)
	default:
		return e.result(-1, ReasonBothLose)
	}
}

func (e *Engine) result(winner int, reason Reason) *Result {
	r := &Result{
		Winner:       winner,
		Reason:       reason,
		DamageA:      e.tanks[0].damageDealt,
		DamageB:      e.tanks[1].damageDealt,
		MovesA:       e.tanks[0].moveCount,
		MovesB:       e.tanks[1].moveCount,
		TicksElapsed: e.tick,
	}
	if reason == ReasonDestroyed && winner >= 0 {
		r.Flawless = e.tanks[winner].hp == 100
	}
	return r
}
