package maze

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
)

// DefaultSize is the maze dimension used when MAZE_SIZE is not set.
// Must be odd and ≥ 5 for the generator's room+passage model to work correctly.
const DefaultSize = 25

// SizeFromEnv reads the MAZE_SIZE environment variable and returns the maze
// dimension to use. Falls back to DefaultSize when the variable is absent,
// zero, or not an odd integer ≥ 5.
func SizeFromEnv() int {
	v := os.Getenv("MAZE_SIZE")
	if v == "" {
		return DefaultSize
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 5 || n%2 == 0 {
		return DefaultSize
	}
	return n
}

// MazeGrid is the canonical maze representation. Cells[row][col] is true when
// the cell is open (passable) and false when it is a wall. SpawnA and SpawnB
// are the starting positions for the two tanks (always open inner corners).
type MazeGrid struct {
	Size   int
	Cells  [][]bool
	SpawnA [2]int
	SpawnB [2]int
}

// NewGrid returns an all-wall MazeGrid of the given dimension with spawn points
// pre-computed. Use it to construct grids by hand (e.g. in tests); for mazes
// use Generate, for stored layouts use Load.
func NewGrid(size int) MazeGrid {
	return MazeGrid{
		Size:   size,
		Cells:  make2D(size),
		SpawnA: [2]int{1, 1},
		SpawnB: [2]int{size - 2, size - 2},
	}
}

// Generate creates a maze using recursive backtracking seeded with seed.
// size must be an odd integer ≥ 5; pass SizeFromEnv() to use the configured
// dimension.
//
// Layout rules:
//   - The outer ring (row 0, row size-1, col 0, col size-1) is always wall.
//   - The interior uses a room+passage model: rooms sit at odd coordinates
//     (1,1),(1,3),…,(size-2,size-2); a passage between two adjacent rooms
//     occupies the even-coordinate cell between them.
//   - Every room is reachable from every other room (perfect maze, no loops).
//   - SpawnA (1,1) and SpawnB (size-2,size-2) are always open.
func Generate(seed int64, size int) MazeGrid {
	g := NewGrid(size)
	rng := rand.New(rand.NewSource(seed))
	visited := make2D(size)

	r0, c0 := g.SpawnA[0], g.SpawnA[1]
	g.Cells[r0][c0] = true
	visited[r0][c0] = true

	dirs := [4][2]int{{-2, 0}, {2, 0}, {0, -2}, {0, 2}}

	var dfs func(r, c int)
	dfs = func(r, c int) {
		rng.Shuffle(len(dirs), func(i, j int) { dirs[i], dirs[j] = dirs[j], dirs[i] })
		for _, d := range dirs {
			nr, nc := r+d[0], c+d[1]
			if nr < 1 || nr > size-2 || nc < 1 || nc > size-2 {
				continue
			}
			if visited[nr][nc] {
				continue
			}
			g.Cells[(r+nr)/2][(c+nc)/2] = true
			g.Cells[nr][nc] = true
			visited[nr][nc] = true
			dfs(nr, nc)
		}
	}

	dfs(r0, c0)
	return g
}

// Load converts an N×N boolean layout (as stored in the tankmaze-maps DynamoDB
// table) into a MazeGrid. The dimension is inferred from the layout; returns
// an error if the layout is empty or not square.
func Load(layout [][]bool) (MazeGrid, error) {
	n := len(layout)
	if n == 0 {
		return MazeGrid{}, fmt.Errorf("maze layout must not be empty")
	}
	cells := make2D(n)
	for r, row := range layout {
		if len(row) != n {
			return MazeGrid{}, fmt.Errorf("maze layout row %d: want %d cols, got %d", r, n, len(row))
		}
		copy(cells[r], row)
	}
	return MazeGrid{
		Size:   n,
		Cells:  cells,
		SpawnA: [2]int{1, 1},
		SpawnB: [2]int{n - 2, n - 2},
	}, nil
}

func make2D(n int) [][]bool {
	cells := make([][]bool, n)
	for i := range cells {
		cells[i] = make([]bool, n)
	}
	return cells
}
