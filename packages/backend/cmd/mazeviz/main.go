package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/tankmaze/backend/internal/maze"
)

func main() {
	mapFlag := flag.String("map", "", "static map: open, donut")
	seedFlag := flag.String("seed", "", "random seed (int64); defaults to current time")
	flag.Parse()

	var g maze.MazeGrid
	var label string

	switch *mapFlag {
	case "open":
		g = maze.Open()
		label = "Map: open"
	case "donut":
		g = maze.Donut()
		label = "Map: donut"
	case "":
		seed, err := parseSeed(*seedFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		g = maze.Generate(seed)
		label = fmt.Sprintf("Map: generated  seed: %d", seed)
	default:
		fmt.Fprintf(os.Stderr, "unknown map %q — valid options: open, donut\n", *mapFlag)
		os.Exit(1)
	}

	fmt.Println(label)
	fmt.Println()
	fmt.Print(maze.Render(g))
}

func parseSeed(s string) (int64, error) {
	if s == "" {
		return time.Now().UnixNano(), nil
	}
	seed, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid seed %q: %w", s, err)
	}
	return seed, nil
}
