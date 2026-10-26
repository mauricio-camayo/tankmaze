package wasm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	tankmaze "github.com/tankmaze/sdk"
)

func TestVerifyChecksum(t *testing.T) {
	content := []byte("hello wasm")
	sum := sha256.Sum256(content)
	correctHex := hex.EncodeToString(sum[:])

	tmp := filepath.Join(t.TempDir(), "test.wasm")
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := VerifyChecksum(tmp, correctHex); err != nil {
		t.Fatalf("expected no error on correct checksum, got: %v", err)
	}

	if err := VerifyChecksum(tmp, "deadbeef"); err == nil {
		t.Fatal("expected error on wrong checksum, got nil")
	}

	if err := VerifyChecksum("/no/such/file", correctHex); err == nil {
		t.Fatal("expected error on missing file, got nil")
	}
}

func TestDecodeAction(t *testing.T) {
	cases := []struct {
		encoded uint32
		want    tankmaze.Action
	}{
		{0, tankmaze.Action{Type: tankmaze.Idle, Direction: tankmaze.Forward}},
		{10, tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}},
		{11, tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Backward}},
		{12, tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Left}},
		{13, tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Right}},
		{20, tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Forward}},
		{22, tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Left}},
		{23, tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Right}},
		{30, tankmaze.Action{Type: tankmaze.Fire, Direction: tankmaze.Forward}},
		{40, tankmaze.Action{Type: tankmaze.Scan, Direction: tankmaze.Forward}},
	}
	for _, c := range cases {
		got := decodeAction(c.encoded)
		if got != c.want {
			t.Errorf("decodeAction(%d) = %+v, want %+v", c.encoded, got, c.want)
		}
	}
}

// --- wazero-version regression coverage ---------------------------------
//
// The tests below were added alongside the wazero v1.7.3 -> v1.12.0 bump.
// None of the existing coverage (TestVerifyChecksum, TestDecodeAction, and
// match-runner's TestScoutVsBruiser) exercises WithRandSource, log
// capture, the tick-timeout path, crash detection, or the oversized-payload
// boundary, so a regression in any of them during a future wazero bump
// would previously have gone unnoticed.

// buildFixtureWasm compiles an inline, dependency-free Go source file to a
// wasip1/wasm binary. The tank ABI (sensors_get/config_register/action_put)
// is declared directly via //go:wasmimport in src — fixtures don't need the
// tankmaze/sdk module since they never decode the Sensors JSON payload.
func buildFixtureWasm(t *testing.T, src string) []byte {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not found in PATH")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "fixture.wasm")
	cmd := exec.Command("go", "build", "-o", dest, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "GOTOOLCHAIN=local", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}

	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// buildRandyWasm compiles the built-in "randy" test tank (the only built-in
// tank that calls math/rand) from packages/testdata/tanks/randy.
func buildRandyWasm(t *testing.T) []byte {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not found in PATH")
	}

	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
	pkgDir := filepath.Join(root, "packages", "testdata", "tanks", "randy")

	dest := filepath.Join(t.TempDir(), "randy.wasm")
	cmd := exec.Command("go", "build", "-o", dest, ".")
	cmd.Dir = pkgDir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "GOTOOLCHAIN=local", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build randy: %v\n%s", err, out)
	}

	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestRandomnessIsNotDeterministic guards against a regression of item 251:
// wazero's own default RandSource is a fixed deterministic stream, so
// without WithRandSource(crand.Reader) (wasm.go's LoadBytes), every tank's
// math/rand seeding via WASI's random_get would be identical across
// independent module instances given identical sensor input — making
// "random" AI behavior perfectly reproducible instead of actually random.
//
// Randy redraws a random wander direction via math/rand every 5 ticks when
// no opponent is in range. Two independently-loaded instances fed the exact
// same synthetic sensor sequence should diverge at some point if entropy is
// wired correctly; if it's broken, their action sequences would be
// byte-identical for all 60 ticks.
func TestRandomnessIsNotDeterministic(t *testing.T) {
	wasmBytes := buildRandyWasm(t)

	open := map[tankmaze.Direction]int{tankmaze.N: 5, tankmaze.S: 5, tankmaze.E: 5, tankmaze.W: 5}

	actionsFor := func() []int32 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		m, err := LoadBytes(ctx, wasmBytes)
		if err != nil {
			t.Fatalf("load randy: %v", err)
		}
		defer m.Close(context.Background())

		encoded := make([]int32, 0, 60)
		for i := 0; i < 60; i++ {
			sensors := tankmaze.Sensors{
				Facing:        tankmaze.N,
				HP:            100,
				WallDistances: open,
				Tick:          i,
			}
			action, _, crashed, _ := m.Tick(ctx, sensors)
			if crashed {
				t.Fatalf("randy crashed on tick %d", i)
			}
			encoded = append(encoded, int32(action.Type)*10+int32(action.Direction))
		}
		return encoded
	}

	seqA := actionsFor()
	seqB := actionsFor()

	identical := len(seqA) == len(seqB)
	if identical {
		for i := range seqA {
			if seqA[i] != seqB[i] {
				identical = false
				break
			}
		}
	}
	if identical {
		t.Fatal("two independent module instances produced an identical action sequence from identical sensor input — math/rand entropy appears deterministic (regression of item 251: WASI random_get must be wired to real entropy via wazero.WithRandSource)")
	}
}

// TestLogCapture guards the stdout/stderr -> curLogs path (stderrCapturer in
// wasm.go), wired via wazero's WithStdout/WithStderr module config. Neither
// built-in test tank logs anything, so this path had zero coverage before.
func TestLogCapture(t *testing.T) {
	src := fixtureWithImports(`"fmt"
	"log"`, `		fmt.Println("plain-stdout-line")
		log.Println("timestamped-line")`)

	wasmBytes := buildFixtureWasm(t, src)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m, err := LoadBytes(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	defer m.Close(context.Background())

	_, logs, crashed, timedOut := m.Tick(ctx, tankmaze.Sensors{})
	if crashed || timedOut {
		t.Fatalf("unexpected crashed=%v timedOut=%v", crashed, timedOut)
	}

	var gotPlain, gotTimestamped bool
	for _, l := range logs {
		switch l {
		case "plain-stdout-line":
			gotPlain = true
		case "timestamped-line":
			gotTimestamped = true
		}
	}
	if !gotPlain {
		t.Errorf("expected captured logs to contain the plain fmt.Println line, got %v", logs)
	}
	if !gotTimestamped {
		t.Errorf("expected captured logs to contain the log.Println line with its timestamp prefix stripped, got %v", logs)
	}
}

// TestTickTimeoutDoesNotCrash guards the tickCtx.Done() path (wasm.go's
// Tick): a tank that overruns the 50ms deadline must be reported as
// timedOut, not crashed, and the module must recover to normal operation on
// the next tick once its slow computation finally finishes.
func TestTickTimeoutDoesNotCrash(t *testing.T) {
	// Note: time.Sleep is not a valid way to simulate a slow tick here —
	// under wazero, a module driven via WithStartFunctions() + a manual
	// _start call (exactly wasm.go's own invocation pattern) does not block
	// real wall-clock time on a WASI clock-based sleep (confirmed in both
	// the old and new wazero version, so it's a pre-existing quirk of this
	// invocation style, not a version regression — but it means a tight
	// CPU-bound loop is the only reliable way to burn real wall time here.
	src := fixtureWithImports(``, `		if tick == 1 {
			sink := 0
			for i := 0; i < 10_000_000; i++ {
				sink += i
			}
			_ = sink
		}`)

	wasmBytes := buildFixtureWasm(t, src)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m, err := LoadBytes(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	defer m.Close(context.Background())

	start := time.Now()
	_, _, crashed, timedOut := m.Tick(ctx, tankmaze.Sensors{})
	elapsed := time.Since(start)
	t.Logf("elapsed=%v crashed=%v timedOut=%v", elapsed, crashed, timedOut)
	if crashed {
		t.Fatal("expected a slow tick to time out, not crash")
	}
	if !timedOut {
		t.Fatal("expected timedOut=true for a tick exceeding the 50ms deadline")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Tick took %v to return on timeout — it should return promptly near the 50ms deadline, not block for the module's full computation", elapsed)
	}

	// Let the background goroutine finish its slow tick and drain the
	// buffered response before issuing the next one.
	time.Sleep(500 * time.Millisecond)

	_, _, crashed2, timedOut2 := m.Tick(ctx, tankmaze.Sensors{})
	if crashed2 || timedOut2 {
		t.Fatalf("expected a normal tick after recovery, got crashed=%v timedOut=%v", crashed2, timedOut2)
	}
}

// TestCrashDetection guards crashed()'s classification of an unrecovered
// panic and a nonzero os.Exit against the installed wazero version's actual
// trap/exit wrapping — the sys.ExitError shape was confirmed unchanged, but
// that's an API check, not a behavioral one, and no existing test
// deliberately crashes a tank.
func TestCrashDetection(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "panic",
			src: `package main

import "unsafe"

//go:wasmimport tankmaze config_register
//go:noescape
func configRegister(ptr unsafe.Pointer, length int32)

func main() {
	cfg := []byte("{}")
	configRegister(unsafe.Pointer(&cfg[0]), int32(len(cfg)))
	panic("boom")
}
`,
		},
		{
			name: "os.Exit(1)",
			src: `package main

import (
	"os"
	"unsafe"
)

//go:wasmimport tankmaze config_register
//go:noescape
func configRegister(ptr unsafe.Pointer, length int32)

func main() {
	cfg := []byte("{}")
	configRegister(unsafe.Pointer(&cfg[0]), int32(len(cfg)))
	os.Exit(1)
}
`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wasmBytes := buildFixtureWasm(t, c.src)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			m, err := LoadBytes(ctx, wasmBytes)
			if err != nil {
				t.Fatalf("load fixture: %v", err)
			}
			defer m.Close(context.Background())

			_, _, crashed, _ := m.Tick(ctx, tankmaze.Sensors{})
			if !crashed {
				t.Errorf("expected crashed=true for a tank that calls %s", c.name)
			}
		})
	}
}

// TestOversizedSensorsPayload guards hostSensorsGet's len(data) > cap branch
// (wasm.go): a tank whose buffer is too small for the marshaled Sensors
// payload must be told to exit (-1) and the module must reach a clean
// doneCh, not crash or hang.
func TestOversizedSensorsPayload(t *testing.T) {
	src := `package main

import "unsafe"

//go:wasmimport tankmaze sensors_get
//go:noescape
func sensorsGet(ptr unsafe.Pointer, cap int32) int32

//go:wasmimport tankmaze config_register
//go:noescape
func configRegister(ptr unsafe.Pointer, length int32)

func main() {
	cfg := []byte("{}")
	configRegister(unsafe.Pointer(&cfg[0]), int32(len(cfg)))

	// Deliberately too small to ever hold a marshaled Sensors payload.
	buf := make([]byte, 4)
	sensorsGet(unsafe.Pointer(&buf[0]), int32(len(buf)))
}
`
	wasmBytes := buildFixtureWasm(t, src)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m, err := LoadBytes(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	defer m.Close(context.Background())

	action, _, crashed, timedOut := m.Tick(ctx, tankmaze.Sensors{Facing: tankmaze.N})
	if crashed {
		t.Fatal("an oversized-payload tick should exit cleanly (sensors_get returns -1), not crash")
	}
	if timedOut {
		t.Fatal("expected a prompt Idle response, not a timeout")
	}
	if action != (tankmaze.Action{}) {
		t.Errorf("expected the zero-value Idle action from a drained request, got %+v", action)
	}

	select {
	case <-m.doneCh:
	case <-time.After(2 * time.Second):
		t.Error("expected the module to reach doneCh after an oversized sensors payload; it did not exit")
	}
}

// fixtureWithImports fills fixtureABI's import list and per-tick body.
func fixtureWithImports(imports, body string) string {
	return "package main\n\nimport (\n\t" + imports + "\n\t\"unsafe\"\n)\n\n" +
		`//go:wasmimport tankmaze sensors_get
//go:noescape
func sensorsGet(ptr unsafe.Pointer, cap int32) int32

//go:wasmimport tankmaze config_register
//go:noescape
func configRegister(ptr unsafe.Pointer, length int32)

//go:wasmimport tankmaze action_put
func actionPut(encoded int32)

func main() {
	cfg := []byte("{}")
	configRegister(unsafe.Pointer(&cfg[0]), int32(len(cfg)))

	buf := make([]byte, 4096)
	tick := 0
	for {
		n := sensorsGet(unsafe.Pointer(&buf[0]), int32(len(buf)))
		if n < 0 {
			return
		}
		tick++
` + body + `
		actionPut(0)
	}
}
`
}
