package maze

// Open returns the "open" static test map: only the outer boundary walls,
// completely free interior. Maximum movement freedom for both tanks.
func Open() MazeGrid {
	var g MazeGrid
	for r := 0; r < Size; r++ {
		for c := 0; c < Size; c++ {
			g.Cells[r][c] = r > 0 && r < Size-1 && c > 0 && c < Size-1
		}
	}
	return g
}

// Donut returns the "donut" static test map: a single open corridor that runs
// exactly 1 cell inside every outer wall, forming a rectangular ring. The
// outer ring and all interior cells (rows 2–22, cols 2–22) are wall.
func Donut() MazeGrid {
	var g MazeGrid
	for r := 1; r < Size-1; r++ {
		for c := 1; c < Size-1; c++ {
			g.Cells[r][c] = r == 1 || r == Size-2 || c == 1 || c == Size-2
		}
	}
	return g
}
