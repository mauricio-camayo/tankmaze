// Randy — built-in AI tank for TankMaze testing.
//
// Compile: GOOS=wasip1 GOARCH=wasm GOWORK=off go build -o randy.wasm .
package main

import (
	"encoding/json"
	"unsafe"

	tankmaze "github.com/tankmaze/sdk"
)

// Config declares Randy's stat allocation (must sum to 15).
//
// Speed 3    → moveCooldown ≈ 166 ms ≈ 1–2 ticks
// Sensor 3   → visibility up to 6 cells
// Damage 3   → 30 HP per projectile
// Armor 3    → 30% damage reduction
// FireRate 3 → fireCooldown ≈ 666 ms ≈ 7 ticks
var Config = tankmaze.TankConfig{
	Name:        "Randy",
	Speed:       3,
	SensorRange: 3,
	Damage:      3,
	Armor:       3,
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

// Wander state — a simple LCG so Randy has deterministic-but-varied behaviour
// without importing math/rand (which requires extra SDK stdlib support).
var seed uint32 = 0xdeadbeef

func nextRand() uint32 {
	seed ^= seed << 13
	seed ^= seed >> 17
	seed ^= seed << 5
	return seed
}

// randDir returns a pseudo-random cardinal direction.
func randDir() tankmaze.Direction {
	return clockwiseOrder[nextRand()%4]
}

var (
	wanderDir   = tankmaze.N
	wanderSteps int // ticks remaining on the current wander heading
)

const wanderLegLen = 5 // ticks per wander leg before picking a new direction

var cfgJSON = func() []byte { b, _ := json.Marshal(Config); return b }()

func main() {
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
	// ---- Pursuit phase: opponent in sensor range ----
	if s.OpponentBearing != nil {
		toward := cardinalFromBearing(*s.OpponentBearing)

		// Fire on contact if ready and aimed.
		if s.ProximityAlert && s.FireCooldown == 0 && s.Facing == toward {
			return tankmaze.Action{Type: tankmaze.Fire}
		}
		// Rotate toward opponent.
		if s.Facing != toward {
			return rotateToward(s.Facing, toward)
		}
		// Move toward opponent.
		if s.MoveCooldown == 0 && s.WallDistances[s.Facing] > 0 {
			return tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
		}
		// Aimed and fired — wait or step aside.
		if s.FireCooldown == 0 {
			return tankmaze.Action{Type: tankmaze.Fire}
		}
		return tankmaze.Action{Type: tankmaze.Idle}
	}

	// ---- Wander phase: random movement ----
	// Pick a new random direction when the leg expires or a wall is ahead.
	wanderSteps--
	if wanderSteps <= 0 || s.WallDistances[wanderDir] == 0 {
		// Avoid the wall: pick a random open direction.
		for i := 0; i < 4; i++ {
			d := randDir()
			if s.WallDistances[d] > 0 {
				wanderDir = d
				break
			}
		}
		wanderSteps = wanderLegLen
	}

	if s.Facing != wanderDir {
		return rotateToward(s.Facing, wanderDir)
	}
	if s.MoveCooldown == 0 {
		return tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
	}
	return tankmaze.Action{Type: tankmaze.Idle}
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
