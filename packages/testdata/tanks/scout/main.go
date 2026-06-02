// Scout — built-in AI tank for TankMaze testing.
//
// Compile: GOOS=wasip1 GOARCH=wasm GOWORK=off go build -o scout.wasm .
package main

import (
	"encoding/json"
	"unsafe"

	tankmaze "github.com/tankmaze/sdk"
)

// Config declares Scout's stat allocation (must sum to 15).
//
// Speed 5   → moveCooldown = 100 ms = 1 tick   (moves every tick)
// Sensor 3  → visibility up to 6 cells
// Damage 2  → 20 HP per projectile (reduced by opponent armor)
// Armor 2   → 20% damage reduction
// FireRate 3 → fireCooldown ≈ 666 ms ≈ 7 ticks
var Config = tankmaze.TankConfig{
	Name:        "Scout",
	Speed:       5,
	SensorRange: 3,
	Damage:      2,
	Armor:       2,
	FireRate:    3,
}

//go:wasmimport tankmaze sensors_get
//go:noescape
func sensorsGet(ptr unsafe.Pointer, cap int32) int32

//go:wasmimport tankmaze config_register
//go:noescape
func configRegister(ptr unsafe.Pointer, length int32)

//go:wasmimport tankmaze action_put
func actionPut(encoded int32)

func encode(a tankmaze.Action) int32 { return int32(a.Type)*10 + int32(a.Direction) }

// Last known opponent position (estimated from bearing + sensor range).
var (
	lastKnownRow int
	lastKnownCol int
	hasLastKnown bool
)

var cfgJSON = func() []byte { b, _ := json.Marshal(Config); return b }()

func main() {
	// Register this tank's config with the host before entering the game loop.
	configRegister(unsafe.Pointer(&cfgJSON[0]), int32(len(cfgJSON)))

	buf := make([]byte, 4096)
	for {
		n := sensorsGet(unsafe.Pointer(&buf[0]), int32(len(buf)))
		if n < 0 {
			return
		}
		var s tankmaze.Sensors
		if err := json.Unmarshal(buf[:n], &s); err != nil {
			actionPut(encode(tankmaze.Action{Type: tankmaze.Idle}))
			continue
		}
		actionPut(encode(tick(s)))
	}
}

func tick(s tankmaze.Sensors) tankmaze.Action {
	myRow := s.Position.Y
	myCol := s.Position.X

	// Refresh last known position whenever bearing is available.
	// Estimate at Config.SensorRange cells (midpoint of the 0–SensorRange×2 visible window).
	if s.OpponentBearing != nil {
		dr, dc := bearingDelta(*s.OpponentBearing)
		lastKnownRow = clamp(myRow+dr*Config.SensorRange, 1, 23)
		lastKnownCol = clamp(myCol+dc*Config.SensorRange, 1, 23)
		hasLastKnown = true
	}

	// ---- Priority 1: aim then fire ----
	// Rotate to face the opponent first; fire on the next tick when already aimed.
	// FireRate 3 → cooldown ≈ 7 ticks, so Scout interrupts the orbit briefly to shoot.
	if s.ProximityAlert && s.FireCooldown == 0 {
		toward := cardinalFromBearing(*s.OpponentBearing)
		if s.Facing == toward {
			return tankmaze.Action{Type: tankmaze.Fire}
		}
		return rotateToward(s.Facing, toward)
	}

	// ---- Priority 2: orbit at close range ----
	// Speed 5 → moveCooldown = 1 tick, so s.MoveCooldown == 0 on virtually every tick.
	if s.ProximityAlert && s.OpponentBearing != nil {
		orbitDir := orbitFacing(*s.OpponentBearing)
		if s.Facing != orbitDir {
			return rotateToward(s.Facing, orbitDir)
		}
		if s.WallDistances[s.Facing] > 0 && s.MoveCooldown == 0 {
			return tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
		}
		return tankmaze.Action{Type: tankmaze.Idle}
	}

	// ---- Priority 3: navigate to last known position and circle ----
	if hasLastKnown {
		dr := lastKnownRow - myRow
		dc := lastKnownCol - myCol
		if iabs(dr) <= 2 && iabs(dc) <= 2 {
			// At the estimated area — circle to search.
			if s.WallDistances[s.Facing] > 0 && s.MoveCooldown == 0 {
				return tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
			}
			return tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Right}
		}
		targetDir := primaryDir(dr, dc)
		if s.Facing != targetDir {
			return rotateToward(s.Facing, targetDir)
		}
		if s.WallDistances[s.Facing] > 0 && s.MoveCooldown == 0 {
			return tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
		}
		return tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Right}
	}

	// ---- Fallback: clockwise wall-following ----
	if s.WallDistances[s.Facing] > 0 && s.MoveCooldown == 0 {
		return tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
	}
	return tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Right}
}

// orbitFacing returns the cardinal to face for a clockwise orbit around an
// opponent at bearing b (90° clockwise rotation: N→E, E→S, S→W, W→N).
func orbitFacing(b tankmaze.Bearing) tankmaze.Direction {
	switch b {
	case tankmaze.BearingN, tankmaze.BearingNW:
		return tankmaze.E
	case tankmaze.BearingNE, tankmaze.BearingE:
		return tankmaze.S
	case tankmaze.BearingSE, tankmaze.BearingS:
		return tankmaze.W
	default: // SW, W
		return tankmaze.N
	}
}

func cardinalFromBearing(b tankmaze.Bearing) tankmaze.Direction {
	switch b {
	case tankmaze.BearingN, tankmaze.BearingNW:
		return tankmaze.N
	case tankmaze.BearingNE, tankmaze.BearingE:
		return tankmaze.E
	case tankmaze.BearingSE, tankmaze.BearingS:
		return tankmaze.S
	default:
		return tankmaze.W
	}
}

// bearingDelta returns the unit (Δrow, Δcol) step for an 8-compass bearing.
func bearingDelta(b tankmaze.Bearing) (dr, dc int) {
	switch b {
	case tankmaze.BearingN:
		return -1, 0
	case tankmaze.BearingNE:
		return -1, 1
	case tankmaze.BearingE:
		return 0, 1
	case tankmaze.BearingSE:
		return 1, 1
	case tankmaze.BearingS:
		return 1, 0
	case tankmaze.BearingSW:
		return 1, -1
	case tankmaze.BearingW:
		return 0, -1
	default: // NW
		return -1, -1
	}
}

// primaryDir returns the cardinal direction that most directly closes (dr, dc).
func primaryDir(dr, dc int) tankmaze.Direction {
	if iabs(dr) >= iabs(dc) {
		if dr < 0 {
			return tankmaze.N
		}
		return tankmaze.S
	}
	if dc > 0 {
		return tankmaze.E
	}
	return tankmaze.W
}

var clockwiseOrder = [4]tankmaze.Direction{tankmaze.N, tankmaze.E, tankmaze.S, tankmaze.W}

func clockwiseIndex(d tankmaze.Direction) int {
	for i, v := range clockwiseOrder {
		if v == d {
			return i
		}
	}
	return 0
}

func rotateToward(current, target tankmaze.Direction) tankmaze.Action {
	diff := (clockwiseIndex(target) - clockwiseIndex(current) + 4) % 4
	if diff == 1 {
		return tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Right}
	}
	return tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Left}
}

func iabs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
