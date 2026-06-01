package engine

import (
	"math"

	tankmaze "github.com/tankmaze/sdk"
	"github.com/tankmaze/backend/internal/maze"
)

// computeSensors builds the Sensors struct passed to a tank's Tick function.
// Sensor range is sensorRange × 2 cells (the spec table has a typo; the
// multiplier applies to the sensorRange stat, not to speed).
func computeSensors(grid maze.MazeGrid, t, opp *tankState, tick int) tankmaze.Sensors {
	maxRange := t.cfg.SensorRange * 2

	dr := float64(t.pos[0] - opp.pos[0])
	dc := float64(t.pos[1] - opp.pos[1])
	dist := math.Sqrt(dr*dr + dc*dc)

	inRange := dist <= float64(maxRange)
	var bearing *tankmaze.Bearing
	if inRange {
		b := calcBearing(t.pos, opp.pos)
		bearing = &b
	}

	return tankmaze.Sensors{
		Facing:   t.facing,
		Position: tankmaze.Point{X: t.pos[1], Y: t.pos[0]}, // X = col, Y = row
		HP:       t.hp,
		WallDistances: map[tankmaze.Direction]int{
			tankmaze.N: raycast(grid, t.pos, tankmaze.N, maxRange),
			tankmaze.S: raycast(grid, t.pos, tankmaze.S, maxRange),
			tankmaze.E: raycast(grid, t.pos, tankmaze.E, maxRange),
			tankmaze.W: raycast(grid, t.pos, tankmaze.W, maxRange),
		},
		ProximityAlert:  inRange,
		OpponentBearing: bearing,
		MoveCooldown:    t.moveCooldownMs,
		FireCooldown:    t.fireCooldownMs,
		Tick:            tick,
	}
}

// raycast returns the number of open cells in direction dir before hitting a
// wall or the grid boundary, capped at maxRange.
//
// Examples (tank at pos, facing dir):
//
//	adjacent cell is wall        → 0  (cannot move)
//	adjacent open, next is wall  → 1  (can move once)
//	two open cells then wall     → 2  (can move twice)
func raycast(grid maze.MazeGrid, pos [2]int, dir tankmaze.Direction, maxRange int) int {
	d := dirDelta[dir]
	for i := 1; i <= maxRange; i++ {
		r := pos[0] + d[0]*i
		c := pos[1] + d[1]*i
		if r < 0 || r >= grid.Size || c < 0 || c >= grid.Size || !grid.Cells[r][c] {
			return i - 1
		}
	}
	return maxRange
}

// calcBearing returns the 8-compass direction from pos to target using atan2
// to determine the correct octant.
//
// Coordinate convention: row increases southward, col increases eastward.
func calcBearing(from, to [2]int) tankmaze.Bearing {
	dy := float64(to[0] - from[0]) // positive = south
	dx := float64(to[1] - from[1]) // positive = east

	angle := math.Atan2(dy, dx) // -π to π; 0 = East, π/2 = South
	if angle < 0 {
		angle += 2 * math.Pi // normalise to [0, 2π)
	}

	// Divide the circle into 8 sectors of π/4, starting from East.
	// Adding π/8 before truncating centres each sector on its cardinal/diagonal.
	sector := int((angle+math.Pi/8)/(math.Pi/4)) % 8

	// sector 0=E, 1=SE, 2=S, 3=SW, 4=W, 5=NW, 6=N, 7=NE
	fromEast := [8]tankmaze.Bearing{
		tankmaze.BearingE,
		tankmaze.BearingSE,
		tankmaze.BearingS,
		tankmaze.BearingSW,
		tankmaze.BearingW,
		tankmaze.BearingNW,
		tankmaze.BearingN,
		tankmaze.BearingNE,
	}
	return fromEast[sector]
}
