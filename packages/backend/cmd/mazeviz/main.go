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
	seedFlag := flag.String("seed", "", "random seed (int64); defaults to current time")
	flag.Parse()

	seed, err := parseSeed(*seedFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	g := maze.Generate(seed, maze.SizeFromEnv())
	fmt.Printf("seed: %d\n\n", seed)
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
