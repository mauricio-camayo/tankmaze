package maze

import "strings"

// Render returns a text representation of the maze suitable for terminal output.
// Each cell is two characters wide so the grid looks square in a monospace font.
//
//	"██" — wall
//	"  " — open passage
//	"A " — SpawnA position
//	"B " — SpawnB position
func Render(g MazeGrid) string {
	var sb strings.Builder
	for r := 0; r < g.Size; r++ {
		for c := 0; c < g.Size; c++ {
			switch {
			case r == g.SpawnA[0] && c == g.SpawnA[1]:
				sb.WriteString("A ")
			case r == g.SpawnB[0] && c == g.SpawnB[1]:
				sb.WriteString("B ")
			case g.Cells[r][c]:
				sb.WriteString("  ")
			default:
				sb.WriteString("██")
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}
