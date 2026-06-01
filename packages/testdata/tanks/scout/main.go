// Scout — built-in AI tank for TankMaze testing.
//
// Stats: Speed 5 / SensorRange 3 / Damage 2 / Armor 2 / FireRate 3
// Strategy: evades walls using clockwise wall-following; once the opponent
// enters sensor range, Scout orbits them clockwise while firing freely.
//
// Compile: GOOS=wasip1 GOARCH=wasm go build -o scout.wasm .
package main

import (
	"encoding/json"
	"unsafe"

	tankmaze "github.com/tankmaze/sdk"
)

// Host functions provided by the tankmaze module.

//go:wasmimport tankmaze sensors_get
//go:noescape
func sensorsGet(ptr unsafe.Pointer, cap int32) int32

//go:wasmimport tankmaze action_put
func actionPut(encoded int32)

func encode(a tankmaze.Action) int32 { return int32(a.Type)*10 + int32(a.Direction) }

func main() {
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
	// Fire whenever the opponent is nearby and the weapon is ready.
	if s.ProximityAlert && s.FireCooldown == 0 {
		return tankmaze.Action{Type: tankmaze.Fire}
	}

	// Opponent bearing known — orbit clockwise at the perimeter.
	if s.OpponentBearing != nil {
		orbitDir := orbitFacing(*s.OpponentBearing)
		if s.Facing != orbitDir {
			return rotateToward(s.Facing, orbitDir)
		}
		if s.WallDistances[s.Facing] > 0 && s.MoveCooldown == 0 {
			return tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
		}
		// Orbit direction blocked — face the opponent directly and wait for
		// a fire opportunity.
		toward := cardinalFromBearing(*s.OpponentBearing)
		if s.Facing != toward {
			return rotateToward(s.Facing, toward)
		}
		return tankmaze.Action{Type: tankmaze.Idle}
	}

	// No opponent in range — clockwise wall-following.
	if s.WallDistances[s.Facing] > 0 && s.MoveCooldown == 0 {
		return tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
	}
	return tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Right}
}

// orbitFacing returns the cardinal direction to face in order to orbit
// clockwise around an opponent located at the given bearing.
// It is the bearing rotated 90° clockwise: N→E, E→S, S→W, W→N.
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

// cardinalFromBearing maps an 8-compass bearing to the nearest cardinal direction.
func cardinalFromBearing(b tankmaze.Bearing) tankmaze.Direction {
	switch b {
	case tankmaze.BearingN, tankmaze.BearingNW:
		return tankmaze.N
	case tankmaze.BearingNE, tankmaze.BearingE:
		return tankmaze.E
	case tankmaze.BearingSE, tankmaze.BearingS:
		return tankmaze.S
	default: // SW, W
		return tankmaze.W
	}
}

// clockwiseOrder defines the four cardinals in clockwise rotation order.
var clockwiseOrder = [4]tankmaze.Direction{
	tankmaze.N, tankmaze.E, tankmaze.S, tankmaze.W,
}

// clockwiseIndex returns the position of d in the clockwise order.
func clockwiseIndex(d tankmaze.Direction) int {
	for i, v := range clockwiseOrder {
		if v == d {
			return i
		}
	}
	return 0
}

// rotateToward returns a single Rotate action that moves current one step
// toward target along the shorter arc.
func rotateToward(current, target tankmaze.Direction) tankmaze.Action {
	ci := clockwiseIndex(current)
	ti := clockwiseIndex(target)
	diff := (ti - ci + 4) % 4
	if diff == 1 || diff == 0 {
		return tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Right}
	}
	return tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Left}
}
