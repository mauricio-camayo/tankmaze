// Package wasm provides the Wazero-based WASM loader and host function
// registration for TankMaze match execution.
//
// Host module "tankmaze" exports three functions to each tank WASM instance:
//
//	sensors_get(ptr i32, cap i32) i32
//	    Blocks until a new game tick begins. Writes JSON-encoded Sensors into
//	    the module's linear memory at ptr (up to cap bytes). Returns bytes
//	    written, or -1 when the module should shut down (match over or close).
//
//	log_write(ptr i32, len i32)
//	    Appends a UTF-8 message to the current tick's log slice.
//
//	action_put(encoded i32)
//	    Submits the tank's chosen Action for this tick.
//	    Encoding: encoded = ActionType*10 + MoveDirection.
//
// # Execution model
//
// A single module instance runs per match. The tank's main() should loop:
// call sensors_get (blocking), decide, call action_put, repeat. Global state
// (Go package-level variables) persists across ticks within the same match.
//
// A background goroutine calls _start (which calls main()) for the lifetime
// of the match. Tick calls synchronise with it via an unbuffered channel so
// that only one goroutine executes WASM at a time.
//
// A 50 ms per-tick deadline yields Idle without marking the module as crashed.
// WASM traps, panics, and non-zero exits are permanent crashes.
//
// # Tank SDK contract
//
// The tank's main() must follow this structure:
//
//	func main() {
//	    buf := make([]byte, 4096)
//	    for {
//	        n := sensorsGet(&buf[0], int32(len(buf)))
//	        if n < 0 { return } // match over
//	        var s tankmaze.Sensors
//	        json.Unmarshal(buf[:n], &s)
//	        action := Tick(s)
//	        actionPut(encodeAction(action))
//	    }
//	}
package wasm

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"

	tankmaze "github.com/tankmaze/sdk"
)

// compilationCache is a process-lifetime Wazero cache backed by /tmp.
// Lambda containers reuse the same process for warm invocations, so the
// compiled native code from the first invocation is reused on subsequent
// ones — eliminating per-invocation JIT overhead.
var compilationCache wazero.CompilationCache

func init() {
	var err error
	compilationCache, err = wazero.NewCompilationCacheWithDir("/tmp/wazero-cache")
	if err != nil {
		log.Printf("wasm: compilation cache unavailable (%v) — running uncached", err)
	}
}

const (
	tickTimeout    = 50 * time.Millisecond
	// Standard Go wasip1 binaries require ~275 pages minimum for the runtime.
	// TinyGo fits in ~32; keep a headroom ceiling of 512 pages (32 MiB).
	maxMemoryPages   = 512 // 512 × 64 KiB = 32 MiB
	hostModule       = "tankmaze"
	fnSensorsGet     = "sensors_get"
	fnLogWrite       = "log_write"
	fnActionPut      = "action_put"
	fnConfigRegister = "config_register"

	// configTimeout is how long LoadBytes waits for the tank to call
	// config_register before giving up and proceeding without a config.
	configTimeout = 5 * time.Second
)

// tickRequest is sent from the host's Tick call to the background goroutine.
type tickRequest struct {
	sensors  tankmaze.Sensors
	response chan<- tickResponse // buffered 1; sender never blocks
}

// tickResponse carries the result of one tick back to the host.
type tickResponse struct {
	action tankmaze.Action
	logs   []string
}

// Module is a live WASM tank instance for one match. Create with Load or
// LoadBytes; call Tick once per game tick; call Close when the match ends.
type Module struct {
	rt     wazero.Runtime
	cancel context.CancelFunc // stops the background goroutine

	// Communication between host (Tick) and background goroutine (WASM).
	tickCh  chan tickRequest // unbuffered — only one goroutine runs WASM at a time
	crashCh chan struct{}    // closed when module crashes (trap / non-zero exit)
	doneCh  chan struct{}    // closed on clean exit or Close()

	// Per-tick state; only the background goroutine reads/writes these.
	curReq  *tickRequest
	curLogs []string

	// tankCfg is populated when the WASM calls the config_register host function
	// at the start of main(), before entering the game loop.
	tankCfg   *tankmaze.TankConfig
	tankCfgCh chan struct{} // closed once tankCfg is set (or module crashes/exits)
}

// TankConfig returns the tank's self-declared stat allocation, or nil if the
// module did not call config_register before the wait timeout elapsed.
func (m *Module) TankConfig() *tankmaze.TankConfig { return m.tankCfg }

// Load reads the WASM binary at wasmPath, optionally verifies its SHA-256
// against expectedSHA256 (hex), and starts the module. The module begins
// running its main() loop in a background goroutine immediately.
func Load(ctx context.Context, wasmPath, expectedSHA256 string) (*Module, error) {
	if expectedSHA256 != "" {
		if err := VerifyChecksum(wasmPath, expectedSHA256); err != nil {
			return nil, err
		}
	}
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("read wasm: %w", err)
	}
	return LoadBytes(ctx, wasmBytes)
}

// LoadBytes compiles a WASM binary and starts the module. SHA-256 verification
// must be done by the caller before calling LoadBytes if required.
func LoadBytes(ctx context.Context, wasmBytes []byte) (*Module, error) {
	// bgCtx lives for the lifetime of the background goroutine; cancelling it
	// interrupts any in-progress WASM execution and causes sensors_get to
	// return -1, signalling the tank to exit cleanly.
	bgCtx, cancel := context.WithCancel(context.Background())

	m := &Module{
		cancel:    cancel,
		tickCh:    make(chan tickRequest), // unbuffered
		crashCh:   make(chan struct{}),
		doneCh:    make(chan struct{}),
		tankCfgCh: make(chan struct{}),
	}

	rtCfg := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(maxMemoryPages).
		WithCloseOnContextDone(true) // interrupt WASM when bgCtx is cancelled
	if compilationCache != nil {
		rtCfg = rtCfg.WithCompilationCache(compilationCache)
	}
	m.rt = wazero.NewRuntimeWithConfig(ctx, rtCfg)

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, m.rt); err != nil {
		cancel()
		m.rt.Close(ctx)
		return nil, fmt.Errorf("wasi init: %w", err)
	}

	if err := m.registerHostFunctions(ctx); err != nil {
		cancel()
		m.rt.Close(ctx)
		return nil, fmt.Errorf("register host functions: %w", err)
	}

	compiled, err := m.rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		cancel()
		m.rt.Close(ctx)
		return nil, fmt.Errorf("compile wasm: %w", err)
	}

	// WithStartFunctions() skips the automatic _start call so we can run it
	// ourselves in the background goroutine under bgCtx.
	modCfg := wazero.NewModuleConfig().
		WithName("tank").
		WithStartFunctions().
		// wazero's default RandSource is a fixed deterministic stream (see its
		// own doc comment on WithRandSource) — without this, every tank's
		// math/rand seeding via WASI's random_get is identical across every
		// match, making any AI logic that calls math/rand (e.g. Randy's wander
		// direction, Rammer's hookSide coin flip) perfectly reproducible given
		// the same map/spawn/opponent instead of actually random (item 251).
		WithRandSource(crand.Reader).
		WithStdout(&stderrCapturer{m}).  // capture fmt.Println / os.Stdout
		WithStderr(&stderrCapturer{m})   // capture log.Println / os.Stderr

	mod, err := m.rt.InstantiateModule(ctx, compiled, modCfg)
	if err != nil {
		cancel()
		m.rt.Close(ctx)
		return nil, fmt.Errorf("instantiate module: %w", err)
	}

	go m.run(bgCtx, mod)

	// The tank calls config_register at the start of main(), before the game
	// loop blocks on sensors_get. Wait here so TankConfig() is populated by
	// the time Load returns.
	select {
	case <-m.tankCfgCh:
	case <-m.crashCh:
	case <-m.doneCh:
	case <-time.After(configTimeout):
	}
	return m, nil
}


// Tick delivers sensor data to the tank and waits for its action decision.
//
// The 50 ms deadline applies to both delivery and computation. If the
// module is still computing the previous tick when Tick is called, or takes
// too long to respond, Tick returns Idle with timedOut=true without setting
// crashed. A genuine Idle action returns timedOut=false.
func (m *Module) Tick(ctx context.Context, sensors tankmaze.Sensors) (action tankmaze.Action, logs []string, crashed, timedOut bool) {
	// Fast path: module already done.
	select {
	case <-m.crashCh:
		return tankmaze.Action{}, nil, true, false
	case <-m.doneCh:
		return tankmaze.Action{}, nil, false, false
	default:
	}

	// responseCh is buffered so action_put never blocks even if we time out.
	respCh := make(chan tickResponse, 1)
	req := tickRequest{sensors: sensors, response: respCh}

	tickCtx, cancel := context.WithTimeout(ctx, tickTimeout)
	defer cancel()

	// Deliver sensors to the background goroutine (blocks until it is idle).
	select {
	case m.tickCh <- req:
	case <-m.crashCh:
		return tankmaze.Action{}, nil, true, false
	case <-m.doneCh:
		return tankmaze.Action{}, nil, false, false
	case <-tickCtx.Done():
		// Background goroutine still computing the previous tick.
		return tankmaze.Action{}, nil, false, true
	}

	// Wait for the action.
	select {
	case resp := <-respCh:
		return resp.action, resp.logs, false, false
	case <-m.crashCh:
		return tankmaze.Action{}, nil, true, false
	case <-m.doneCh:
		return tankmaze.Action{}, nil, false, false
	case <-tickCtx.Done():
		// Computing too long — return Idle; background goroutine continues.
		// Its eventual action_put writes into the buffered respCh and exits
		// gracefully; the channel is GC'd when this Tick call returns.
		return tankmaze.Action{}, nil, false, true
	}
}

// Close stops the module and releases all Wazero resources. It waits for the
// background goroutine to exit before freeing the runtime.
func (m *Module) Close(ctx context.Context) {
	m.cancel()
	select {
	case <-m.crashCh:
	case <-m.doneCh:
	case <-ctx.Done():
	}
	m.rt.Close(context.Background())
}

// run is the background goroutine. It calls _start (which calls main()) and
// keeps it alive for the duration of the match.
func (m *Module) run(bgCtx context.Context, mod api.Module) {
	defer mod.Close(context.Background())

	startFn := mod.ExportedFunction("_start")
	if startFn == nil {
		m.drainPendingReq()
		close(m.crashCh)
		return
	}

	_, err := startFn.Call(bgCtx)

	m.drainPendingReq()

	if crashed(err) {
		close(m.crashCh)
	} else {
		close(m.doneCh)
	}
}

// drainPendingReq unblocks any Tick call that delivered sensors and is waiting
// for a response that will now never come (because the module crashed or exited).
func (m *Module) drainPendingReq() {
	if m.curReq != nil {
		m.curReq.response <- tickResponse{logs: append([]string(nil), m.curLogs...)}
		m.curReq = nil
	}
}

// crashed reports whether err represents an unrecoverable module failure.
func crashed(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var exitErr *sys.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode() != 0
	}
	return true // WASM trap, OOM, etc.
}

// registerHostFunctions wires up the tankmaze host module into the runtime.
// The closures capture m so they can access per-tick state without locks —
// only the background goroutine ever calls these functions.
func (m *Module) registerHostFunctions(ctx context.Context) error {
	_, err := m.rt.NewHostModuleBuilder(hostModule).
		NewFunctionBuilder().
		WithFunc(m.hostSensorsGet).
		Export(fnSensorsGet).
		NewFunctionBuilder().
		WithFunc(m.hostLogWrite).
		Export(fnLogWrite).
		NewFunctionBuilder().
		WithFunc(m.hostActionPut).
		Export(fnActionPut).
		NewFunctionBuilder().
		WithFunc(m.hostConfigRegister).
		Export(fnConfigRegister).
		Instantiate(ctx)
	return err
}

// hostSensorsGet blocks until the host delivers sensors for the next tick, then
// writes them as JSON into WASM linear memory. Returns bytes written, or -1
// when the module should exit (context cancelled or write failure).
func (m *Module) hostSensorsGet(ctx context.Context, mod api.Module, ptr, cap uint32) int32 {
	m.curLogs = m.curLogs[:0]
	m.curReq = nil

	select {
	case req := <-m.tickCh:
		data, err := json.Marshal(req.sensors)
		if err != nil || uint32(len(data)) > cap {
			// Unblock the waiting Tick with an empty (Idle) response.
			req.response <- tickResponse{}
			return -1
		}
		if !mod.Memory().Write(ptr, data) {
			req.response <- tickResponse{}
			return -1
		}
		m.curReq = &req
		return int32(len(data))
	case <-ctx.Done():
		return -1
	}
}

// hostConfigRegister is called by the tank at the very start of main(), before
// entering the game loop. It reads the JSON-encoded TankConfig from WASM memory
// and stores it, then signals the waiting LoadBytes goroutine.
func (m *Module) hostConfigRegister(_ context.Context, mod api.Module, ptr, length uint32) {
	if data, ok := mod.Memory().Read(ptr, length); ok {
		var cfg tankmaze.TankConfig
		if json.Unmarshal(data, &cfg) == nil {
			m.tankCfg = &cfg
		}
	}
	select {
	case <-m.tankCfgCh:
	default:
		close(m.tankCfgCh)
	}
}

// hostLogWrite appends a UTF-8 log message read from WASM memory to curLogs.
func (m *Module) hostLogWrite(_ context.Context, mod api.Module, ptr, length uint32) {
	data, ok := mod.Memory().Read(ptr, length)
	if !ok {
		return
	}
	m.curLogs = append(m.curLogs, string(data))
}

// hostActionPut stores the encoded Action and unblocks the waiting Tick call.
// Encoding: ActionType*10 + MoveDirection.
func (m *Module) hostActionPut(_ context.Context, encoded uint32) {
	if m.curReq == nil {
		return
	}
	m.curReq.response <- tickResponse{
		action: decodeAction(encoded),
		logs:   append([]string(nil), m.curLogs...),
	}
	m.curReq = nil
}

// goLogPrefix matches the default Go log timestamp: "2006/01/02 15:04:05 "
var goLogPrefix = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} `)

// stderrCapturer routes WASM stdout/stderr (e.g. log.Println, fmt.Println) into
// the per-tick curLogs slice so they appear in TICK_UPDATE log lines.
// Write is always called from the background WASM goroutine, so no lock is needed.
type stderrCapturer struct{ m *Module }

func (c *stderrCapturer) Write(p []byte) (n int, err error) {
	s := string(p)
	// Trim a single trailing newline that log.Println appends
	if len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	// Strip the default Go log timestamp prefix (tanks that call log.SetFlags(0) are unaffected)
	s = goLogPrefix.ReplaceAllString(s, "")
	if s != "" {
		c.m.curLogs = append(c.m.curLogs, s)
	}
	return len(p), nil
}

// VerifyChecksum returns an error if the file at path does not match the given
// SHA-256 hex digest.
func VerifyChecksum(path, expectedHex string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open wasm for checksum: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash wasm: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != expectedHex {
		return fmt.Errorf("wasm integrity check failed: got %s, want %s", got, expectedHex)
	}
	return nil
}

// decodeAction unpacks the uint32 returned by action_put into an Action.
func decodeAction(v uint32) tankmaze.Action {
	return tankmaze.Action{
		Type:      tankmaze.ActionType(v / 10),
		Direction: tankmaze.MoveDirection(v % 10),
	}
}
