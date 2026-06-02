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
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/tankmaze/backend/internal/db"
	"github.com/tankmaze/backend/internal/engine"
	"github.com/tankmaze/backend/internal/maze"
	"github.com/tankmaze/backend/internal/wasm"
	tankmaze "github.com/tankmaze/sdk"
)

const (
	defaultTickLimit   = 300
	tickBudget         = 100 * time.Millisecond
	violationThreshold = 0.20 // disqualify if >20% of ticks violate
)

// matchEvent is the Lambda invocation payload.
type matchEvent struct {
	MatchID string `json:"matchId"`
}

// ---- Tick log types (written to S3) -----------------------------------------

type tickLogFile struct {
	MatchID  string       `json:"matchId"`
	MazeSeed string       `json:"mazeSeed,omitempty"`
	MapID    string       `json:"mapId,omitempty"`
	Maze     [][]bool     `json:"maze"`
	Tanks    logTankPair  `json:"tanks"`
	Ticks    []logTick    `json:"ticks"`
	Result   *logResult   `json:"result"`
}

type logTankPair struct {
	A logTankMeta `json:"a"`
	B logTankMeta `json:"b"`
}

type logTankMeta struct {
	TankID  string           `json:"tankId"`
	Version string           `json:"version"`
	Config  db.VersionConfig `json:"config"`
}

type logTick struct {
	Tick int          `json:"tick"`
	A    logTankTick  `json:"a"`
	B    logTankTick  `json:"b"`
}

type logTankTick struct {
	Sensors    tankmaze.Sensors `json:"sensors"`
	Action     tankmaze.Action  `json:"action"`
	DurationMs int64            `json:"durationMs"`
	Violation  bool             `json:"violation"`
	Log        []string         `json:"log"`
}

type logResult struct {
	Winner       *string `json:"winner"` // "a", "b", or null
	Reason       string  `json:"reason"`
	DamageA      int     `json:"damageA"`
	DamageB      int     `json:"damageB"`
	MovesA       int     `json:"movesA"`
	MovesB       int     `json:"movesB"`
	TicksElapsed int     `json:"ticksElapsed"`
	Flawless     bool    `json:"flawless"`
}

// ---- Observer broadcast types -----------------------------------------------

type tickUpdateMsg struct {
	Event       string     `json:"event"`
	Tick        int        `json:"tick"`
	TankA       tankSnap   `json:"tankA"`
	TankB       tankSnap   `json:"tankB"`
	Projectiles []projSnap `json:"projectiles"`
}

type tankSnap struct {
	Position [2]int `json:"position"` // [row, col]
	Facing   int    `json:"facing"`
	HP       int    `json:"hp"`
}

type projSnap struct {
	Position [2]int `json:"position"`
	Facing   int    `json:"facing"`
	Owner    int    `json:"owner"` // 0=A, 1=B
}

type matchOverMsg struct {
	Event  string        `json:"event"`
	Winner *string       `json:"winner"` // "a", "b", or null
	Reason string        `json:"reason"`
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

// ---- Handler ----------------------------------------------------------------

type handler struct {
	store         *db.Store
	s3            *s3.Client
	apigw         *apigatewaymanagementapi.Client
	wasmBucket    string
	logsBucket    string
	tickLimit     int
	projSpeed     int
	wallHitDamage int
}

var h *handler

func main() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}

	tickLimit := defaultTickLimit
	if v := os.Getenv("TICK_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tickLimit = n
		}
	}

	h = &handler{
		store: db.New(dynamodb.NewFromConfig(cfg)),
		s3:    s3.NewFromConfig(cfg),
		apigw: apigatewaymanagementapi.NewFromConfig(cfg, func(o *apigatewaymanagementapi.Options) {
			o.BaseEndpoint = aws.String(os.Getenv("APIGW_ENDPOINT"))
		}),
		wasmBucket:    os.Getenv("WASM_BUCKET"),
		logsBucket:    os.Getenv("MATCH_LOGS_BUCKET"),
		tickLimit:     tickLimit,
		projSpeed:     engine.ProjSpeedFromEnv(),
		wallHitDamage: engine.WallHitDamageFromEnv(),
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

	verA, err := h.store.GetVersion(ctx, match.TankA.TankID, match.TankA.Version)
	if err != nil {
		return fmt.Errorf("get version A (%s/%s): %w", match.TankA.TankID, match.TankA.Version, err)
	}
	verB, err := h.store.GetVersion(ctx, match.TankB.TankID, match.TankB.Version)
	if err != nil {
		return fmt.Errorf("get version B (%s/%s): %w", match.TankB.TankID, match.TankB.Version, err)
	}

	// ---- Download WASM binaries to /tmp -------------------------------------

	wasmPathA := fmt.Sprintf("/tmp/%s-a.wasm", matchID)
	wasmPathB := fmt.Sprintf("/tmp/%s-b.wasm", matchID)
	defer func() {
		os.Remove(wasmPathA)
		os.Remove(wasmPathB)
	}()

	if err := h.downloadWASM(ctx, verA.WasmS3Key, wasmPathA); err != nil {
		return fmt.Errorf("download WASM A: %w", err)
	}
	if err := h.downloadWASM(ctx, verB.WasmS3Key, wasmPathB); err != nil {
		return fmt.Errorf("download WASM B: %w", err)
	}

	// ---- Load maze ----------------------------------------------------------

	grid, err := h.loadMaze(ctx, match)
	if err != nil {
		return fmt.Errorf("load maze: %w", err)
	}

	// ---- Initialise engine and WASM modules ---------------------------------

	cfgA := versionToTankConfig(verA)
	cfgB := versionToTankConfig(verB)
	eng := engine.New(grid, cfgA, cfgB, h.tickLimit, h.projSpeed, h.wallHitDamage)

	modA, err := wasm.Load(ctx, wasmPathA, verA.WasmSHA256)
	if err != nil {
		return fmt.Errorf("load WASM A: %w", err)
	}
	defer modA.Close(context.Background())

	modB, err := wasm.Load(ctx, wasmPathB, verB.WasmSHA256)
	if err != nil {
		return fmt.Errorf("load WASM B: %w", err)
	}
	defer modB.Close(context.Background())

	// ---- Mark match active --------------------------------------------------

	if err := h.store.UpdateMatchStatus(ctx, matchID, "active"); err != nil {
		return fmt.Errorf("set match active: %w", err)
	}

	// ---- Game loop ----------------------------------------------------------

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
		})

		h.broadcast(ctx, matchID, state)

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

	// ---- Write tick log to S3 -----------------------------------------------

	tickLogKey := fmt.Sprintf("matches/%s/ticks.json.gz", matchID)

	logFile := tickLogFile{
		MatchID:  matchID,
		MazeSeed: match.MazeSeed,
		MapID:    match.MapID,
		Maze:     grid.Cells,
		Tanks: logTankPair{
			A: logTankMeta{TankID: match.TankA.TankID, Version: match.TankA.Version, Config: verA.Config},
			B: logTankMeta{TankID: match.TankB.TankID, Version: match.TankB.Version, Config: verB.Config},
		},
		Ticks:  ticks,
		Result: engineResultToLog(result),
	}

	if err := h.writeTickLog(ctx, tickLogKey, logFile); err != nil {
		log.Printf("write tick log for match %s: %v", matchID, err)
		// Non-fatal: result is still persisted even if the log upload fails.
		tickLogKey = ""
	}

	// ---- Persist result -----------------------------------------------------

	dbResult := engineResultToDB(result)
	if err := h.store.SetMatchResult(ctx, matchID, tickLogKey, dbResult); err != nil {
		return fmt.Errorf("set match result: %w", err)
	}

	// ---- Broadcast MATCH_OVER to observers ----------------------------------

	h.broadcastMatchOver(ctx, matchID, result)

	// ---- Update version stats -----------------------------------------------

	totalTicks := result.TicksElapsed
	if totalTicks == 0 {
		totalTicks = 1
	}

	if float64(violationsA)/float64(totalTicks) > violationThreshold {
		if err := h.store.SetVersionDisqualified(ctx, match.TankA.TankID, match.TankA.Version); err != nil {
			log.Printf("set disqualified A: %v", err)
		}
	}
	if float64(violationsB)/float64(totalTicks) > violationThreshold {
		if err := h.store.SetVersionDisqualified(ctx, match.TankB.TankID, match.TankB.Version); err != nil {
			log.Printf("set disqualified B: %v", err)
		}
	}

	switch match.MatchType {
	case "ranked":
		h.updateRankedStats(ctx, match, verA, verB, result)
	case "test-ai", "test-own":
		if err := h.store.IncrementTestMatchCount(ctx, match.TankA.TankID, match.TankA.Version); err != nil {
			log.Printf("increment test match count A: %v", err)
		}
		if err := h.store.IncrementTestMatchCount(ctx, match.TankB.TankID, match.TankB.Version); err != nil {
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

// downloadWASM fetches an S3 object and writes it to destPath.
func (h *handler) downloadWASM(ctx context.Context, s3Key, destPath string) error {
	if s3Key == "" {
		return fmt.Errorf("WASM S3 key is empty")
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

// broadcast sends a TICK_UPDATE event to all live observers for the match.
func (h *handler) broadcast(ctx context.Context, matchID string, state engine.State) {
	conns, err := h.store.ListConnectionsByMatch(ctx, matchID)
	if err != nil {
		log.Printf("list connections for match %s: %v", matchID, err)
		return
	}
	if len(conns) == 0 {
		return
	}

	projs := make([]projSnap, len(state.Projectiles))
	for i, p := range state.Projectiles {
		projs[i] = projSnap{Position: p.Position, Facing: int(p.Facing), Owner: p.Owner}
	}
	msg := tickUpdateMsg{
		Event: "TICK_UPDATE",
		Tick:  state.Tick,
		TankA: tankSnap{Position: state.Tanks[0].Position, Facing: int(state.Tanks[0].Facing), HP: state.Tanks[0].HP},
		TankB: tankSnap{Position: state.Tanks[1].Position, Facing: int(state.Tanks[1].Facing), HP: state.Tanks[1].HP},
		Projectiles: projs,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("marshal TICK_UPDATE: %v", err)
		return
	}
	for _, conn := range conns {
		h.postToConnection(ctx, conn.ConnectionID, matchID, data)
	}
}

// broadcastMatchOver sends a MATCH_OVER event to all live observers.
func (h *handler) broadcastMatchOver(ctx context.Context, matchID string, result *engine.Result) {
	conns, err := h.store.ListConnectionsByMatch(ctx, matchID)
	if err != nil {
		log.Printf("list connections for match %s: %v", matchID, err)
		return
	}
	if len(conns) == 0 {
		return
	}
	msg := matchOverMsg{
		Event:  "MATCH_OVER",
		Winner: winnerStr(result.Winner),
		Reason: string(result.Reason),
		Stats: matchOverStats{
			DamageA:      result.DamageA,
			DamageB:      result.DamageB,
			MovesA:       result.MovesA,
			MovesB:       result.MovesB,
			TicksElapsed: result.TicksElapsed,
			Flawless:     result.Flawless,
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("marshal MATCH_OVER: %v", err)
		return
	}
	for _, conn := range conns {
		h.postToConnection(ctx, conn.ConnectionID, matchID, data)
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
func (h *handler) updateRankedStats(ctx context.Context, match db.Match, verA, verB db.TankVersion, result *engine.Result) {
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
	updateStats(match.TankA.TankID, match.TankA.Version, wonA, result.DamageA, result.TicksElapsed)
	updateStats(match.TankB.TankID, match.TankB.Version, wonB, result.DamageB, result.TicksElapsed)
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
func engineResultToLog(r *engine.Result) *logResult {
	if r == nil {
		return nil
	}
	return &logResult{
		Winner:       winnerStr(r.Winner),
		Reason:       string(r.Reason),
		DamageA:      r.DamageA,
		DamageB:      r.DamageB,
		MovesA:       r.MovesA,
		MovesB:       r.MovesB,
		TicksElapsed: r.TicksElapsed,
		Flawless:     r.Flawless,
	}
}

// engineResultToDB converts an engine.Result to the db.MatchResult format.
// Winner is 0, 1, or nil (for both_lose).
func engineResultToDB(r *engine.Result) db.MatchResult {
	var winner *int
	if r.Winner >= 0 {
		w := r.Winner
		winner = &w
	}
	return db.MatchResult{
		Winner:       winner,
		Reason:       string(r.Reason),
		DamageA:      r.DamageA,
		DamageB:      r.DamageB,
		MovesA:       r.MovesA,
		MovesB:       r.MovesB,
		TicksElapsed: r.TicksElapsed,
		Flawless:     r.Flawless,
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
