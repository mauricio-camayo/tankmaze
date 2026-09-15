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
//   - The interior uses a room+passage model, but rooms and passages are
//     blocks at least 2 cells wide/tall rather than single cells: the
//     interior range [1, size-2] on each axis is partitioned (see
//     partitionInterior) into rooms of width ≥ 2 separated by single-cell
//     walls, and a passage carved between two adjacent rooms is opened
//     across the full width of the shared room face (also ≥ 2 cells) —
//     never a 1-cell-wide corridor.
//   - Every room is reachable from every other room (perfect maze, no loops).
//   - SpawnA (1,1) and SpawnB (size-2,size-2) are always open: they sit in
//     the first and last room, which the partition always places at exactly
//     those corners regardless of size.
func Generate(seed int64, size int) MazeGrid {
	g := NewGrid(size)
	rng := rand.New(rand.NewSource(seed))

	segs := partitionInterior(size)
	n := len(segs)
	visited := make([][]bool, n)
	for i := range visited {
		visited[i] = make([]bool, n)
	}

	openRoom := func(bi, bj int) {
		rs, cs := segs[bi], segs[bj]
		for r := rs.start; r < rs.start+rs.width; r++ {
			for c := cs.start; c < cs.start+cs.width; c++ {
				g.Cells[r][c] = true
			}
		}
	}

	// openPassage opens the full-width wall gap between two adjacent rooms
	// (bi,bj) and (nbi,nbj); exactly one of the two indices differs by 1.
	openPassage := func(bi, bj, nbi, nbj int) {
		if bi == nbi {
			col := segs[min(bj, nbj)].start + segs[min(bj, nbj)].width
			rs := segs[bi]
			for r := rs.start; r < rs.start+rs.width; r++ {
				g.Cells[r][col] = true
			}
			return
		}
		row := segs[min(bi, nbi)].start + segs[min(bi, nbi)].width
		cs := segs[bj]
		for c := cs.start; c < cs.start+cs.width; c++ {
			g.Cells[row][c] = true
		}
	}

	dirs := [4][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}}

	var dfs func(bi, bj int)
	dfs = func(bi, bj int) {
		openRoom(bi, bj)
		visited[bi][bj] = true
		rng.Shuffle(len(dirs), func(i, j int) { dirs[i], dirs[j] = dirs[j], dirs[i] })
		for _, d := range dirs {
			nbi, nbj := bi+d[0], bj+d[1]
			if nbi < 0 || nbi >= n || nbj < 0 || nbj >= n {
				continue
			}
			if visited[nbi][nbj] {
				continue
			}
			openPassage(bi, bj, nbi, nbj)
			dfs(nbi, nbj)
		}
	}

	dfs(0, 0)
	return g
}

// segment is one room's span along a single axis: absolute grid coordinates
// [start, start+width-1], with width always ≥ 2.
type segment struct {
	start, width int
}

// partitionInterior splits the interior range [1, size-2] into segments of
// width ≥ 2 separated by single-cell walls, covering the range exactly (the
// last segment always ends at size-2, so it lines up with SpawnB). Segments
// are nominally width 2 (a 2-cell room, matching the 2-cell passage carved
// between rooms); any leftover cells that don't divide evenly into 3-cell
// slots (2 room + 1 wall) are distributed round-robin as extra width so no
// interior cell is ever left permanently walled off.
func partitionInterior(size int) []segment {
	length := size - 2
	n := (length + 1) / 3
	if n < 1 {
		n = 1
	}
	widths := make([]int, n)
	for i := range widths {
		widths[i] = 2
	}
	remainder := length - (3*n - 1)
	for i := 0; i < remainder; i++ {
		widths[i%n]++
	}
	segs := make([]segment, n)
	pos := 1
	for i, w := range widths {
		segs[i] = segment{start: pos, width: w}
		pos += w + 1
	}
	return segs
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
