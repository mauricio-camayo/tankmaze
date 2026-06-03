// Bruiser — built-in AI tank for TankMaze testing.
//
// Compile: GOOS=wasip1 GOARCH=wasm GOWORK=off go build -o bruiser.wasm .
package main

import (
	"encoding/json"
	"unsafe"

	tankmaze "github.com/tankmaze/sdk"
)

// Config declares Bruiser's stat allocation (must sum to 15).
//
// Speed 2    → moveCooldown = 250 ms ≈ 2–3 ticks  (must respect s.MoveCooldown)
// Sensor 2   → visibility up to 4 cells
// Damage 5   → 50 HP per projectile
// Armor 5    → 50% damage reduction
// FireRate 1 → fireCooldown = 2000 ms = 20 ticks  (fire only on contact)
var Config = tankmaze.TankConfig{
	Name:        "Bruiser",
	Speed:       2,
	SensorRange: 2,
	Damage:      5,
	Armor:       5,
	FireRate:    1,
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

var (
	targetRow   int
	targetCol   int
	initialized bool
	wandering   bool // true once we reach the initial target corner or see the opponent
)

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
	// First tick: infer the opponent's start corner from own spawn position.
	if !initialized {
		initialized = true
		if s.Position.Y > 5 {
			targetRow, targetCol = 1, 1
		} else {
			targetRow, targetCol = 23, 23
		}
	}

	// ---- Priority 1: fire on contact ----
	if s.ProximityAlert && s.FireCooldown == 0 {
		return tankmaze.Action{Type: tankmaze.Fire}
	}

	// ---- Priority 2: direct pursuit when bearing is known ----
	if s.OpponentBearing != nil {
		wandering = true
		toward := cardinalFromBearing(*s.OpponentBearing)
		chosen := pickDir(s, toward)
		if s.Facing != chosen {
			return rotateToward(s.Facing, chosen)
		}
		if s.MoveCooldown == 0 {
			return tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
		}
		return tankmaze.Action{Type: tankmaze.Idle}
	}

	// ---- Priority 3: navigate ----
	// Once within 2 cells of the target corner, switch to free exploration.
	if !wandering {
		dr := targetRow - s.Position.Y
		dc := targetCol - s.Position.X
		if iabs(dr) <= 2 && iabs(dc) <= 2 {
			wandering = true
		}
	}

	// Choose a bias direction: toward the target while charging, forward while wandering.
	var biasDir tankmaze.Direction
	if !wandering {
		dr := targetRow - s.Position.Y
		dc := targetCol - s.Position.X
		biasDir = primaryDir(dr, dc)
	} else {
		biasDir = s.Facing // prefer keeping current heading
	}

	chosen := pickDir(s, biasDir)
	if s.Facing != chosen {
		return rotateToward(s.Facing, chosen)
	}
	if s.MoveCooldown == 0 {
		return tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
	}
	return tankmaze.Action{Type: tankmaze.Idle}
}

// pickDir returns the best available open direction starting from primary,
// then trying clockwise, counterclockwise, and finally the opposite direction.
// This ensures Bruiser always finds a way through any wall layout.
func pickDir(s tankmaze.Sensors, primary tankmaze.Direction) tankmaze.Direction {
	candidates := [4]tankmaze.Direction{
		primary,
		cwDir(primary),
		ccwDir(primary),
		oppositeDir(primary),
	}
	for _, d := range candidates {
		if s.WallDistances[d] > 0 {
			return d
		}
	}
	return primary // unreachable in a valid connected maze
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

func cwDir(d tankmaze.Direction) tankmaze.Direction {
	return clockwiseOrder[(clockwiseIndex(d)+1)%4]
}

func ccwDir(d tankmaze.Direction) tankmaze.Direction {
	return clockwiseOrder[(clockwiseIndex(d)+3)%4]
}

func oppositeDir(d tankmaze.Direction) tankmaze.Direction {
	return clockwiseOrder[(clockwiseIndex(d)+2)%4]
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
