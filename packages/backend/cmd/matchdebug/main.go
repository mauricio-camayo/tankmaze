// matchdebug is a development CLI that compiles Scout and Bruiser to WASM,
// runs a full match, and repaints the arena to the terminal each tick.
//
// Usage:
//
//	go run ./cmd/matchdebug              # random maze, 150 ms/tick
//	go run ./cmd/matchdebug -seed -1     # Open map (empty interior)
//	go run ./cmd/matchdebug -seed 42     # deterministic maze
//	go run ./cmd/matchdebug -delay 200ms # half real-time speed
//	go run ./cmd/matchdebug -delay 0     # as fast as possible
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tankmaze/backend/internal/engine"
	"github.com/tankmaze/backend/internal/maze"
	"github.com/tankmaze/backend/internal/testutil"
	"github.com/tankmaze/backend/internal/wasm"
	tankmaze "github.com/tankmaze/sdk"
)

const (
	reset      = "\033[0m"
	bold       = "\033[1m"
	green      = "\033[32m"
	red        = "\033[31m"
	yellow     = "\033[33m"
	gray       = "\033[90m"
	dimGreen   = "\033[2;32m"
	dimRed     = "\033[2;31m"
	faintGreen = "\033[90;32m" // gray-green for prediction
	faintRed   = "\033[90;31m"
)

var dirArrow = [4]string{
	tankmaze.N: "↑",
	tankmaze.S: "↓",
	tankmaze.E: "→",
	tankmaze.W: "←",
}

// dirDelta maps Direction → (Δrow, Δcol).
var dirDelta = [4][2]int{
	tankmaze.N: {-1, 0},
	tankmaze.S: {1, 0},
	tankmaze.E: {0, 1},
	tankmaze.W: {0, -1},
}

// Bullet head character per facing direction (2 chars wide).
var bulletHead = [4]string{
	tankmaze.N: "↑ ",
	tankmaze.S: "↓ ",
	tankmaze.E: "→ ",
	tankmaze.W: "← ",
}

// Line character for N/S or E/W travel (2 chars wide).
var bulletLine = [4]string{
	tankmaze.N: "│ ",
	tankmaze.S: "│ ",
	tankmaze.E: "──",
	tankmaze.W: "──",
}

func actionLabel(a tankmaze.Action) string {
	switch a.Type {
	case tankmaze.Move:
		switch a.Direction {
		case tankmaze.Forward:
			return "Move fwd"
		case tankmaze.Backward:
			return "Move bwd"
		case tankmaze.Left:
			return "Move left"
		default:
			return "Move right"
		}
	case tankmaze.Rotate:
		if a.Direction == tankmaze.Left {
			return "Rotate left "
		}
		return "Rotate right"
	case tankmaze.Fire:
		return "Fire!       "
	case tankmaze.Scan:
		return "Scan        "
	default:
		return "Idle        "
	}
}

// overlayKind classifies what is drawn in a cell. Higher values win ties.
type overlayKind int

const (
	kOpen       overlayKind = iota
	kWall                   // 1
	kBulletPred             // 2 – predicted path ahead (dim)
	kBulletTrail            // 3 – trail behind (dim)
	kBulletHead             // 4 – bullet current position (bright)
	kTankA                  // 5
	kTankB                  // 6 – tanks always on top
)

type overlayCell struct {
	k     overlayKind
	dir   tankmaze.Direction
	owner int // 0 = A, 1 = B
}

// renderFrame builds one terminal frame. The status bar is at the bottom so
// it stays visible even when the maze is taller than the terminal window.
//
// projSpeed controls how many cells of trail are drawn behind each bullet.
// The prediction line extends from the bullet forward until it would hit a wall.
func renderFrame(grid maze.MazeGrid, state engine.State, actions [2]tankmaze.Action, projSpeed int) string {
	tA := state.Tanks[0]
	tB := state.Tanks[1]
	width := grid.Size * 2

	var sb strings.Builder

	// ---- build overlay ----
	cells := make([][]overlayCell, grid.Size)
	for r := range cells {
		cells[r] = make([]overlayCell, grid.Size)
		for c := range cells[r] {
			if !grid.Cells[r][c] {
				cells[r][c] = overlayCell{k: kWall}
			}
		}
	}

	setCell := func(r, c int, oc overlayCell) {
		if r < 0 || r >= grid.Size || c < 0 || c >= grid.Size {
			return
		}
		if oc.k > cells[r][c].k {
			cells[r][c] = oc
		}
	}

	for _, p := range state.Projectiles {
		dir := tankmaze.Direction(p.Facing)
		d := dirDelta[dir]
		tPos := state.Tanks[p.Owner].Position
		bPos := p.Position

		// Trail: walk from the firing tank's current position toward the bullet
		// head, marking each cell as trail.  The tank cell itself is included
		// (tank rendering has higher priority and will overwrite it).  We only
		// draw when tank and bullet are on the same axis in the right orientation;
		// if the tank has moved sideways since firing we skip the trail.
		aligned := false
		switch dir {
		case tankmaze.N:
			aligned = tPos[1] == bPos[1] && tPos[0] > bPos[0]
		case tankmaze.S:
			aligned = tPos[1] == bPos[1] && tPos[0] < bPos[0]
		case tankmaze.E:
			aligned = tPos[0] == bPos[0] && tPos[1] < bPos[1]
		case tankmaze.W:
			aligned = tPos[0] == bPos[0] && tPos[1] > bPos[1]
		}
		if aligned {
			pos := tPos
			for pos != bPos {
				setCell(pos[0], pos[1], overlayCell{k: kBulletTrail, dir: dir, owner: p.Owner})
				pos = [2]int{pos[0] + d[0], pos[1] + d[1]}
			}
		}

		// Head: bullet's current position.
		setCell(bPos[0], bPos[1],
			overlayCell{k: kBulletHead, dir: dir, owner: p.Owner})

		// Prediction: cells ahead until a wall (or grid edge).
		for i := 1; ; i++ {
			r := p.Position[0] + d[0]*i
			c := p.Position[1] + d[1]*i
			if r < 0 || r >= grid.Size || c < 0 || c >= grid.Size {
				break
			}
			if !grid.Cells[r][c] {
				break // wall
			}
			setCell(r, c, overlayCell{k: kBulletPred, dir: dir, owner: p.Owner})
		}
	}

	// Tanks always on top.
	setCell(tA.Position[0], tA.Position[1], overlayCell{k: kTankA})
	setCell(tB.Position[0], tB.Position[1], overlayCell{k: kTankB})

	// ---- grid ----
	for row := 0; row < grid.Size; row++ {
		for col := 0; col < grid.Size; col++ {
			oc := cells[row][col]
			switch oc.k {
			case kWall:
				sb.WriteString(gray + "██" + reset)
			case kTankA:
				sb.WriteString(green + bold + "A" + dirArrow[tA.Facing] + reset)
			case kTankB:
				sb.WriteString(red + bold + "B" + dirArrow[tB.Facing] + reset)
			case kBulletHead:
				if oc.owner == 0 {
					sb.WriteString(green + bold + bulletHead[oc.dir] + reset)
				} else {
					sb.WriteString(red + bold + bulletHead[oc.dir] + reset)
				}
			case kBulletTrail:
				if oc.owner == 0 {
					sb.WriteString(dimGreen + bulletLine[oc.dir] + reset)
				} else {
					sb.WriteString(dimRed + bulletLine[oc.dir] + reset)
				}
			case kBulletPred:
				if oc.owner == 0 {
					sb.WriteString(faintGreen + bulletLine[oc.dir] + reset)
				} else {
					sb.WriteString(faintRed + bulletLine[oc.dir] + reset)
				}
			default:
				sb.WriteString("  ")
			}
		}
		sb.WriteByte('\n')
	}

	// ---- status bar (always at the bottom, always visible) ----
	div := strings.Repeat("─", width)
	sb.WriteString(div + "\n")
	sb.WriteString(fmt.Sprintf("  Tick %-4d  │  %sA Scout%s   HP:%3d %s  %-13s│  %sB Bruiser%s HP:%3d %s  %s\n",
		state.Tick,
		green+bold, reset, tA.HP, dirArrow[tA.Facing], actionLabel(actions[0]),
		red+bold, reset, tB.HP, dirArrow[tB.Facing], actionLabel(actions[1]),
	))
	sb.WriteString(div + "\n")

	return sb.String()
}

func main() {
	mapFlag := flag.String("map", "open", "built-in map: open, donut (overrides -seed)")
	seedFlag := flag.Int64("seed", 0, "random maze seed (0 = random); ignored when -map is set")
	delayFlag := flag.Duration("delay", 150*time.Millisecond, "pause between ticks")
	limitFlag := flag.Int("limit", 300, "max ticks")
	projSpeedFlag  := flag.Int("projspeed", engine.ProjSpeedFromEnv(), "projectile cells per tick (PROJ_SPEED env var)")
	wallHitFlag    := flag.Int("wallhit", engine.WallHitDamageFromEnv(), "HP lost when moving into a wall (WALL_HIT_DAMAGE env var)")
	flag.Parse()

	// ---- compile WASM tanks ----
	tmpDir, err := os.MkdirTemp("", "matchdebug-*")
	if err != nil {
		fatalf("tempdir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	fmt.Print("Compiling Scout... ")
	scoutWasm := filepath.Join(tmpDir, "scout.wasm")
	if err := buildWasm(tanksDir("scout"), scoutWasm); err != nil {
		fatalf("build scout: %v", err)
	}
	fmt.Println("ok")

	fmt.Print("Compiling Bruiser... ")
	bruiserWasm := filepath.Join(tmpDir, "bruiser.wasm")
	if err := buildWasm(tanksDir("bruiser"), bruiserWasm); err != nil {
		fatalf("build bruiser: %v", err)
	}
	fmt.Println("ok")

	// ---- build maze ----
	var grid maze.MazeGrid
	var mapLabel string
	switch *mapFlag {
	case "open":
		grid = testutil.OpenMap()
		mapLabel = "Open"
	case "donut":
		grid = testutil.DonutMap()
		mapLabel = "Donut"
	default:
		seed := *seedFlag
		if seed == 0 {
			seed = rand.New(rand.NewSource(time.Now().UnixNano())).Int63()
		}
		grid = maze.Generate(seed, maze.SizeFromEnv())
		mapLabel = fmt.Sprintf("generated  seed: %d", seed)
	}
	fmt.Printf("Map: %s\n", mapLabel)
	fmt.Println("Starting in 1 s…")
	time.Sleep(time.Second)

	// ---- engine + WASM ----
	ctx := context.Background()
	modA, err := wasm.Load(ctx, scoutWasm, "")
	if err != nil {
		fatalf("load scout: %v", err)
	}
	defer modA.Close(context.Background())

	modB, err := wasm.Load(ctx, bruiserWasm, "")
	if err != nil {
		fatalf("load bruiser: %v", err)
	}
	defer modB.Close(context.Background())

	// Read each tank's declared config from its WASM exports (config_size / config_ptr).
	// Fall back to hard-coded defaults only when the module does not export them.
	scoutCfg := tankCfgOrDefault(modA, tankmaze.TankConfig{Speed: 5, SensorRange: 3, Damage: 2, Armor: 2, FireRate: 3})
	bruiserCfg := tankCfgOrDefault(modB, tankmaze.TankConfig{Speed: 2, SensorRange: 2, Damage: 5, Armor: 5, FireRate: 1})
	eng := engine.New(grid, scoutCfg, bruiserCfg, *limitFlag, *projSpeedFlag, *wallHitFlag)

	// ---- match loop ----
	// Repaint in-place using cursor-up rather than clear-screen.
	// The first frame scrolls naturally; each subsequent frame moves
	// the cursor back to the top of the frame and overwrites it.
	var result *engine.Result
	var lastActions [2]tankmaze.Action
	prevLines := 0

	for result == nil {
		sensorsA := eng.Sensors(0)
		sensorsB := eng.Sensors(1)

		actionA, _, crashedA, _ := modA.Tick(ctx, sensorsA)
		actionB, _, crashedB, _ := modB.Tick(ctx, sensorsB)
		lastActions = [2]tankmaze.Action{actionA, actionB}

		result = eng.Step(actionA, actionB, crashedA, crashedB)

		frame := renderFrame(grid, eng.State(), lastActions, *projSpeedFlag)
		if prevLines > 0 {
			fmt.Printf("\033[%dA", prevLines) // cursor up to top of previous frame
		}
		fmt.Print(frame)
		prevLines = strings.Count(frame, "\n")

		if *delayFlag > 0 {
			time.Sleep(*delayFlag)
		}
	}

	// Final frame.
	frame := renderFrame(grid, eng.State(), lastActions, *projSpeedFlag)
	if prevLines > 0 {
		fmt.Printf("\033[%dA", prevLines)
	}
	fmt.Print(frame)

	// Result banner.
	fmt.Println()
	switch result.Winner {
	case 0:
		fmt.Printf(green+bold+"  Scout (A) wins!"+reset+"  reason: %s\n", result.Reason)
	case 1:
		fmt.Printf(red+bold+"  Bruiser (B) wins!"+reset+"  reason: %s\n", result.Reason)
	default:
		fmt.Printf(yellow+bold+"  Both lose"+reset+"  reason: %s\n", result.Reason)
	}
	fmt.Printf("  Ticks: %d   Damage A→B: %d   B→A: %d   Moves A: %d   B: %d\n",
		result.TicksElapsed, result.DamageA, result.DamageB, result.MovesA, result.MovesB)
}

func buildWasm(srcDir, dest string) error {
	cmd := exec.Command("go", "build", "-o", dest, ".")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(),
		"GOOS=wasip1",
		"GOARCH=wasm",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v\n%s", err, out)
	}
	return nil
}

func tanksDir(name string) string {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "../../../..")
	return filepath.Join(root, "packages", "testdata", "tanks", name)
}

// tankCfgOrDefault returns the TankConfig exported by the module, or fallback
// when the module does not export config_size / config_ptr.
func tankCfgOrDefault(m *wasm.Module, fallback tankmaze.TankConfig) tankmaze.TankConfig {
	if cfg := m.TankConfig(); cfg != nil {
		return *cfg
	}
	return fallback
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "matchdebug: "+format+"\n", args...)
	os.Exit(1)
}
