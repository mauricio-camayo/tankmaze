package maze

import (
	"reflect"
	"testing"
)

// bfsReachable returns a visited grid for cells reachable from (startRow, startCol).
func bfsReachable(g MazeGrid, startRow, startCol int) [][]bool {
	visited := make2D(g.Size)
	queue := [][2]int{{startRow, startCol}}
	visited[startRow][startCol] = true
	dirs := [4][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range dirs {
			nr, nc := cur[0]+d[0], cur[1]+d[1]
			if nr < 0 || nr >= g.Size || nc < 0 || nc >= g.Size {
				continue
			}
			if visited[nr][nc] || !g.Cells[nr][nc] {
				continue
			}
			visited[nr][nc] = true
			queue = append(queue, [2]int{nr, nc})
		}
	}
	return visited
}

func TestGenerate_OuterWallsAlwaysClosed(t *testing.T) {
	for _, seed := range []int64{0, 1, 42, 99, 1000} {
		g := Generate(seed, DefaultSize)
		n := g.Size
		for i := 0; i < n; i++ {
			if g.Cells[0][i] {
				t.Errorf("seed %d: top wall cell (0,%d) is open", seed, i)
			}
			if g.Cells[n-1][i] {
				t.Errorf("seed %d: bottom wall cell (%d,%d) is open", seed, n-1, i)
			}
			if g.Cells[i][0] {
				t.Errorf("seed %d: left wall cell (%d,0) is open", seed, i)
			}
			if g.Cells[i][n-1] {
				t.Errorf("seed %d: right wall cell (%d,%d) is open", seed, i, n-1)
			}
		}
	}
}

func TestGenerate_SpawnPointsAlwaysOpen(t *testing.T) {
	for _, seed := range []int64{0, 1, 42, 99, 1000} {
		g := Generate(seed, DefaultSize)
		if !g.Cells[g.SpawnA[0]][g.SpawnA[1]] {
			t.Errorf("seed %d: SpawnA %v is not open", seed, g.SpawnA)
		}
		if !g.Cells[g.SpawnB[0]][g.SpawnB[1]] {
			t.Errorf("seed %d: SpawnB %v is not open", seed, g.SpawnB)
		}
	}
}

func TestGenerate_FullyConnected(t *testing.T) {
	for _, seed := range []int64{0, 1, 42, 99, 1000} {
		g := Generate(seed, DefaultSize)
		reachable := bfsReachable(g, g.SpawnA[0], g.SpawnA[1])

		for r := 0; r < g.Size; r++ {
			for c := 0; c < g.Size; c++ {
				if g.Cells[r][c] && !reachable[r][c] {
					t.Errorf("seed %d: open cell (%d,%d) is not reachable from SpawnA", seed, r, c)
				}
			}
		}

		if !reachable[g.SpawnB[0]][g.SpawnB[1]] {
			t.Errorf("seed %d: SpawnB %v not reachable from SpawnA", seed, g.SpawnB)
		}
	}
}

func TestGenerate_Deterministic(t *testing.T) {
	g1 := Generate(42, DefaultSize)
	g2 := Generate(42, DefaultSize)
	if !reflect.DeepEqual(g1.Cells, g2.Cells) {
		t.Error("same seed produced different mazes")
	}
}

func TestGenerate_DifferentSeedsDiffer(t *testing.T) {
	g1 := Generate(1, DefaultSize)
	g2 := Generate(2, DefaultSize)
	if reflect.DeepEqual(g1.Cells, g2.Cells) {
		t.Error("different seeds produced identical mazes")
	}
}

func TestGenerate_NonDefaultSize(t *testing.T) {
	g := Generate(42, 11)
	if g.Size != 11 {
		t.Errorf("size: got %d, want 11", g.Size)
	}
	if g.SpawnA != ([2]int{1, 1}) {
		t.Errorf("SpawnA: %v", g.SpawnA)
	}
	if g.SpawnB != ([2]int{9, 9}) {
		t.Errorf("SpawnB: %v", g.SpawnB)
	}
	// Verify connectivity on a smaller grid too.
	reachable := bfsReachable(g, g.SpawnA[0], g.SpawnA[1])
	if !reachable[g.SpawnB[0]][g.SpawnB[1]] {
		t.Error("SpawnB not reachable from SpawnA on size-11 grid")
	}
}

func TestLoad_Valid(t *testing.T) {
	n := DefaultSize
	layout := make([][]bool, n)
	for r := range layout {
		layout[r] = make([]bool, n)
		for c := range layout[r] {
			layout[r][c] = r > 0 && r < n-1 && c > 0 && c < n-1
		}
	}
	g, err := Load(layout)
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if g.Size != n {
		t.Errorf("size: got %d, want %d", g.Size, n)
	}
	if g.Cells[0][0] {
		t.Error("corner (0,0) should be wall")
	}
	if !g.Cells[1][1] {
		t.Error("interior cell (1,1) should be open")
	}
}

func TestLoad_Empty(t *testing.T) {
	_, err := Load([][]bool{})
	if err == nil {
		t.Error("expected error for empty layout")
	}
}

func TestLoad_NotSquare(t *testing.T) {
	layout := [][]bool{
		{true, false, true},
		{false, true},     // too short
		{true, false, true},
	}
	_, err := Load(layout)
	if err == nil {
		t.Error("expected error for non-square layout")
	}
}

func TestSizeFromEnv_Default(t *testing.T) {
	t.Setenv("MAZE_SIZE", "")
	if got := SizeFromEnv(); got != DefaultSize {
		t.Errorf("got %d, want %d", got, DefaultSize)
	}
}

func TestSizeFromEnv_Valid(t *testing.T) {
	t.Setenv("MAZE_SIZE", "11")
	if got := SizeFromEnv(); got != 11 {
		t.Errorf("got %d, want 11", got)
	}
}

func TestSizeFromEnv_EvenFallsBack(t *testing.T) {
	t.Setenv("MAZE_SIZE", "26")
	if got := SizeFromEnv(); got != DefaultSize {
		t.Errorf("even size should fall back to default; got %d", got)
	}
}

func TestSizeFromEnv_TooSmallFallsBack(t *testing.T) {
	t.Setenv("MAZE_SIZE", "3")
	if got := SizeFromEnv(); got != DefaultSize {
		t.Errorf("size < 5 should fall back to default; got %d", got)
	}
}

func TestSizeFromEnv_InvalidFallsBack(t *testing.T) {
	t.Setenv("MAZE_SIZE", "notanumber")
	if got := SizeFromEnv(); got != DefaultSize {
		t.Errorf("invalid value should fall back to default; got %d", got)
	}
}
