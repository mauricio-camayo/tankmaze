// Package main implements the TankMaze match-runner Lambda.
//
// Invoked directly by tournament-scheduler (ranked matches) and tank-api (test
// matches) with a JSON payload of { "matchId": "<uuid>" }.
//
// Execution model:
//  1. Load the match record and both tank version records from DynamoDB.
//  2. Download each tank's WASM binary from S3 to /tmp; verify SHA-256.
//  3. Build the maze from MazeSeed (generated) or MapID (static layout).
//  4. Initialise the engine and two Wazero instances.
//  5. Run the game loop: Sensors → WASM Tick → engine.Step, up to tickLimit.
//     After each tick, broadcast TICK_UPDATE to live observers via API Gateway.
//  6. Write the full tick log to S3 (gzip-compressed JSON).
//  7. Persist the match result and update version stats in DynamoDB.
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi"
	agwtypes "github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	lambdasvc "github.com/aws/aws-sdk-go-v2/service/lambda"
	ltypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/tankmaze/backend/internal/db"
	"github.com/tankmaze/backend/internal/engine"
	"github.com/tankmaze/backend/internal/maze"
	"github.com/tankmaze/backend/internal/wasm"
	tankmaze "github.com/tankmaze/sdk"
)

const (
	tickBudget         = 100 * time.Millisecond
	violationThreshold = 0.20 // disqualify if >20% of ticks violate
)

// matchEvent is the Lambda invocation payload.
type matchEvent struct {
	MatchID string `json:"matchId"`
}

// ---- Tick log types (written to S3) -----------------------------------------

type tickLogFile struct {
	MatchID  string      `json:"matchId"`
	MazeSeed string      `json:"mazeSeed,omitempty"`
	MapID    string      `json:"mapId,omitempty"`
	Maze     [][]bool    `json:"maze"`
	Tanks    logTankPair `json:"tanks"`
	Ticks    []logTick   `json:"ticks"`
	Result   *logResult  `json:"result"`
}

type logTankPair struct {
	A logTankMeta `json:"a"`
	B logTankMeta `json:"b"`
}

type logTankMeta struct {
	TankID  string           `json:"tankId"`
	Version string           `json:"version"`
	Name    string           `json:"name,omitempty"`
	Config  db.VersionConfig `json:"config"`
}

type logTick struct {
	Tick        int         `json:"tick"`
	A           logTankTick `json:"a"`
	B           logTankTick `json:"b"`
	Projectiles []logProj   `json:"projectiles,omitempty"`
}

type logProj struct {
	X     int    `json:"x"`
	Y     int    `json:"y"`
	Dir   string `json:"dir"`
	Owner int    `json:"owner"` // 0 = tank A, 1 = tank B
}

type logTankTick struct {
	Sensors    tankmaze.Sensors `json:"sensors"`
	Action     tankmaze.Action  `json:"action"`
	DurationMs int64            `json:"durationMs"`
	Violation  bool             `json:"violation"`
	Log        []string         `json:"log"`
}

type logResult struct {
	Winner          *string `json:"winner"` // "a", "b", or null
	Reason          string  `json:"reason"`
	DamageA         int     `json:"damageA"`
	DamageB         int     `json:"damageB"`
	MovesA          int     `json:"movesA"`
	MovesB          int     `json:"movesB"`
	TicksElapsed    int     `json:"ticksElapsed"`
	Flawless        bool    `json:"flawless"`
	FinalHPA        int     `json:"finalHpA"`
	FinalHPB        int     `json:"finalHpB"`
	ShotsFiredA     int     `json:"shotsFiredA"`
	ShotsFiredB     int     `json:"shotsFiredB"`
	HitsA           int     `json:"hitsA"`
	HitsB           int     `json:"hitsB"`
	TickViolationsA int     `json:"tickViolationsA"`
	TickViolationsB int     `json:"tickViolationsB"`
	DurationMs      int64   `json:"durationMs"`
}

// ---- Observer broadcast types -----------------------------------------------
//
// All outbound messages use { "type": "<EVENT>", "payload": { ... } } to match
// the frontend ws.ts dispatch convention.

type wsEnvelope struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// tickPayload matches the frontend TickUpdate interface.
type tickPayload struct {
	Tick        int         `json:"tick"`
	TankA       tankStateWS `json:"tankA"`
	TankB       tankStateWS `json:"tankB"`
	Projectiles []projWS    `json:"projectiles"`
}

// tankStateWS matches the frontend TankState interface.
type tankStateWS struct {
	TankID   string   `json:"tankId"`
	Version  string   `json:"version"`
	Position pointWS  `json:"position"`
	Facing   string   `json:"facing"`
	HP       int      `json:"hp"`
	Config   configWS `json:"config"`
	Log      []string `json:"log,omitempty"`
}

type pointWS struct {
	X int `json:"x"` // col
	Y int `json:"y"` // row
}

type configWS struct {
	Name        string `json:"name"`
	Speed       int    `json:"speed"`
	SensorRange int    `json:"sensorRange"`
	Damage      int    `json:"damage"`
	Armor       int    `json:"armor"`
	FireRate    int    `json:"fireRate"`
}

// projWS matches the frontend Projectile interface.
type projWS struct {
	Position    pointWS `json:"position"`
	Direction   string  `json:"direction"`
	OwnerTankID string  `json:"ownerTankId"`
}

type matchOverPayload struct {
	Winner *string        `json:"winner"` // "a", "b", or null
	Reason string         `json:"reason"`
	Stats  matchOverStats `json:"stats"`
}

type matchOverStats struct {
	DamageA         int   `json:"damageA"`
	DamageB         int   `json:"damageB"`
	MovesA          int   `json:"movesA"`
	MovesB          int   `json:"movesB"`
	TicksElapsed    int   `json:"ticksElapsed"`
	Flawless        bool  `json:"flawless"`
	FinalHPA        int   `json:"finalHpA"`
	FinalHPB        int   `json:"finalHpB"`
	ShotsFiredA     int   `json:"shotsFiredA"`
	ShotsFiredB     int   `json:"shotsFiredB"`
	HitsA           int   `json:"hitsA"`
	HitsB           int   `json:"hitsB"`
	TickViolationsA int   `json:"tickViolationsA"`
	TickViolationsB int   `json:"tickViolationsB"`
	DurationMs      int64 `json:"durationMs"`
}

// cardinalStr maps Direction int (N=0,S=1,E=2,W=3) to letter.
var cardinalStr = [4]string{0: "N", 1: "S", 2: "E", 3: "W"}

func dirName(d tankmaze.Direction) string {
	if int(d) < len(cardinalStr) {
		return cardinalStr[d]
	}
	return "N"
}

// ---- Handler ----------------------------------------------------------------

type handler struct {
	store                 *db.Store
	s3                    *s3.Client
	apigw                 *apigatewaymanagementapi.Client
	lambdaSvc             *lambdasvc.Client
	wasmBucket            string
	logsBucket            string
	tournamentSchedulerFn string
	tickLimit             int
	projSpeed             int
	wallHitDamage         int
	collisionDamageTable  [5]int
	moveCooldownTicks     [5]int
}

var h *handler

func main() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}

	h = &handler{
		store: db.New(dynamodb.NewFromConfig(cfg)),
		s3:    s3.NewFromConfig(cfg),
		apigw: apigatewaymanagementapi.NewFromConfig(cfg, func(o *apigatewaymanagementapi.Options) {
			o.BaseEndpoint = aws.String(os.Getenv("APIGW_ENDPOINT"))
		}),
		lambdaSvc:             lambdasvc.NewFromConfig(cfg),
		wasmBucket:            os.Getenv("WASM_BUCKET"),
		logsBucket:            os.Getenv("MATCH_LOGS_BUCKET"),
		tournamentSchedulerFn: os.Getenv("TOURNAMENT_SCHEDULER_FUNCTION"),
		tickLimit:             engine.TickLimitFromEnv(),
		projSpeed:             engine.ProjSpeedFromEnv(),
		wallHitDamage:         engine.WallHitDamageFromEnv(),
		collisionDamageTable:  engine.CollisionDamageTableFromEnv(),
		moveCooldownTicks:     engine.MoveCooldownTicksFromEnv(),
	}

	lambda.Start(h.handle)
}

func (h *handler) handle(ctx context.Context, evt matchEvent) error {
	if evt.MatchID == "" {
		return fmt.Errorf("matchId is required")
	}
	matchID := evt.MatchID

	// ---- Load match and version records -------------------------------------

	match, err := h.store.GetMatch(ctx, matchID)
	if err != nil {
		return fmt.Errorf("get match: %w", err)
	}
	if match.Status == "ended" {
		log.Printf("match %s already ended, skipping", matchID)
		return nil
	}

	// Game Day autofill (item 248) may suffix TankID (e.g. "builtin-scout#2")
	// to distinguish repeated copies of the same built-in AI in per-event
	// standings/seeding/bracket bookkeeping — it is never a real Tank/
	// TankVersion record, so every DB lookup here must strip it back off.
	realTankA := db.RealTankID(match.TankA.TankID)
	realTankB := db.RealTankID(match.TankB.TankID)

	verA, err := h.store.GetVersion(ctx, realTankA, match.TankA.Version)
	if err != nil {
		return fmt.Errorf("get version A (%s/%s): %w", realTankA, match.TankA.Version, err)
	}
	verB, err := h.store.GetVersion(ctx, realTankB, match.TankB.Version)
	if err != nil {
		return fmt.Errorf("get version B (%s/%s): %w", realTankB, match.TankB.Version, err)
	}

	// Resolve display names for the tick log.
	nameA := match.TankA.TankName
	if nameA == "" {
		if t, err := h.store.GetTank(ctx, realTankA); err == nil {
			nameA = t.Name
		}
	}
	nameB := match.TankB.TankName
	if nameB == "" {
		if t, err := h.store.GetTank(ctx, realTankB); err == nil {
			nameB = t.Name
		}
	}

	// ---- Download WASM binaries to /tmp -------------------------------------
	// Use SHA256-keyed paths so the same binary is downloaded and compiled only
	// once per Lambda container lifetime across all matches.

	wasmPathA := wasmCachePath(verA.WasmSHA256, matchID+"-a")
	wasmPathB := wasmCachePath(verB.WasmSHA256, matchID+"-b")

	missingA := h.downloadWASM(ctx, verA.WasmS3Key, wasmPathA)
	missingB := h.downloadWASM(ctx, verB.WasmS3Key, wasmPathB)
	if isWasmMissing(missingA) || isWasmMissing(missingB) {
		// One or both WASM binaries are no longer in S3 (expired lifecycle or
		// never uploaded). Record a forfeit rather than leaving the match stuck.
		log.Printf("match %s: WASM missing (A=%v B=%v) — recording forfeit", matchID, missingA, missingB)
		return h.recordForfeit(ctx, match, "", missingA, missingB)
	}
	if missingA != nil {
		return fmt.Errorf("download WASM A: %w", missingA)
	}
	if missingB != nil {
		return fmt.Errorf("download WASM B: %w", missingB)
	}

	// ---- Load maze ----------------------------------------------------------

	grid, err := h.loadMaze(ctx, match)
	if err != nil {
		return fmt.Errorf("load maze: %w", err)
	}

	// ---- Initialise engine and WASM modules ---------------------------------

	cfgA := versionToTankConfig(verA)
	cfgB := versionToTankConfig(verB)
	eng := engine.New(grid, cfgA, cfgB, h.tickLimit, h.projSpeed, h.wallHitDamage,
		engine.WithCollisionDamageTable(h.collisionDamageTable),
		engine.WithMoveCooldownTicks(h.moveCooldownTicks))

	modA, errLoadA := wasm.Load(ctx, wasmPathA, verA.WasmSHA256)
	modB, errLoadB := wasm.Load(ctx, wasmPathB, verB.WasmSHA256)
	if errLoadA != nil || errLoadB != nil {
		if errLoadA != nil {
			log.Printf("match %s: cannot load WASM A: %v — recording forfeit", matchID, errLoadA)
		}
		if errLoadB != nil {
			log.Printf("match %s: cannot load WASM B: %v — recording forfeit", matchID, errLoadB)
		}
		return h.recordLoadForfeit(ctx, match, errLoadA, errLoadB)
	}
	defer modA.Close(context.Background())
	defer modB.Close(context.Background())

	// ---- Mark match active --------------------------------------------------

	if err := h.store.UpdateMatchStatus(ctx, matchID, "active"); err != nil {
		return fmt.Errorf("set match active: %w", err)
	}

	// ---- Game loop ----------------------------------------------------------

	matchStart := time.Now()
	var ticks []logTick
	var violationsA, violationsB int
	var result *engine.Result

	for result == nil {
		tickStart := time.Now()

		sensorsA := eng.Sensors(0)
		sensorsB := eng.Sensors(1)

		tA := time.Now()
		actionA, logsA, crashedA, timedOutA := modA.Tick(ctx, sensorsA)
		durA := time.Since(tA)

		tB := time.Now()
		actionB, logsB, crashedB, timedOutB := modB.Tick(ctx, sensorsB)
		durB := time.Since(tB)

		if timedOutA {
			violationsA++
		}
		if timedOutB {
			violationsB++
		}

		result = eng.Step(actionA, actionB, crashedA, crashedB)

		state := eng.State()
		for i, line := range logsA {
			logsA[i] = fmt.Sprintf("%d: %s", state.Tick, line)
		}
		for i, line := range logsB {
			logsB[i] = fmt.Sprintf("%d: %s", state.Tick, line)
		}
		logProjs := make([]logProj, len(state.Projectiles))
		for i, p := range state.Projectiles {
			logProjs[i] = logProj{
				X:     p.Position[1], // col
				Y:     p.Position[0], // row
				Dir:   dirName(p.Facing),
				Owner: p.Owner,
			}
		}
		ticks = append(ticks, logTick{
			Tick: state.Tick,
			A: logTankTick{
				Sensors:    sensorsA,
				Action:     actionA,
				DurationMs: durA.Milliseconds(),
				Violation:  timedOutA,
				Log:        logsA,
			},
			B: logTankTick{
				Sensors:    sensorsB,
				Action:     actionB,
				DurationMs: durB.Milliseconds(),
				Violation:  timedOutB,
				Log:        logsB,
			},
			Projectiles: logProjs,
		})

		h.broadcast(ctx, match, verA, verB, state, logsA, logsB)

		if elapsed := time.Since(tickStart); elapsed < tickBudget {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(tickBudget - elapsed):
			}
		}
	}

	modA.Close(ctx)
	modB.Close(ctx)

	durationMs := time.Since(matchStart).Milliseconds()

	// ---- Write tick log to S3 -----------------------------------------------

	tickLogKey := fmt.Sprintf("matches/%s/ticks.json.gz", matchID)

	logFile := tickLogFile{
		MatchID:  matchID,
		MazeSeed: match.MazeSeed,
		MapID:    match.MapID,
		Maze:     grid.Cells,
		Tanks: logTankPair{
			A: logTankMeta{TankID: match.TankA.TankID, Version: match.TankA.Version, Name: nameA, Config: verA.Config},
			B: logTankMeta{TankID: match.TankB.TankID, Version: match.TankB.Version, Name: nameB, Config: verB.Config},
		},
		Ticks:  ticks,
		Result: engineResultToLog(result, violationsA, violationsB, durationMs),
	}

	if err := h.writeTickLog(ctx, tickLogKey, logFile); err != nil {
		log.Printf("write tick log for match %s: %v", matchID, err)
		// Non-fatal: result is still persisted even if the log upload fails.
		tickLogKey = ""
	}

	// ---- Persist result -----------------------------------------------------

	dbResult := engineResultToDB(result, violationsA, violationsB, durationMs)
	if err := h.store.SetMatchResult(ctx, matchID, tickLogKey, dbResult); err != nil {
		return fmt.Errorf("set match result: %w", err)
	}

	// Item 23: "watch last replay" link on tank detail — any match type, any
	// version, both participants. Best-effort; a failure here shouldn't fail
	// the match itself since the result is already durably persisted above.
	if err := h.store.UpdateTankLastMatch(ctx, realTankA, matchID); err != nil {
		log.Printf("update last match A: %v", err)
	}
	if err := h.store.UpdateTankLastMatch(ctx, realTankB, matchID); err != nil {
		log.Printf("update last match B: %v", err)
	}

	// ---- Broadcast MATCH_OVER to observers ----------------------------------

	h.broadcastMatchOver(ctx, matchID, result, violationsA, violationsB, durationMs)

	// ---- Update version stats -----------------------------------------------

	totalTicks := result.TicksElapsed
	if totalTicks == 0 {
		totalTicks = 1
	}

	if float64(violationsA)/float64(totalTicks) > violationThreshold {
		if err := h.store.SetVersionDisqualified(ctx, realTankA, match.TankA.Version); err != nil {
			log.Printf("set disqualified A: %v", err)
		}
	}
	if float64(violationsB)/float64(totalTicks) > violationThreshold {
		if err := h.store.SetVersionDisqualified(ctx, realTankB, match.TankB.Version); err != nil {
			log.Printf("set disqualified B: %v", err)
		}
	}

	switch match.MatchType {
	case "ranked":
		h.updateRankedStats(ctx, match, realTankA, realTankB, verA, verB, result)
		// When the last ranked match in a game day ends, trigger the next phase in
		// tournament-scheduler. This makes phase transitions event-driven so they
		// fire even if EventBridge schedules arrived before matches completed.
		if match.GameDayID != "" && h.tournamentSchedulerFn != "" {
			h.maybeAdvanceTournament(ctx, match.GameDayID)
		}
	case "test-ai", "test-own":
		if err := h.store.IncrementTestMatchCount(ctx, realTankA, match.TankA.Version); err != nil {
			log.Printf("increment test match count A: %v", err)
		}
		if err := h.store.IncrementTestMatchCount(ctx, realTankB, match.TankB.Version); err != nil {
			log.Printf("increment test match count B: %v", err)
		}
	}

	return nil
}

// ---- Helpers ----------------------------------------------------------------

// loadMaze returns a MazeGrid using the match's MapID (static) or MazeSeed (generated).
func (h *handler) loadMaze(ctx context.Context, match db.Match) (maze.MazeGrid, error) {
	if match.MapID != "" {
		m, err := h.store.GetMapByID(ctx, match.MapID)
		if err != nil {
			return maze.MazeGrid{}, fmt.Errorf("get map %s: %w", match.MapID, err)
		}
		return maze.Load(m.Layout)
	}
	seed, err := strconv.ParseInt(match.MazeSeed, 10, 64)
	if err != nil {
		return maze.MazeGrid{}, fmt.Errorf("parse maze seed %q: %w", match.MazeSeed, err)
	}
	return maze.Generate(seed, maze.SizeFromEnv()), nil
}

// wasmCachePath returns a deterministic /tmp path for a WASM binary.
// When sha256 is known, the path is keyed by content so the same binary is
// reused across matches in the same Lambda container. Falls back to a
// per-invocation path when sha256 is empty.
func wasmCachePath(sha256, fallback string) string {
	if sha256 != "" {
		return "/tmp/wasm-" + sha256 + ".wasm"
	}
	return "/tmp/" + fallback + ".wasm"
}

// downloadWASM fetches an S3 object and writes it to destPath.
// If the file already exists at destPath it is reused without an S3 call.
func (h *handler) downloadWASM(ctx context.Context, s3Key, destPath string) error {
	if s3Key == "" {
		return fmt.Errorf("WASM S3 key is empty")
	}
	if _, err := os.Stat(destPath); err == nil {
		return nil // already cached in /tmp from a previous invocation
	}
	out, err := h.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(h.wasmBucket),
		Key:    aws.String(s3Key),
	})
	if err != nil {
		return fmt.Errorf("s3 get %s: %w", s3Key, err)
	}
	defer out.Body.Close()

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", destPath, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, out.Body); err != nil {
		return fmt.Errorf("write %s: %w", destPath, err)
	}
	return nil
}

// isWasmMissing reports whether err indicates the WASM file no longer exists in S3.
func isWasmMissing(err error) bool {
	if err == nil {
		return false
	}
	var noKey *s3types.NoSuchKey
	return errors.As(err, &noKey)
}

// recordForfeit ends a match whose WASM is missing without running any ticks.
// If A is missing → B wins; if B is missing → A wins; if both missing → void.
func (h *handler) recordForfeit(ctx context.Context, match db.Match, _ string, errA, errB error) error {
	aForfeit := isWasmMissing(errA)
	bForfeit := isWasmMissing(errB)

	var winner *int
	var reason string
	switch {
	case aForfeit && bForfeit:
		reason = "both_lose"
	case aForfeit:
		w := 1 // B wins
		winner = &w
		reason = "forfeit"
	default:
		w := 0 // A wins
		winner = &w
		reason = "forfeit"
	}

	result := db.MatchResult{
		Winner:   winner,
		Reason:   reason,
		FinalHPA: 100, // no ticks ran — neither tank took damage
		FinalHPB: 100,
	}
	if err := h.store.SetMatchResult(ctx, match.MatchID, "", result); err != nil {
		return fmt.Errorf("set forfeit result: %w", err)
	}
	return nil
}

// recordLoadForfeit ends a match whose WASM module failed to load.
// If only A fails → B wins; if only B fails → A wins; if both fail → void.
func (h *handler) recordLoadForfeit(ctx context.Context, match db.Match, errA, errB error) error {
	var winner *int
	var reason string
	switch {
	case errA != nil && errB != nil:
		reason = "both_lose"
	case errA != nil:
		w := 1
		winner = &w
		reason = "forfeit"
	default:
		w := 0
		winner = &w
		reason = "forfeit"
	}
	result := db.MatchResult{Winner: winner, Reason: reason, FinalHPA: 100, FinalHPB: 100}
	if err := h.store.SetMatchResult(ctx, match.MatchID, "", result); err != nil {
		return fmt.Errorf("set load-forfeit result: %w", err)
	}
	return nil
}

// broadcast sends a TICK_UPDATE event to all live observers for the match.
func (h *handler) broadcast(ctx context.Context, match db.Match, verA, verB db.TankVersion, state engine.State, logsA, logsB []string) {
	conns, err := h.store.ListConnectionsByMatch(ctx, match.MatchID)
	if err != nil {
		log.Printf("list connections for match %s: %v", match.MatchID, err)
		return
	}
	if len(conns) == 0 {
		return
	}

	projs := make([]projWS, len(state.Projectiles))
	for i, p := range state.Projectiles {
		ownerID := match.TankA.TankID
		if p.Owner == 1 {
			ownerID = match.TankB.TankID
		}
		projs[i] = projWS{
			Position:    pointWS{X: p.Position[1], Y: p.Position[0]},
			Direction:   cardinalStr[p.Facing],
			OwnerTankID: ownerID,
		}
	}
	payload := tickPayload{
		Tick: state.Tick,
		TankA: tankStateWS{
			TankID:   match.TankA.TankID,
			Version:  match.TankA.Version,
			Position: pointWS{X: state.Tanks[0].Position[1], Y: state.Tanks[0].Position[0]},
			Facing:   cardinalStr[state.Tanks[0].Facing],
			HP:       state.Tanks[0].HP,
			Config:   versionToConfigWS(verA),
			Log:      logsA,
		},
		TankB: tankStateWS{
			TankID:   match.TankB.TankID,
			Version:  match.TankB.Version,
			Position: pointWS{X: state.Tanks[1].Position[1], Y: state.Tanks[1].Position[0]},
			Facing:   cardinalStr[state.Tanks[1].Facing],
			HP:       state.Tanks[1].HP,
			Config:   versionToConfigWS(verB),
			Log:      logsB,
		},
		Projectiles: projs,
	}
	data, err := json.Marshal(wsEnvelope{Type: "TICK_UPDATE", Payload: payload})
	if err != nil {
		log.Printf("marshal TICK_UPDATE: %v", err)
		return
	}
	for _, conn := range conns {
		h.postToConnection(ctx, conn.ConnectionID, match.MatchID, data)
	}
}

// broadcastMatchOver sends a MATCH_OVER event to all live observers.
func (h *handler) broadcastMatchOver(ctx context.Context, matchID string, result *engine.Result, violationsA, violationsB int, durationMs int64) {
	conns, err := h.store.ListConnectionsByMatch(ctx, matchID)
	if err != nil {
		log.Printf("list connections for match %s: %v", matchID, err)
		return
	}
	if len(conns) == 0 {
		return
	}
	payload := matchOverPayload{
		Winner: winnerStr(result.Winner),
		Reason: string(result.Reason),
		Stats: matchOverStats{
			DamageA:         result.DamageA,
			DamageB:         result.DamageB,
			MovesA:          result.MovesA,
			MovesB:          result.MovesB,
			TicksElapsed:    result.TicksElapsed,
			Flawless:        result.Flawless,
			FinalHPA:        result.FinalHPA,
			FinalHPB:        result.FinalHPB,
			ShotsFiredA:     result.ShotsFiredA,
			ShotsFiredB:     result.ShotsFiredB,
			HitsA:           result.HitsA,
			HitsB:           result.HitsB,
			TickViolationsA: violationsA,
			TickViolationsB: violationsB,
			DurationMs:      durationMs,
		},
	}
	data, err := json.Marshal(wsEnvelope{Type: "MATCH_OVER", Payload: payload})
	if err != nil {
		log.Printf("marshal MATCH_OVER: %v", err)
		return
	}
	for _, conn := range conns {
		h.postToConnection(ctx, conn.ConnectionID, matchID, data)
	}
}

// versionToConfigWS converts a db.TankVersion to the broadcast config shape.
func versionToConfigWS(v db.TankVersion) configWS {
	return configWS{
		Speed:       v.Config.Speed,
		SensorRange: v.Config.SensorRange,
		Damage:      v.Config.Damage,
		Armor:       v.Config.Armor,
		FireRate:    v.Config.FireRate,
	}
}

// postToConnection sends data to a WebSocket connection, cleaning up stale ones.
func (h *handler) postToConnection(ctx context.Context, connID, matchID string, data []byte) {
	_, err := h.apigw.PostToConnection(ctx, &apigatewaymanagementapi.PostToConnectionInput{
		ConnectionId: aws.String(connID),
		Data:         data,
	})
	if err != nil {
		var gone *agwtypes.GoneException
		if errors.As(err, &gone) {
			if delErr := h.store.DeleteConnection(ctx, connID); delErr != nil {
				log.Printf("delete stale connection %s: %v", connID, delErr)
			}
		} else {
			log.Printf("post to connection %s for match %s: %v", connID, matchID, err)
		}
	}
}

// writeTickLog gzip-compresses the tick log and uploads it to S3.
func (h *handler) writeTickLog(ctx context.Context, key string, logFile tickLogFile) error {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := json.NewEncoder(gz).Encode(logFile); err != nil {
		return fmt.Errorf("encode tick log: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("close gzip: %w", err)
	}
	_, err := h.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:          aws.String(h.logsBucket),
		Key:             aws.String(key),
		Body:            bytes.NewReader(buf.Bytes()),
		ContentType:     aws.String("application/gzip"),
		ContentEncoding: aws.String("gzip"),
	})
	return err
}

// updateRankedStats performs a read-modify-write to update the running
// performance averages on both major versions after a ranked match.
func (h *handler) updateRankedStats(ctx context.Context, match db.Match, realTankA, realTankB string, verA, verB db.TankVersion, result *engine.Result) {
	updateStats := func(tankID, version string, won bool, damageDealt, ticksSurvived int) {
		ver, err := h.store.GetVersion(ctx, tankID, version)
		if err != nil {
			log.Printf("get version %s/%s for stats: %v", tankID, version, err)
			return
		}
		n := ver.MatchesPlayed + 1
		wins := 0
		if won {
			wins = 1
		}
		newWinRate := (ver.WinRate*float64(n-1) + float64(wins)) / float64(n)
		newAvgDamage := (ver.AvgDamageDealt*float64(n-1) + float64(damageDealt)) / float64(n)
		newAvgSurvival := (ver.AvgSurvivalTicks*float64(n-1) + float64(ticksSurvived)) / float64(n)
		stats := db.VersionStats{
			WinRate:          newWinRate,
			MatchesPlayed:    n,
			AvgDamageDealt:   newAvgDamage,
			AvgSurvivalTicks: newAvgSurvival,
		}
		if err := h.store.UpdateVersionStats(ctx, tankID, version, stats); err != nil {
			log.Printf("update stats %s/%s: %v", tankID, version, err)
		}
	}

	wonA := result.Winner == 0
	wonB := result.Winner == 1
	updateStats(realTankA, match.TankA.Version, wonA, result.DamageA, result.TicksElapsed)
	updateStats(realTankB, match.TankB.Version, wonB, result.DamageB, result.TicksElapsed)
}

// versionToTankConfig converts a db.TankVersion into the engine's TankConfig.
func versionToTankConfig(ver db.TankVersion) tankmaze.TankConfig {
	return tankmaze.TankConfig{
		Speed:       ver.Config.Speed,
		SensorRange: ver.Config.SensorRange,
		Damage:      ver.Config.Damage,
		Armor:       ver.Config.Armor,
		FireRate:    ver.Config.FireRate,
	}
}

// engineResultToLog converts an engine.Result to the tick log result format.
// Winner is "a", "b", or null (nil pointer).
func engineResultToLog(r *engine.Result, violationsA, violationsB int, durationMs int64) *logResult {
	if r == nil {
		return nil
	}
	return &logResult{
		Winner:          winnerStr(r.Winner),
		Reason:          string(r.Reason),
		DamageA:         r.DamageA,
		DamageB:         r.DamageB,
		MovesA:          r.MovesA,
		MovesB:          r.MovesB,
		TicksElapsed:    r.TicksElapsed,
		Flawless:        r.Flawless,
		FinalHPA:        r.FinalHPA,
		FinalHPB:        r.FinalHPB,
		ShotsFiredA:     r.ShotsFiredA,
		ShotsFiredB:     r.ShotsFiredB,
		HitsA:           r.HitsA,
		HitsB:           r.HitsB,
		TickViolationsA: violationsA,
		TickViolationsB: violationsB,
		DurationMs:      durationMs,
	}
}

// engineResultToDB converts an engine.Result to the db.MatchResult format.
// Winner is 0, 1, or nil (for both_lose).
func engineResultToDB(r *engine.Result, violationsA, violationsB int, durationMs int64) db.MatchResult {
	var winner *int
	if r.Winner >= 0 {
		w := r.Winner
		winner = &w
	}
	return db.MatchResult{
		Winner:          winner,
		Reason:          string(r.Reason),
		DamageA:         r.DamageA,
		DamageB:         r.DamageB,
		MovesA:          r.MovesA,
		MovesB:          r.MovesB,
		TicksElapsed:    r.TicksElapsed,
		Flawless:        r.Flawless,
		FinalHPA:        r.FinalHPA,
		FinalHPB:        r.FinalHPB,
		ShotsFiredA:     r.ShotsFiredA,
		ShotsFiredB:     r.ShotsFiredB,
		HitsA:           r.HitsA,
		HitsB:           r.HitsB,
		TickViolationsA: violationsA,
		TickViolationsB: violationsB,
		DurationMs:      durationMs,
	}
}

// winnerStr returns a pointer to "a", "b", or nil based on the winner index.
func winnerStr(winner int) *string {
	switch winner {
	case 0:
		s := "a"
		return &s
	case 1:
		s := "b"
		return &s
	}
	return nil
}

// maybeAdvanceTournament checks whether all ranked matches for gameDayID have
// ended and, if so, invokes tournament-scheduler with the next phase. This
// ensures phase transitions happen even when EventBridge schedules fire before
// all matches complete and Lambda's 3-attempt retry window is exhausted.
func (h *handler) maybeAdvanceTournament(ctx context.Context, gameDayID string) {
	matches, err := h.store.ScanMatchesByGameDay(ctx, gameDayID)
	if err != nil {
		log.Printf("maybeAdvanceTournament %s: scan: %v", gameDayID, err)
		return
	}
	for i := range matches {
		if matches[i].Status != "ended" {
			return
		}
	}
	gd, err := h.store.GetGameDay(ctx, gameDayID)
	if err != nil {
		log.Printf("maybeAdvanceTournament %s: get game day: %v", gameDayID, err)
		return
	}
	phase := nextTournamentPhase(gd)
	if phase == "" {
		return
	}
	log.Printf("maybeAdvanceTournament %s: all matches ended, triggering %s", gameDayID, phase)
	payload, _ := json.Marshal(map[string]string{"gameDayId": gameDayID, "phase": phase})
	if _, err := h.lambdaSvc.Invoke(ctx, &lambdasvc.InvokeInput{
		FunctionName:   aws.String(h.tournamentSchedulerFn),
		InvocationType: ltypes.InvocationTypeEvent,
		Payload:        payload,
	}); err != nil {
		log.Printf("maybeAdvanceTournament %s: invoke %s: %v", gameDayID, phase, err)
	}
}

// nextTournamentPhase returns the tournament-scheduler phase to trigger after
// all current matches have ended, or "" if the tournament is already done.
func nextTournamentPhase(gd db.GameDay) string {
	if gd.Phases.Final.Status == "running" || gd.Phases.Final.Status == "complete" {
		return ""
	}
	if gd.Phases.RoundRobin.Status == "running" {
		return "elimination_r1"
	}
	// Find the highest running elimination round.
	last := 0
	for key, p := range gd.Phases.Elimination {
		if p.Status == "running" && len(key) > 1 && key[0] == 'r' {
			if n, err := strconv.Atoi(key[1:]); err == nil && n > last {
				last = n
			}
		}
	}
	if last > 0 {
		// If the current round already has ≤1 active (both-sides-real) pair,
		// there's no next elimination round to run — the survivor(s) go
		// straight to the final. Mirrors the guard in tournament-scheduler's
		// handleElimination, which otherwise just no-ops when invoked with
		// this bogus elimination_r{N+1} phase, leaving the round stuck at
		// "running" until the separately-scheduled -final EventBridge rule
		// eventually rescues it.
		lastKey := fmt.Sprintf("r%d", last)
		if activePairs(gd.Bracket[lastKey]) <= 1 {
			return "final"
		}
		return fmt.Sprintf("elimination_r%d", last+1)
	}
	return ""
}

// activePairs returns the number of pairs in slots where both sides are real
// tanks (as opposed to byes). Mirrors tournament-scheduler's activePairs.
func activePairs(slots []db.BracketSlot) int {
	n := 0
	for i := 0; i+1 < len(slots); i += 2 {
		if slots[i].TankID != "" && slots[i+1].TankID != "" {
			n++
		}
	}
	return n
}
