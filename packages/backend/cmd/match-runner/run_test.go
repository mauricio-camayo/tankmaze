package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/tankmaze/backend/internal/engine"
	"github.com/tankmaze/backend/internal/testutil"
	"github.com/tankmaze/backend/internal/wasm"
	tankmaze "github.com/tankmaze/sdk"
)

// TestScoutVsBruiser compiles Scout and Bruiser to WASM and runs a complete
// match on the Open map, exercising the engine + wasm integration layer.
// It validates that the match terminates with a well-formed result within
// the tick limit and that no panics or data races occur.
func TestScoutVsBruiser(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not found in PATH")
	}

	tmpDir := t.TempDir()
	scoutWasm := filepath.Join(tmpDir, "scout.wasm")
	bruiserWasm := filepath.Join(tmpDir, "bruiser.wasm")

	buildWasm(t, tanksDir("scout"), scoutWasm)
	buildWasm(t, tanksDir("bruiser"), bruiserWasm)

	grid := testutil.OpenMap()

	scoutCfg := tankmaze.TankConfig{Speed: 5, SensorRange: 3, Damage: 2, Armor: 2, FireRate: 3}
	bruiserCfg := tankmaze.TankConfig{Speed: 2, SensorRange: 2, Damage: 5, Armor: 5, FireRate: 1}

	const tickLimit = 300
	eng := engine.New(grid, scoutCfg, bruiserCfg, tickLimit)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	modA, err := wasm.Load(ctx, scoutWasm, "")
	if err != nil {
		t.Fatalf("load scout wasm: %v", err)
	}
	defer modA.Close(context.Background())

	modB, err := wasm.Load(ctx, bruiserWasm, "")
	if err != nil {
		t.Fatalf("load bruiser wasm: %v", err)
	}
	defer modB.Close(context.Background())

	var result *engine.Result
	for result == nil {
		sensorsA := eng.Sensors(0)
		sensorsB := eng.Sensors(1)

		actionA, _, crashedA, _ := modA.Tick(ctx, sensorsA)
		actionB, _, crashedB, _ := modB.Tick(ctx, sensorsB)

		result = eng.Step(actionA, actionB, crashedA, crashedB)
	}

	if result.TicksElapsed <= 0 {
		t.Errorf("expected positive TicksElapsed, got %d", result.TicksElapsed)
	}
	if result.Winner < -1 || result.Winner > 1 {
		t.Errorf("unexpected Winner value %d", result.Winner)
	}
	if result.Reason == "" {
		t.Error("Reason must not be empty")
	}
	t.Logf("result: winner=%d reason=%s ticks=%d damageA=%d damageB=%d",
		result.Winner, result.Reason, result.TicksElapsed, result.DamageA, result.DamageB)
}

// buildWasm compiles the Go package at pkgDir to a WASM binary at dest.
func buildWasm(t *testing.T, pkgDir, dest string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", dest, ".")
	cmd.Dir = pkgDir
	cmd.Env = append(os.Environ(),
		"GOOS=wasip1",
		"GOARCH=wasm",
		"GOTOOLCHAIN=local",
		"GOWORK=off", // tank module is not in the workspace; use its own go.mod
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkgDir, err, out)
	}
}

// tanksDir returns the absolute path to the named built-in tank source package.
func tanksDir(name string) string {
	_, file, _, _ := runtime.Caller(0)
	// file = .../packages/backend/cmd/match-runner/run_test.go
	// tanks are at .../packages/testdata/tanks/<name>
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
	return filepath.Join(root, "packages", "testdata", "tanks", name)
}
