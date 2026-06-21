// Ranger — built-in AI tank for TankMaze testing.
//
// Compile: GOOS=wasip1 GOARCH=wasm GOWORK=off go build -o ranger.wasm .
package main

import (
	"encoding/json"
	"unsafe"

	tankmaze "github.com/tankmaze/sdk"
)

// Config declares Ranger's stat allocation (must sum to 15).
//
// Speed 3    → moveCooldown ≈ 166 ms ≈ 1–2 ticks
// Sensor 5   → visibility up to 10 cells (very long range)
// Damage 3   → 30 HP per projectile
// Armor 2    → 20% damage reduction
// FireRate 2 → fireCooldown ≈ 1000 ms ≈ 10 ticks
var Config = tankmaze.TankConfig{
	Name:        "Ranger",
	Speed:       3,
	SensorRange: 5,
	Damage:      3,
	Armor:       2,
	FireRate:    2,
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

// Patrol state
var (
	patrolDir     = tankmaze.N // current patrol heading
	patrolSteps   int          // ticks remaining on this leg
	lastKnownRow  int
	lastKnownCol  int
	hasLastKnown  bool
)

const patrolLegLen = 8 // ticks per patrol leg before turning

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
	// Update last known position whenever opponent is in sensor range.
	if s.OpponentBearing != nil {
		dr, dc := bearingDelta(*s.OpponentBearing)
		lastKnownRow = clamp(s.Position.Y+dr*Config.SensorRange, 1, 23)
		lastKnownCol = clamp(s.Position.X+dc*Config.SensorRange, 1, 23)
		hasLastKnown = true
	}

	// ---- Enemy visible: maintain 4-5 cell engagement distance ----
	//
	// WallDistances[toward] gives open cells in the enemy's cardinal direction.
	// When the corridor toward the enemy is short the enemy is close (retreat);
	// when it is long they are far (advance); 4-5 cells is the ideal standoff.
	if s.OpponentBearing != nil {
		toward := cardinalFromBearing(*s.OpponentBearing)
		away := oppositeDir(toward)
		clearPath := s.WallDistances[toward]

		switch {
		case clearPath < 4:
			// Too close — retreat while shooting opportunistically.
			if s.FireCooldown == 0 && s.Facing == toward {
				return tankmaze.Action{Type: tankmaze.Fire}
			}
			retreatDir := pickDir(s, away)
			if s.MoveCooldown == 0 {
				if s.Facing != retreatDir {
					return rotateToward(s.Facing, retreatDir)
				}
				return tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
			}
			// Move on cooldown: face enemy and fire if ready.
			if s.FireCooldown == 0 {
				if s.Facing != toward {
					return rotateToward(s.Facing, toward)
				}
				return tankmaze.Action{Type: tankmaze.Fire}
			}
			return tankmaze.Action{Type: tankmaze.Idle}

		case clearPath > 5:
			// Too far — close the gap; fire en route if already aimed.
			chosen := pickDir(s, toward)
			if s.Facing == chosen {
				if s.FireCooldown == 0 {
					return tankmaze.Action{Type: tankmaze.Fire}
				}
				if s.MoveCooldown == 0 {
					return tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
				}
				return tankmaze.Action{Type: tankmaze.Idle}
			}
			return rotateToward(s.Facing, chosen)

		default:
			// 4 ≤ clearPath ≤ 5: ideal standoff — aim and fire; strafe on reload.
			if s.FireCooldown == 0 {
				if s.Facing == toward {
					return tankmaze.Action{Type: tankmaze.Fire}
				}
				return rotateToward(s.Facing, toward)
			}
			// Strafe perpendicular while reloading to make Ranger harder to hit.
			strafeDir := pickDir(s, cwDir(toward))
			if s.MoveCooldown == 0 && strafeDir != away {
				if s.Facing != strafeDir {
					return rotateToward(s.Facing, strafeDir)
				}
				return tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
			}
			return tankmaze.Action{Type: tankmaze.Idle}
		}
	}

	// ---- No enemy in range: reposition toward last known location ----
	if hasLastKnown {
		dr := lastKnownRow - s.Position.Y
		dc := lastKnownCol - s.Position.X
		if iabs(dr) > 2 || iabs(dc) > 2 {
			targetDir := primaryDir(dr, dc)
			chosen := pickDir(s, targetDir)
			if s.Facing != chosen {
				return rotateToward(s.Facing, chosen)
			}
			if s.MoveCooldown == 0 {
				return tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
			}
			return tankmaze.Action{Type: tankmaze.Idle}
		}
	}

	// ---- Fallback: systematic patrol ----
	patrolSteps--
	if patrolSteps <= 0 || s.WallDistances[patrolDir] == 0 {
		patrolDir = cwDir(patrolDir)
		patrolSteps = patrolLegLen
	}
	chosen := pickDir(s, patrolDir)
	if s.Facing != chosen {
		return rotateToward(s.Facing, chosen)
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
	return primary
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

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
