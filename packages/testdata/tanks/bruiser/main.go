// Bruiser — built-in AI tank for TankMaze testing.
//
// Stats: Speed 2 / SensorRange 2 / Damage 5 / Armor 5 / FireRate 1
// Strategy: charges in a straight line toward the detected opponent and fires
// the moment they are in range. When no bearing is known, keeps moving
// forward and turns right on walls.
//
// Compile: GOOS=wasip1 GOARCH=wasm go build -o bruiser.wasm .
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
	// Fire on contact — Bruiser's primary weapon is its damage stat.
	if s.ProximityAlert && s.FireCooldown == 0 {
		return tankmaze.Action{Type: tankmaze.Fire}
	}

	// Bearing known — rotate to face opponent then charge.
	if s.OpponentBearing != nil {
		toward := cardinalFromBearing(*s.OpponentBearing)
		if s.Facing != toward {
			return rotateToward(s.Facing, toward)
		}
		if s.WallDistances[s.Facing] > 0 && s.MoveCooldown == 0 {
			return tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
		}
		// Wall between Bruiser and the opponent — sidestep right, then left.
		rightDir := cwDir(s.Facing)
		if s.WallDistances[rightDir] > 0 {
			return tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Right}
		}
		leftDir := ccwDir(s.Facing)
		if s.WallDistances[leftDir] > 0 {
			return tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Left}
		}
		return tankmaze.Action{Type: tankmaze.Idle}
	}

	// No opponent in range — advance, turn right on walls.
	if s.WallDistances[s.Facing] > 0 && s.MoveCooldown == 0 {
		return tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
	}
	return tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Right}
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

func rotateToward(current, target tankmaze.Direction) tankmaze.Action {
	ci := clockwiseIndex(current)
	ti := clockwiseIndex(target)
	diff := (ti - ci + 4) % 4
	if diff == 1 {
		return tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Right}
	}
	return tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Left}
}
