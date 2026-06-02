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
// Tank configs are read directly from each WASM module's exported config_size
// / config_ptr functions — the WASM is the single source of truth for stats.
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

	// Read configs from the WASM modules; fail if not exported (both tanks must
	// declare config_size / config_ptr for the test to be authoritative).
	scoutCfg := requireTankConfig(t, modA, "scout")
	bruiserCfg := requireTankConfig(t, modB, "bruiser")

	t.Logf("scout cfg:   %+v", scoutCfg)
	t.Logf("bruiser cfg: %+v", bruiserCfg)

	const tickLimit = 300
	eng := engine.New(grid, scoutCfg, bruiserCfg, tickLimit, engine.ProjSpeedFromEnv(), engine.WallHitDamageFromEnv())

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

func requireTankConfig(t *testing.T, m *wasm.Module, name string) tankmaze.TankConfig {
	t.Helper()
	cfg := m.TankConfig()
	if cfg == nil {
		t.Fatalf("%s WASM does not export config_size / config_ptr", name)
	}
	return *cfg
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
		"GOWORK=off",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkgDir, err, out)
	}
}

// tanksDir returns the absolute path to the named built-in tank source package.
func tanksDir(name string) string {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
	return filepath.Join(root, "packages", "testdata", "tanks", name)
}
