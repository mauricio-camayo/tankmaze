package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tankmaze/backend/internal/db"
	"github.com/tankmaze/backend/internal/engine"
	"github.com/tankmaze/backend/internal/maze"
	"github.com/tankmaze/backend/internal/wasm"
	tankmaze "github.com/tankmaze/sdk"
)

// liveMatch tracks streaming state for WebSocket observers.
type liveMatch struct {
	mu       sync.Mutex
	cond     *sync.Cond
	snapshot json.RawMessage   // MATCH_SNAPSHOT event (type+payload wrapper)
	ticks    []json.RawMessage // TICK_UPDATE events in order
	overMsg  json.RawMessage   // MATCH_OVER event; nil until done
	done     bool
}

// nextFrom returns ticks starting at index from. Blocks until there are new ticks or done.
func (lm *liveMatch) nextFrom(from int) (ticks []json.RawMessage, over json.RawMessage, done bool) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	for len(lm.ticks) <= from && !lm.done {
		lm.cond.Wait()
	}
	if from < len(lm.ticks) {
		ticks = lm.ticks[from:]
	}
	return ticks, lm.overMsg, lm.done
}

// ── WebSocket event wire format ────────────────────────────────────────────

// wsEnvelope wraps all server→client messages with a type discriminant.
// The frontend (ws.ts) reads .type and .payload.
type wsEnvelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func makeWSEvent(typ string, payload any) json.RawMessage {
	p, _ := json.Marshal(payload)
	env := wsEnvelope{Type: typ, Payload: p}
	data, _ := json.Marshal(env)
	return data
}

// ── Snapshot / tick payload shapes (match frontend types.ts) ──────────────

type snapshotPayload struct {
	MatchID     string    `json:"matchId"`
	Status      string    `json:"status"`
	Maze        [][]bool  `json:"maze"`
	TankA       tankStateWS `json:"tankA"`
	TankB       tankStateWS `json:"tankB"`
	Projectiles []projWS  `json:"projectiles"`
	Tick        int       `json:"tick"`
}

type tankStateWS struct {
	TankID   string  `json:"tankId"`
	Version  string  `json:"version"`
	Position pointWS `json:"position"`
	Facing   string  `json:"facing"`
	HP       int     `json:"hp"`
	Config   cfgWS   `json:"config"`
}

type cfgWS struct {
	Name        string `json:"name"`
	Speed       int    `json:"speed"`
	SensorRange int    `json:"sensorRange"`
	Damage      int    `json:"damage"`
	Armor       int    `json:"armor"`
	FireRate    int    `json:"fireRate"`
}

type pointWS struct {
	X int `json:"x"` // col
	Y int `json:"y"` // row
}

type projWS struct {
	Position    pointWS `json:"position"`
	Direction   string  `json:"direction"`
	OwnerTankID string  `json:"ownerTankId"`
}

type tickPayload struct {
	Tick        int         `json:"tick"`
	TankA       tankTickWS  `json:"tankA"`
	TankB       tankTickWS  `json:"tankB"`
	Projectiles []projWS    `json:"projectiles"`
}

type tankTickWS struct {
	tankStateWS
	Action     *actionWS `json:"action,omitempty"`
	Log        []string  `json:"log"`
	DurationMs int64     `json:"durationMs"`
	Violation  bool      `json:"violation"`
}

type actionWS struct {
	Type      string `json:"type"`
	Direction string `json:"direction,omitempty"`
}

type matchOverPayload struct {
	Winner *string        `json:"winner"`
	Reason string         `json:"reason"`
	Stats  matchOverStats `json:"stats"`
}

type matchOverStats struct {
	DamageA      int  `json:"damageA"`
	DamageB      int  `json:"damageB"`
	MovesA       int  `json:"movesA"`
	MovesB       int  `json:"movesB"`
	TicksElapsed int  `json:"ticksElapsed"`
	Flawless     bool `json:"flawless"`
}

// ── Conversion helpers ─────────────────────────────────────────────────────

var cardinalStr = [4]string{tankmaze.N: "N", tankmaze.S: "S", tankmaze.E: "E", tankmaze.W: "W"}

func posToPoint(pos [2]int) pointWS { return pointWS{X: pos[1], Y: pos[0]} }

func makeTankStateWS(snap engine.TankSnapshot, tankID, version, tankName string, cfg db.VersionConfig) tankStateWS {
	return tankStateWS{
		TankID:   tankID,
		Version:  version,
		Position: posToPoint(snap.Position),
		Facing:   cardinalStr[snap.Facing],
		HP:       snap.HP,
		Config: cfgWS{
			Name:        tankName,
			Speed:       cfg.Speed,
			SensorRange: cfg.SensorRange,
			Damage:      cfg.Damage,
			Armor:       cfg.Armor,
			FireRate:    cfg.FireRate,
		},
	}
}

func makeProjsWS(projs []engine.ProjSnapshot, tankIDA, tankIDB string) []projWS {
	out := make([]projWS, len(projs))
	for i, p := range projs {
		ownerID := tankIDA
		if p.Owner == 1 {
			ownerID = tankIDB
		}
		out[i] = projWS{
			Position:    posToPoint(p.Position),
			Direction:   cardinalStr[p.Facing],
			OwnerTankID: ownerID,
		}
	}
	return out
}

// invertMaze converts engine convention (true=open/passable) to frontend convention (true=wall).
// ObserverScene renders maze[r][c]=true as wall (dark) and false as path (lighter blue).
// The engine uses Cells[r][c]=true for open cells, so we must invert.
func invertMaze(cells [][]bool) [][]bool {
	out := make([][]bool, len(cells))
	for r, row := range cells {
		out[r] = make([]bool, len(row))
		for c, open := range row {
			out[r][c] = !open // true=wall in frontend
		}
	}
	return out
}

var actionTypeStr = map[tankmaze.ActionType]string{
	tankmaze.Idle:   "Idle",
	tankmaze.Move:   "Move",
	tankmaze.Rotate: "Rotate",
	tankmaze.Fire:   "Fire",
	tankmaze.Scan:   "Scan",
}

var moveDirStr = [4]string{"Forward", "Backward", "Left", "Right"}

func makeActionWS(a tankmaze.Action) *actionWS {
	typ, ok := actionTypeStr[a.Type]
	if !ok || a.Type == tankmaze.Idle {
		return nil
	}
	aw := &actionWS{Type: typ}
	if a.Type == tankmaze.Move || a.Type == tankmaze.Rotate {
		aw.Direction = moveDirStr[a.Direction]
	}
	return aw
}

func winnerPtr(w int) *string {
	switch w {
	case 0:
		s := "a"
		return &s
	case 1:
		s := "b"
		return &s
	}
	return nil
}

func versionToSDKConfig(v db.TankVersion) tankmaze.TankConfig {
	return tankmaze.TankConfig{
		Speed:       v.Config.Speed,
		SensorRange: v.Config.SensorRange,
		Damage:      v.Config.Damage,
		Armor:       v.Config.Armor,
		FireRate:    v.Config.FireRate,
	}
}

// ── Match runner ───────────────────────────────────────────────────────────

func (srv *server) runMatch(matchID string) {
	ctx := context.Background()

	match, err := srv.store.getMatch(matchID)
	if err != nil {
		return
	}
	verA, err := srv.store.getVersion(match.TankA.TankID, match.TankA.Version)
	if err != nil {
		return
	}
	verB, err := srv.store.getVersion(match.TankB.TankID, match.TankB.Version)
	if err != nil {
		return
	}

	wasmA := srv.getWasm(verA.WasmS3Key)
	wasmB := srv.getWasm(verB.WasmS3Key)
	if len(wasmA) == 0 || len(wasmB) == 0 {
		return
	}

	tmpA, err := writeTempWasm(wasmA)
	if err != nil {
		return
	}
	defer os.Remove(tmpA)
	tmpB, err := writeTempWasm(wasmB)
	if err != nil {
		return
	}
	defer os.Remove(tmpB)

	var grid maze.MazeGrid
	if match.MapID != "" {
		m, err := srv.store.getMapByID(match.MapID)
		if err != nil {
			return
		}
		grid, err = maze.Load(m.Layout)
		if err != nil {
			return
		}
	} else {
		seed, err := strconv.ParseInt(match.MazeSeed, 10, 64)
		if err != nil {
			return
		}
		grid = maze.Generate(seed, maze.SizeFromEnv())
	}

	modA, err := wasm.Load(ctx, tmpA, "")
	if err != nil {
		return
	}
	defer modA.Close(context.Background())
	modB, err := wasm.Load(ctx, tmpB, "")
	if err != nil {
		return
	}
	defer modB.Close(context.Background())

	// Use WASM-declared config when available, else fall back to version record.
	cfgA := versionToSDKConfig(verA)
	cfgB := versionToSDKConfig(verB)
	if wc := modA.TankConfig(); wc != nil {
		cfgA = *wc
	}
	if wc := modB.TankConfig(); wc != nil {
		cfgB = *wc
	}

	dbCfgA := db.VersionConfig{Speed: cfgA.Speed, SensorRange: cfgA.SensorRange, Damage: cfgA.Damage, Armor: cfgA.Armor, FireRate: cfgA.FireRate}
	dbCfgB := db.VersionConfig{Speed: cfgB.Speed, SensorRange: cfgB.SensorRange, Damage: cfgB.Damage, Armor: cfgB.Armor, FireRate: cfgB.FireRate}

	tankNameA := srv.tankName(match.TankA.TankID)
	tankNameB := srv.tankName(match.TankB.TankID)

	eng := engine.New(grid, cfgA, cfgB, engine.TickLimitFromEnv(), engine.ProjSpeedFromEnv(), engine.WallHitDamageFromEnv())

	// Build initial snapshot before starting the game loop.
	initState := eng.State()
	snap := snapshotPayload{
		MatchID:     matchID,
		Status:      "active",
		Maze:        invertMaze(grid.Cells),
		TankA:       makeTankStateWS(initState.Tanks[0], match.TankA.TankID, match.TankA.Version, tankNameA, dbCfgA),
		TankB:       makeTankStateWS(initState.Tanks[1], match.TankB.TankID, match.TankB.Version, tankNameB, dbCfgB),
		Projectiles: makeProjsWS(initState.Projectiles, match.TankA.TankID, match.TankB.TankID),
		Tick:        0,
	}

	lm := &liveMatch{}
	lm.cond = sync.NewCond(&lm.mu)
	lm.snapshot = makeWSEvent("MATCH_SNAPSHOT", snap)

	srv.mu.Lock()
	srv.liveMatches[matchID] = lm
	srv.mu.Unlock()

	srv.store.updateMatchStatus(matchID, "active")

	var result *engine.Result
	for result == nil {
		tStart := time.Now()

		sensA := eng.Sensors(0)
		sensB := eng.Sensors(1)

		tA := time.Now()
		actA, logsA, crashedA, timedOutA := modA.Tick(ctx, sensA)
		durA := time.Since(tA)

		tB := time.Now()
		actB, logsB, crashedB, timedOutB := modB.Tick(ctx, sensB)
		durB := time.Since(tB)

		result = eng.Step(actA, actB, crashedA, crashedB)
		state := eng.State()

		stateA := makeTankStateWS(state.Tanks[0], match.TankA.TankID, match.TankA.Version, tankNameA, dbCfgA)
		stateB := makeTankStateWS(state.Tanks[1], match.TankB.TankID, match.TankB.Version, tankNameB, dbCfgB)

		tickMsg := makeWSEvent("TICK_UPDATE", tickPayload{
			Tick: state.Tick,
			TankA: tankTickWS{
				tankStateWS: stateA,
				Action:      makeActionWS(actA),
				Log:         logsA,
				DurationMs:  durA.Milliseconds(),
				Violation:   timedOutA,
			},
			TankB: tankTickWS{
				tankStateWS: stateB,
				Action:      makeActionWS(actB),
				Log:         logsB,
				DurationMs:  durB.Milliseconds(),
				Violation:   timedOutB,
			},
			Projectiles: makeProjsWS(state.Projectiles, match.TankA.TankID, match.TankB.TankID),
		})

		lm.mu.Lock()
		lm.ticks = append(lm.ticks, tickMsg)
		lm.cond.Broadcast()
		lm.mu.Unlock()

		if elapsed := time.Since(tStart); elapsed < 100*time.Millisecond {
			time.Sleep(100*time.Millisecond - elapsed)
		}
	}

	modA.Close(ctx)
	modB.Close(ctx)

	var winnerInt *int
	if result.Winner >= 0 {
		w := result.Winner
		winnerInt = &w
	}
	srv.store.setMatchResult(matchID, db.MatchResult{
		Winner:       winnerInt,
		Reason:       string(result.Reason),
		DamageA:      result.DamageA,
		DamageB:      result.DamageB,
		MovesA:       result.MovesA,
		MovesB:       result.MovesB,
		TicksElapsed: result.TicksElapsed,
		Flawless:     result.Flawless,
	})

	srv.store.incrementTestMatchCount(match.TankA.TankID, match.TankA.Version)
	srv.store.incrementTestMatchCount(match.TankB.TankID, match.TankB.Version)

	overMsg := makeWSEvent("MATCH_OVER", matchOverPayload{
		Winner: winnerPtr(result.Winner),
		Reason: string(result.Reason),
		Stats: matchOverStats{
			DamageA:      result.DamageA,
			DamageB:      result.DamageB,
			MovesA:       result.MovesA,
			MovesB:       result.MovesB,
			TicksElapsed: result.TicksElapsed,
			Flawless:     result.Flawless,
		},
	})

	lm.mu.Lock()
	lm.overMsg = overMsg
	lm.done = true
	lm.cond.Broadcast()
	lm.mu.Unlock()
}

// tankName returns the display name for a tank from the store.
func (srv *server) tankName(tankID string) string {
	t, err := srv.store.getTank(tankID)
	if err != nil {
		return tankID
	}
	return t.Name
}

// ── WASM compilation ──────────────────────────────────────────────────────

// compileWasm compiles Go source to WASM.
// Source can be either:
//   - package main  → compiled directly with full WASM ABI boilerplate expected
//   - package tank  → a Tick(Sensors)Action + Config wrapper is injected as main
func (srv *server) compileWasm(source string) ([]byte, string, error) {
	tmpDir, err := os.MkdirTemp("", "tankmaze-build-*")
	if err != nil {
		return nil, "", fmt.Errorf("tempdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	sdkPath := sdkDir()
	if err := checkDir(sdkPath); err != nil {
		return nil, "", fmt.Errorf("SDK not found at %s: %w", sdkPath, err)
	}

	gomod := fmt.Sprintf(
		"module build\n\ngo 1.22\n\nrequire github.com/tankmaze/sdk v0.0.0-00010101000000-000000000000\n\nreplace github.com/tankmaze/sdk => %s\n",
		sdkPath,
	)

	isMain := strings.Contains(source, "package main")

	if isMain {
		if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(source), 0644); err != nil {
			return nil, "", err
		}
	} else {
		// package tank style: create tank/ sub-dir + inject main.go wrapper
		tankDir := filepath.Join(tmpDir, "tank")
		if err := os.MkdirAll(tankDir, 0755); err != nil {
			return nil, "", err
		}
		if err := os.WriteFile(filepath.Join(tankDir, "tank.go"), []byte(source), 0644); err != nil {
			return nil, "", err
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(wrapperSrc), 0644); err != nil {
			return nil, "", err
		}
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(gomod), 0644); err != nil {
		return nil, "", err
	}

	outWasm := filepath.Join(tmpDir, "tank.wasm")
	cmd := exec.Command("go", "build", "-mod=mod", "-o", outWasm, ".")
	cmd.Dir = tmpDir
	cmd.Env = append(os.Environ(),
		"GOOS=wasip1",
		"GOARCH=wasm",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"GOPROXY=off",
		"GONOSUMDB=*",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, "", errors.New(string(out))
	}

	data, err := os.ReadFile(outWasm)
	if err != nil {
		return nil, "", err
	}
	h := sha256.Sum256(data)
	return data, fmt.Sprintf("%x", h), nil
}

func aiConfig(name string) db.VersionConfig {
	switch name {
	case "scout":
		return db.VersionConfig{Speed: 5, SensorRange: 3, Damage: 2, Armor: 2, FireRate: 3}
	case "bruiser":
		return db.VersionConfig{Speed: 2, SensorRange: 2, Damage: 5, Armor: 5, FireRate: 1}
	default:
		return db.VersionConfig{}
	}
}

// compileAITank builds a testdata tank to WASM and registers it in the server's store.
func (srv *server) compileAITank(name string) error {
	dir := filepath.Join(tanksBaseDir(), name)
	if err := checkDir(dir); err != nil {
		return fmt.Errorf("tank dir %s not found: %w", dir, err)
	}

	tmpWasm := filepath.Join(os.TempDir(), fmt.Sprintf("tankmaze-ai-%s.wasm", name))
	cmd := exec.Command("go", "build", "-o", tmpWasm, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GOOS=wasip1",
		"GOARCH=wasm",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"GOPROXY=off",
		"GONOSUMDB=*",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("compile %s: %v\n%s", name, err, out)
	}

	data, err := os.ReadFile(tmpWasm)
	os.Remove(tmpWasm)
	if err != nil {
		return err
	}

	wasmKey := fmt.Sprintf("__ai__/%s/tank.wasm", name)
	srv.setWasm(wasmKey, data)

	// Store source so fork-copy works and getVersionSource can serve it directly.
	srcKey := fmt.Sprintf("__ai__/%s/source.go", name)
	if srcBytes, err := os.ReadFile(filepath.Join(dir, "main.go")); err == nil {
		srv.mu.Lock()
		srv.srcData[srcKey] = srcBytes
		srv.mu.Unlock()
	}

	tankID := "__" + name + "__"
	displayName := strings.ToUpper(name[:1]) + name[1:]
	aiAvatarURLs := map[string]string{
		"scout":   "/avatars/tank-14.png",
		"bruiser": "/avatars/tank-9.png",
		"ranger":  "/avatars/tank-15.png",
		"randy":   "/avatars/tank-11.png",
	}
	srv.store.putTank(db.Tank{
		TankID:    tankID,
		UserID:    aiUserID,
		Name:      displayName,
		AvatarURL: aiAvatarURLs[name],
		CreatedAt: time.Now().Unix(),
	})
	srv.store.putVersion(db.TankVersion{
		TankID:        tankID,
		Version:       "v1",
		VersionType:   "major",
		Config:        aiConfig(name),
		WasmS3Key:     wasmKey,
		SourceS3Key:   srcKey,
		CompileStatus: "ready",
		CreatedAt:     time.Now().Unix(),
	})
	return nil
}

// ── Helpers ────────────────────────────────────────────────────────────────

func writeTempWasm(data []byte) (string, error) {
	f, err := os.CreateTemp("", "tankmaze-*.wasm")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()
	return f.Name(), nil
}

func sdkDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Join(filepath.Dir(file), "../../../../packages/sdk")
}

func tanksBaseDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Join(filepath.Dir(file), "../../../../packages/testdata/tanks")
}

func checkDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory")
	}
	return nil
}

// wrapperSrc is injected as main.go when user submits "package tank" style source.
// It calls tank.Config and tank.Tick from the user's sub-package.
const wrapperSrc = `package main

import (
	"encoding/json"
	"unsafe"

	"build/tank"
	tankmaze "github.com/tankmaze/sdk"
)

//go:wasmimport tankmaze sensors_get
//go:noescape
func sensorsGet(ptr unsafe.Pointer, cap int32) int32

//go:wasmimport tankmaze config_register
//go:noescape
func configRegister(ptr unsafe.Pointer, length int32)

//go:wasmimport tankmaze action_put
func actionPut(encoded int32)

func encode(a tankmaze.Action) int32 { return int32(a.Type)*10 + int32(a.Direction) }

var cfgJSON = func() []byte { b, _ := json.Marshal(tank.Config); return b }()

func main() {
	if len(cfgJSON) > 0 {
		configRegister(unsafe.Pointer(&cfgJSON[0]), int32(len(cfgJSON)))
	}
	buf := make([]byte, 4096)
	for {
		n := sensorsGet(unsafe.Pointer(&buf[0]), int32(len(buf)))
		if n < 0 {
			return
		}
		var s tankmaze.Sensors
		if err := json.Unmarshal(buf[:n], &s); err != nil {
			actionPut(encode(tankmaze.Action{Type: tankmaze.Idle}))
			continue
		}
		actionPut(encode(tank.Tick(s)))
	}
}
`
