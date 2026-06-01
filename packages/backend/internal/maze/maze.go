package maze

import (
	"fmt"
	"math/rand"
)

// Size is the fixed dimension of every maze grid (rows and columns).
const Size = 25

// SpawnA and SpawnB are the fixed starting positions for the two tanks,
// at diagonally opposite inner corners of the playfield.
// The outer ring (row/col 0 and Size-1) is always wall, so the inner
// corners sit at index 1 and Size-2.
var (
	SpawnA = [2]int{1, 1}
	SpawnB = [2]int{Size - 2, Size - 2}
)

// MazeGrid is the canonical maze representation used by both the generator
// and the static map loader. Cells[row][col] is true when the cell is open
// (passable) and false when it is a wall.
type MazeGrid struct {
	Cells [Size][Size]bool
}

// Generate creates a maze using recursive backtracking seeded with seed.
//
// Layout rules:
//   - The outer ring (row 0, row Size-1, col 0, col Size-1) is always wall.
//   - The interior uses a room+passage model: rooms sit at odd coordinates
//     (1,1),(1,3),…,(23,23); a passage between two adjacent rooms occupies
//     the cell between them.
//   - Every room is reachable from every other room (perfect maze, no loops).
//   - SpawnA (1,1) and SpawnB (23,23) are always open.
func Generate(seed int64) MazeGrid {
	rng := rand.New(rand.NewSource(seed))
	var g MazeGrid
	var visited [Size][Size]bool

	// Open the starting room and begin DFS.
	r0, c0 := SpawnA[0], SpawnA[1]
	g.Cells[r0][c0] = true
	visited[r0][c0] = true

	dirs := [4][2]int{{-2, 0}, {2, 0}, {0, -2}, {0, 2}}

	var dfs func(r, c int)
	dfs = func(r, c int) {
		// Shuffle directions in place using Fisher-Yates.
		rng.Shuffle(len(dirs), func(i, j int) { dirs[i], dirs[j] = dirs[j], dirs[i] })
		for _, d := range dirs {
			nr, nc := r+d[0], c+d[1]
			if nr < 1 || nr > Size-2 || nc < 1 || nc > Size-2 {
				continue
			}
			if visited[nr][nc] {
				continue
			}
			// Carve the passage between (r,c) and (nr,nc).
			g.Cells[(r+nr)/2][(c+nc)/2] = true
			g.Cells[nr][nc] = true
			visited[nr][nc] = true
			dfs(nr, nc)
		}
	}

	dfs(r0, c0)
	return g
}

// Load converts a 25×25 boolean layout (as stored in the tankmaze-maps
// DynamoDB table) into a MazeGrid. Returns an error if dimensions are wrong.
func Load(layout [][]bool) (MazeGrid, error) {
	if len(layout) != Size {
		return MazeGrid{}, fmt.Errorf("maze layout must have %d rows, got %d", Size, len(layout))
	}
	var g MazeGrid
	for r, row := range layout {
		if len(row) != Size {
			return MazeGrid{}, fmt.Errorf("maze layout row %d must have %d cols, got %d", r, Size, len(row))
		}
		for c, open := range row {
			g.Cells[r][c] = open
		}
	}
	return g, nil
}
