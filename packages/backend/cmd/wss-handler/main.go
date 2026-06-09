// Package main implements the TankMaze observer WebSocket Lambda.
//
// Routes:
//
//	$connect    – validates matchId query param; stores connection in DynamoDB.
//	             Returns 200 to allow the connection or 4xx to reject it.
//	$disconnect – removes the connection record. Always returns 200.
//	$default    – routes client actions:
//	               OBSERVE      → sends MATCH_SNAPSHOT; streams replay if match ended.
//	               REPLAY_SEEK  → stores seek position in DynamoDB; ack'd to client.
//	               REPLAY_SPEED → stores replay speed in DynamoDB; ack'd to client.
//
// Replay model: REPLAY_SEEK and REPLAY_SPEED persist state into the connection
// record. The next OBSERVE invocation reads them to resume streaming from the
// correct tick at the requested speed. This fits Lambda's per-message invocation
// model without requiring inter-invocation coordination.
package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi"
	agwtypes "github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/tankmaze/backend/internal/db"
	"github.com/tankmaze/backend/internal/maze"
)

// ---- Types: client → server messages ----------------------------------------

type clientMsg struct {
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type observePayload struct {
	MatchID string `json:"matchId"`
}

type replaySeekPayload struct {
	Tick int `json:"tick"`
}

type replaySpeedPayload struct {
	Multiplier string `json:"multiplier"`
}

// ---- Types: server → client envelope ----------------------------------------
//
// All outbound messages use { "type": "<EVENT>", "payload": { ... } } so the
// frontend ws.ts can dispatch on msg.type in a single switch.

type wsEnvelope struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// ---- Types: server → client payloads ----------------------------------------

// snapshotPayload matches the frontend MatchSnapshot interface.
type snapshotPayload struct {
	MatchID     string      `json:"matchId"`
	Status      string      `json:"status"`
	Maze        [][]bool    `json:"maze"`
	TankA       tankStateWS `json:"tankA"`
	TankB       tankStateWS `json:"tankB"`
	Projectiles []projWS    `json:"projectiles"`
	Tick        int         `json:"tick"`
	TotalTicks  int         `json:"totalTicks,omitempty"`
}

// tankStateWS matches the frontend TankState interface.
type tankStateWS struct {
	TankID   string   `json:"tankId"`
	Version  string   `json:"version"`
	Position pointWS  `json:"position"`
	Facing   string   `json:"facing"`
	HP       int      `json:"hp"`
	Config   configWS `json:"config"`
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

// tickPayload matches the frontend TickUpdate interface.
type tickPayload struct {
	Tick        int       `json:"tick"`
	TankA       tankStateWS `json:"tankA"`
	TankB       tankStateWS `json:"tankB"`
	Projectiles []projWS  `json:"projectiles"`
}

type matchOverStats struct {
	DamageA      int  `json:"damageA"`
	DamageB      int  `json:"damageB"`
	MovesA       int  `json:"movesA"`
	MovesB       int  `json:"movesB"`
	TicksElapsed int  `json:"ticksElapsed"`
	Flawless     bool `json:"flawless"`
}

type matchOverPayload struct {
	Winner *string        `json:"winner"` // "a", "b", or null
	Reason string         `json:"reason"`
	Stats  matchOverStats `json:"stats"`
}

type replayAckMsg struct {
	Tick  int    `json:"tick,omitempty"`
	Speed string `json:"speed,omitempty"`
}

type errMsg struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// cardinalStr converts a 0-based int direction (N=0,S=1,E=2,W=3) to its letter.
var cardinalStr = [4]string{0: "N", 1: "S", 2: "E", 3: "W"}

// ---- Types: S3 tick log -----------------------------------------------------

// tickLog is the gzip-compressed JSON written to S3 by match-runner.
// Only the fields needed by the wss-handler are unmarshalled; the rest
// (actions, log lines, etc.) are intentionally ignored.
type tickLog struct {
	MatchID string       `json:"matchId"`
	Maze    [][]bool     `json:"maze"`
	Tanks   logTankPair  `json:"tanks"`
	Ticks   []tickEntry  `json:"ticks"`
	Result  *logResult   `json:"result"`
}

// logTankPair holds metadata for both tanks, written by match-runner at the top of the log.
type logTankPair struct {
	A logTankMeta `json:"a"`
	B logTankMeta `json:"b"`
}

type logTankMeta struct {
	TankID  string          `json:"tankId"`
	Version string          `json:"version"`
	Config  db.VersionConfig `json:"config"`
}

type tickEntry struct {
	Tick int          `json:"tick"`
	A    logTankEntry `json:"a"`
	B    logTankEntry `json:"b"`
}

// logTankEntry holds only the fields wss-handler needs from each tank per tick.
// The match-runner writes the full sensors struct; we decode just what we need.
type logTankEntry struct {
	Sensors logSensors `json:"sensors"`
}

// logSensors mirrors the JSON serialisation of tankmaze.Sensors.
// Field names are capitalised (no json tags on the SDK struct).
type logSensors struct {
	Facing   int `json:"Facing"`
	Position struct {
		X int `json:"X"` // col
		Y int `json:"Y"` // row
	} `json:"Position"`
	HP int `json:"HP"`
}

// logResult mirrors the match result as written by match-runner.
// Winner is "a", "b", or null (not an integer) in the tick log.
type logResult struct {
	Winner       *string `json:"winner"`
	Reason       string  `json:"reason"`
	DamageA      int     `json:"damageA"`
	DamageB      int     `json:"damageB"`
	MovesA       int     `json:"movesA"`
	MovesB       int     `json:"movesB"`
	TicksElapsed int     `json:"ticksElapsed"`
	Flawless     bool    `json:"flawless"`
}

// ---- Handler ----------------------------------------------------------------

type handler struct {
	store           *db.Store
	apigw           *apigatewaymanagementapi.Client
	s3              *s3.Client
	matchLogsBucket string
}

var h *handler // initialised once per cold start in main()

func main() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}

	h = &handler{
		store: db.New(dynamodb.NewFromConfig(cfg)),
		apigw: apigatewaymanagementapi.NewFromConfig(cfg, func(o *apigatewaymanagementapi.Options) {
			o.BaseEndpoint = aws.String(os.Getenv("APIGW_ENDPOINT"))
		}),
		s3:              s3.NewFromConfig(cfg),
		matchLogsBucket: os.Getenv("MATCH_LOGS_BUCKET"),
	}

	lambda.Start(h.handle)
}

func (h *handler) handle(ctx context.Context, req events.APIGatewayWebsocketProxyRequest) (events.APIGatewayProxyResponse, error) {
	switch req.RequestContext.RouteKey {
	case "$connect":
		return h.handleConnect(ctx, req)
	case "$disconnect":
		return h.handleDisconnect(ctx, req)
	default:
		return h.handleDefault(ctx, req)
	}
}

// ---- $connect ---------------------------------------------------------------

func (h *handler) handleConnect(ctx context.Context, req events.APIGatewayWebsocketProxyRequest) (events.APIGatewayProxyResponse, error) {
	matchID := req.QueryStringParameters["matchId"]
	if matchID == "" {
		return resp(400), nil
	}

	if _, err := h.store.GetMatch(ctx, matchID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return resp(404), nil
		}
		log.Printf("connect: get match %s: %v", matchID, err)
		return resp(500), nil
	}

	conn := db.Connection{
		ConnectionID: req.RequestContext.ConnectionID,
		MatchID:      matchID,
		TTL:          time.Now().Add(2 * time.Hour).Unix(),
	}
	if err := h.store.PutConnection(ctx, conn); err != nil {
		log.Printf("connect: put connection: %v", err)
		return resp(500), nil
	}

	return resp(200), nil
}

// ---- $disconnect ------------------------------------------------------------

func (h *handler) handleDisconnect(ctx context.Context, req events.APIGatewayWebsocketProxyRequest) (events.APIGatewayProxyResponse, error) {
	if err := h.store.DeleteConnection(ctx, req.RequestContext.ConnectionID); err != nil {
		log.Printf("disconnect: delete connection: %v", err)
	}
	return resp(200), nil
}

// ---- $default ---------------------------------------------------------------

func (h *handler) handleDefault(ctx context.Context, req events.APIGatewayWebsocketProxyRequest) (events.APIGatewayProxyResponse, error) {
	connID := req.RequestContext.ConnectionID

	var msg clientMsg
	if err := json.Unmarshal([]byte(req.Body), &msg); err != nil {
		_ = h.sendErr(ctx, connID, "invalid_message", "request body is not valid JSON")
		return resp(200), nil
	}

	switch msg.Action {
	case "OBSERVE":
		return h.handleObserve(ctx, connID, msg.Payload)
	case "REPLAY_SEEK":
		return h.handleReplaySeek(ctx, connID, msg.Payload)
	case "REPLAY_SPEED":
		return h.handleReplaySpeed(ctx, connID, msg.Payload)
	default:
		_ = h.sendErr(ctx, connID, "unknown_action", fmt.Sprintf("unknown action %q", msg.Action))
		return resp(200), nil
	}
}

// handleObserve sends MATCH_SNAPSHOT immediately and, for ended matches,
// streams the full replay from the connection's stored seek position.
func (h *handler) handleObserve(ctx context.Context, connID string, raw json.RawMessage) (events.APIGatewayProxyResponse, error) {
	conn, err := h.store.GetConnection(ctx, connID)
	if err != nil {
		_ = h.sendErr(ctx, connID, "internal_error", "could not load connection")
		return resp(200), nil
	}

	match, err := h.store.GetMatch(ctx, conn.MatchID)
	if err != nil {
		_ = h.sendErr(ctx, connID, "match_not_found", "match not found")
		return resp(200), nil
	}

	// For ended matches, load the tick log once — it contains the maze, tank
	// metadata, all ticks, and the result. Build the snapshot from tick 0.
	if match.Status == "ended" && match.TickLogS3Key != "" {
		fromTick := conn.ReplayTick
		speed := conn.ReplaySpeed
		if speed == "" {
			speed = "1"
		}
		if err := h.streamReplayWithSnapshot(ctx, connID, match.TickLogS3Key, fromTick, speed); err != nil {
			log.Printf("observe: stream replay for match %s: %v", match.MatchID, err)
		}
		return resp(200), nil
	}

	// For active (or scheduled) matches, build a minimal snapshot from the
	// match record + version configs so the frontend can render the maze.
	// Tank positions will be corrected by the first incoming TICK_UPDATE.
	verA, errA := h.store.GetVersion(ctx, match.TankA.TankID, match.TankA.Version)
	verB, errB := h.store.GetVersion(ctx, match.TankB.TankID, match.TankB.Version)
	if errA != nil || errB != nil {
		_ = h.sendErr(ctx, connID, "internal_error", "could not load version records")
		return resp(200), nil
	}

	mazeGrid, mazeErr := h.loadMaze(ctx, match)
	if mazeErr != nil {
		mazeGrid = nil
	}

	tankNameA := match.TankA.TankID
	tankNameB := match.TankB.TankID

	snap := snapshotPayload{
		MatchID:     match.MatchID,
		Status:      match.Status,
		Maze:        mazeGrid,
		TankA:       makeTankStateWS(match.TankA.TankID, match.TankA.Version, pointWS{0, 0}, "N", 100, verA.Config, tankNameA),
		TankB:       makeTankStateWS(match.TankB.TankID, match.TankB.Version, pointWS{0, 0}, "N", 100, verB.Config, tankNameB),
		Projectiles: []projWS{},
		Tick:        0,
	}
	_ = h.send(ctx, connID, wsEnvelope{Type: "MATCH_SNAPSHOT", Payload: snap})
	return resp(200), nil
}

// handleReplaySeek stores the requested seek position and acknowledges it.
// The next OBSERVE will stream from this tick.
func (h *handler) handleReplaySeek(ctx context.Context, connID string, raw json.RawMessage) (events.APIGatewayProxyResponse, error) {
	conn, err := h.store.GetConnection(ctx, connID)
	if err != nil {
		_ = h.sendErr(ctx, connID, "internal_error", "could not load connection")
		return resp(200), nil
	}

	var p replaySeekPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.Tick < 0 {
		_ = h.sendErr(ctx, connID, "invalid_payload", "tick must be a non-negative integer")
		return resp(200), nil
	}

	speed := conn.ReplaySpeed
	if speed == "" {
		speed = "1"
	}
	if err := h.store.UpdateConnectionReplay(ctx, connID, p.Tick, speed); err != nil {
		log.Printf("replay-seek: update connection: %v", err)
	}

	_ = h.send(ctx, connID, wsEnvelope{Type: "REPLAY_SEEK", Payload: replayAckMsg{Tick: p.Tick}})
	return resp(200), nil
}

// handleReplaySpeed stores the requested playback speed and acknowledges it.
// The next OBSERVE will stream at this speed.
func (h *handler) handleReplaySpeed(ctx context.Context, connID string, raw json.RawMessage) (events.APIGatewayProxyResponse, error) {
	conn, err := h.store.GetConnection(ctx, connID)
	if err != nil {
		_ = h.sendErr(ctx, connID, "internal_error", "could not load connection")
		return resp(200), nil
	}

	var p replaySpeedPayload
	if err := json.Unmarshal(raw, &p); err != nil || !validSpeed(p.Multiplier) {
		_ = h.sendErr(ctx, connID, "invalid_payload", "multiplier must be one of: 0.25, 0.5, 1, 2, 4, 8, step")
		return resp(200), nil
	}

	if err := h.store.UpdateConnectionReplay(ctx, connID, conn.ReplayTick, p.Multiplier); err != nil {
		log.Printf("replay-speed: update connection: %v", err)
	}

	_ = h.send(ctx, connID, wsEnvelope{Type: "REPLAY_SPEED", Payload: replayAckMsg{Speed: p.Multiplier}})
	return resp(200), nil
}

// ---- Replay streaming -------------------------------------------------------

// streamReplayWithSnapshot downloads the gzip tick log from S3, sends a
// MATCH_SNAPSHOT (with maze from the log), then streams TICK_UPDATE events
// sleeping between ticks according to speed. Sends MATCH_OVER at the end.
func (h *handler) streamReplayWithSnapshot(ctx context.Context, connID, s3Key string, fromTick int, speed string) error {
	out, err := h.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(h.matchLogsBucket),
		Key:    aws.String(s3Key),
	})
	if err != nil {
		return fmt.Errorf("get tick log: %w", err)
	}
	defer out.Body.Close()

	gr, err := gzip.NewReader(out.Body)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	var tl tickLog
	if err := json.NewDecoder(gr).Decode(&tl); err != nil {
		return fmt.Errorf("decode tick log: %w", err)
	}

	// Build initial TankState from the first tick that is at or after fromTick.
	// Falls back to tick 0 when seeking.
	var initA, initB tankStateWS
	for _, entry := range tl.Ticks {
		dirA := cardinalFromInt(entry.A.Sensors.Facing)
		dirB := cardinalFromInt(entry.B.Sensors.Facing)
		initA = makeTankStateWS(
			tl.Tanks.A.TankID, tl.Tanks.A.Version,
			pointWS{X: entry.A.Sensors.Position.X, Y: entry.A.Sensors.Position.Y},
			dirA, entry.A.Sensors.HP, tl.Tanks.A.Config, tl.Tanks.A.TankID,
		)
		initB = makeTankStateWS(
			tl.Tanks.B.TankID, tl.Tanks.B.Version,
			pointWS{X: entry.B.Sensors.Position.X, Y: entry.B.Sensors.Position.Y},
			dirB, entry.B.Sensors.HP, tl.Tanks.B.Config, tl.Tanks.B.TankID,
		)
		if entry.Tick >= fromTick {
			break
		}
	}

	// tl.Maze uses engine convention (true=open); invert to frontend (true=wall).
	snap := snapshotPayload{
		MatchID:     tl.MatchID,
		Status:      "ended",
		Maze:        invertMaze(tl.Maze),
		TankA:       initA,
		TankB:       initB,
		Projectiles: []projWS{},
		Tick:        fromTick,
		TotalTicks:  len(tl.Ticks),
	}
	if err := h.send(ctx, connID, wsEnvelope{Type: "MATCH_SNAPSHOT", Payload: snap}); err != nil {
		return err
	}

	delay := replayDelay(speed)

	for _, entry := range tl.Ticks {
		if entry.Tick < fromTick {
			continue
		}
		if delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		tick := tickPayload{
			Tick: entry.Tick,
			TankA: makeTankStateWS(
				tl.Tanks.A.TankID, tl.Tanks.A.Version,
				pointWS{X: entry.A.Sensors.Position.X, Y: entry.A.Sensors.Position.Y},
				cardinalFromInt(entry.A.Sensors.Facing), entry.A.Sensors.HP,
				tl.Tanks.A.Config, tl.Tanks.A.TankID,
			),
			TankB: makeTankStateWS(
				tl.Tanks.B.TankID, tl.Tanks.B.Version,
				pointWS{X: entry.B.Sensors.Position.X, Y: entry.B.Sensors.Position.Y},
				cardinalFromInt(entry.B.Sensors.Facing), entry.B.Sensors.HP,
				tl.Tanks.B.Config, tl.Tanks.B.TankID,
			),
			Projectiles: []projWS{}, // projectile state not stored in tick log
		}
		if err := h.send(ctx, connID, wsEnvelope{Type: "TICK_UPDATE", Payload: tick}); err != nil {
			return err
		}
	}

	if tl.Result != nil {
		over := matchOverPayload{
			Winner: tl.Result.Winner,
			Reason: tl.Result.Reason,
			Stats: matchOverStats{
				DamageA:      tl.Result.DamageA,
				DamageB:      tl.Result.DamageB,
				MovesA:       tl.Result.MovesA,
				MovesB:       tl.Result.MovesB,
				TicksElapsed: tl.Result.TicksElapsed,
				Flawless:     tl.Result.Flawless,
			},
		}
		_ = h.send(ctx, connID, wsEnvelope{Type: "MATCH_OVER", Payload: over})
	}

	return nil
}

// ---- Helpers ----------------------------------------------------------------

// send marshals v and posts it to the WebSocket connection. Stale (410 Gone)
// connections are cleaned up from DynamoDB.
func (h *handler) send(ctx context.Context, connID string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	_, err = h.apigw.PostToConnection(ctx, &apigatewaymanagementapi.PostToConnectionInput{
		ConnectionId: aws.String(connID),
		Data:         data,
	})
	if err != nil {
		var gone *agwtypes.GoneException
		if errors.As(err, &gone) {
			_ = h.store.DeleteConnection(ctx, connID)
		}
		return err
	}
	return nil
}

func (h *handler) sendErr(ctx context.Context, connID, code, message string) error {
	return h.send(ctx, connID, wsEnvelope{Type: "ERROR", Payload: errMsg{Code: code, Message: message}})
}

// makeTankStateWS builds a tankStateWS from individual fields.
func makeTankStateWS(tankID, version string, pos pointWS, facing string, hp int, cfg db.VersionConfig, name string) tankStateWS {
	return tankStateWS{
		TankID:   tankID,
		Version:  version,
		Position: pos,
		Facing:   facing,
		HP:       hp,
		Config: configWS{
			Name:        name,
			Speed:       cfg.Speed,
			SensorRange: cfg.SensorRange,
			Damage:      cfg.Damage,
			Armor:       cfg.Armor,
			FireRate:    cfg.FireRate,
		},
	}
}

// cardinalFromInt converts a Direction int (N=0,S=1,E=2,W=3) to its letter.
func cardinalFromInt(d int) string {
	if d >= 0 && d < len(cardinalStr) {
		return cardinalStr[d]
	}
	return "N"
}

// invertMaze converts engine convention (true=open) to frontend convention (true=wall).
func invertMaze(cells [][]bool) [][]bool {
	out := make([][]bool, len(cells))
	for r, row := range cells {
		out[r] = make([]bool, len(row))
		for c, open := range row {
			out[r][c] = !open
		}
	}
	return out
}

// loadMaze builds a MazeGrid from the match's MapID (static) or MazeSeed (generated).
func (h *handler) loadMaze(ctx context.Context, match db.Match) ([][]bool, error) {
	if match.MapID != "" {
		m, err := h.store.GetMapByID(ctx, match.MapID)
		if err != nil {
			return nil, fmt.Errorf("get map %s: %w", match.MapID, err)
		}
		grid, err := maze.Load(m.Layout)
		if err != nil {
			return nil, err
		}
		return invertMaze(grid.Cells), nil
	}
	seed, err := strconv.ParseInt(match.MazeSeed, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse maze seed %q: %w", match.MazeSeed, err)
	}
	grid := maze.Generate(seed, maze.SizeFromEnv())
	return invertMaze(grid.Cells), nil
}

func resp(code int) events.APIGatewayProxyResponse {
	return events.APIGatewayProxyResponse{StatusCode: code}
}

// replayDelay returns the sleep duration between TICK_UPDATE events for the
// given speed multiplier. "step" and unknown values use real-time (100 ms).
func replayDelay(speed string) time.Duration {
	switch speed {
	case "0.25":
		return 400 * time.Millisecond
	case "0.5":
		return 200 * time.Millisecond
	case "1", "":
		return 100 * time.Millisecond
	case "2":
		return 50 * time.Millisecond
	case "4":
		return 25 * time.Millisecond
	case "8":
		return 13 * time.Millisecond
	case "step":
		return 0
	default:
		return 100 * time.Millisecond
	}
}

func validSpeed(s string) bool {
	switch s {
	case "0.25", "0.5", "1", "2", "4", "8", "step":
		return true
	}
	return false
}
