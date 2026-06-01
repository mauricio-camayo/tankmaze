// Package testutil provides fixtures and helpers for match-runner integration tests.
package testutil

import (
	"time"

	"github.com/tankmaze/backend/internal/db"
	"github.com/tankmaze/backend/internal/maze"
)

// OpenMap returns a 25×25 MazeGrid with only outer boundary walls —
// the "Open" static map from the functional spec (§7.2).
// Every interior cell is passable; spawns sit at (1,1) and (23,23).
func OpenMap() maze.MazeGrid {
	const size = 25
	cells := make([][]bool, size)
	for r := range cells {
		cells[r] = make([]bool, size)
		for c := range cells[r] {
			cells[r][c] = r > 0 && r < size-1 && c > 0 && c < size-1
		}
	}
	return maze.MazeGrid{
		Size:   size,
		Cells:  cells,
		SpawnA: [2]int{1, 1},
		SpawnB: [2]int{size - 2, size - 2},
	}
}

// OpenMapRecord returns a db.Map record for the "Open" built-in static map.
// Useful for seeding a local DynamoDB or asserting map metadata in tests.
func OpenMapRecord() db.Map {
	grid := OpenMap()
	return db.Map{
		MapID:       "00000000-0000-0000-0000-000000000001",
		Slug:        "open",
		Name:        "Open",
		Description: "Only the outer boundary walls. Completely free interior — maximum movement freedom.",
		Layout:      grid.Cells,
		IsBuiltIn:   true,
		IsActive:    true,
		CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
	}
}
