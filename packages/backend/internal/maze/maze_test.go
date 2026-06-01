package maze

import (
	"testing"
)

// bfsReachable returns the set of open cells reachable from (startRow, startCol).
func bfsReachable(g MazeGrid, startRow, startCol int) [Size][Size]bool {
	var visited [Size][Size]bool
	queue := [][2]int{{startRow, startCol}}
	visited[startRow][startCol] = true
	dirs := [4][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range dirs {
			nr, nc := cur[0]+d[0], cur[1]+d[1]
			if nr < 0 || nr >= Size || nc < 0 || nc >= Size {
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
		g := Generate(seed)
		for i := 0; i < Size; i++ {
			if g.Cells[0][i] {
				t.Errorf("seed %d: top wall cell (0,%d) is open", seed, i)
			}
			if g.Cells[Size-1][i] {
				t.Errorf("seed %d: bottom wall cell (%d,%d) is open", seed, Size-1, i)
			}
			if g.Cells[i][0] {
				t.Errorf("seed %d: left wall cell (%d,0) is open", seed, i)
			}
			if g.Cells[i][Size-1] {
				t.Errorf("seed %d: right wall cell (%d,%d) is open", seed, i, Size-1)
			}
		}
	}
}

func TestGenerate_SpawnPointsAlwaysOpen(t *testing.T) {
	for _, seed := range []int64{0, 1, 42, 99, 1000} {
		g := Generate(seed)
		if !g.Cells[SpawnA[0]][SpawnA[1]] {
			t.Errorf("seed %d: SpawnA %v is not open", seed, SpawnA)
		}
		if !g.Cells[SpawnB[0]][SpawnB[1]] {
			t.Errorf("seed %d: SpawnB %v is not open", seed, SpawnB)
		}
	}
}

func TestGenerate_FullyConnected(t *testing.T) {
	for _, seed := range []int64{0, 1, 42, 99, 1000} {
		g := Generate(seed)
		reachable := bfsReachable(g, SpawnA[0], SpawnA[1])

		// Every open cell must be reachable from SpawnA.
		for r := 0; r < Size; r++ {
			for c := 0; c < Size; c++ {
				if g.Cells[r][c] && !reachable[r][c] {
					t.Errorf("seed %d: open cell (%d,%d) is not reachable from SpawnA", seed, r, c)
				}
			}
		}

		// SpawnB must be reachable.
		if !reachable[SpawnB[0]][SpawnB[1]] {
			t.Errorf("seed %d: SpawnB %v not reachable from SpawnA", seed, SpawnB)
		}
	}
}

func TestGenerate_Deterministic(t *testing.T) {
	g1 := Generate(42)
	g2 := Generate(42)
	if g1.Cells != g2.Cells {
		t.Error("same seed produced different mazes")
	}
}

func TestGenerate_DifferentSeedsDiffer(t *testing.T) {
	g1 := Generate(1)
	g2 := Generate(2)
	if g1.Cells == g2.Cells {
		t.Error("different seeds produced identical mazes")
	}
}

func TestLoad_Valid(t *testing.T) {
	layout := make([][]bool, Size)
	for r := range layout {
		layout[r] = make([]bool, Size)
		for c := range layout[r] {
			// Open interior, closed outer ring — like the "open" test map.
			layout[r][c] = r > 0 && r < Size-1 && c > 0 && c < Size-1
		}
	}
	g, err := Load(layout)
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if g.Cells[0][0] {
		t.Error("corner (0,0) should be wall")
	}
	if !g.Cells[1][1] {
		t.Error("interior cell (1,1) should be open")
	}
}

func TestLoad_WrongRowCount(t *testing.T) {
	_, err := Load([][]bool{})
	if err == nil {
		t.Error("expected error for empty layout")
	}

	short := make([][]bool, Size-1)
	for r := range short {
		short[r] = make([]bool, Size)
	}
	_, err = Load(short)
	if err == nil {
		t.Error("expected error for layout with too few rows")
	}
}

func TestLoad_WrongColCount(t *testing.T) {
	layout := make([][]bool, Size)
	for r := range layout {
		layout[r] = make([]bool, Size-1)
	}
	_, err := Load(layout)
	if err == nil {
		t.Error("expected error for layout with wrong column count")
	}
}
