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
	Action     string          `json:"action"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Tick       int             `json:"tick"`
	Multiplier string          `json:"multiplier,omitempty"`
}

type observePayload struct {
	MatchID string `json:"matchId"`
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
	TankID    string   `json:"tankId"`
	Version   string   `json:"version"`
	Position  pointWS  `json:"position"`
	Facing    string   `json:"facing"`
	HP        int      `json:"hp"`
	Config    configWS `json:"config"`
	AvatarURL string   `json:"avatarUrl,omitempty"`
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
	Tick        int               `json:"tick"`
	TankA       tickTankStateWS   `json:"tankA"`
	TankB       tickTankStateWS   `json:"tankB"`
	Projectiles []projWS          `json:"projectiles"`
}

// tickTankStateWS extends tankStateWS with per-tick debug fields (action, sensors, log).
type tickTankStateWS struct {
	tankStateWS
	Action     *actionWS       `json:"action,omitempty"`
	Sensors    json.RawMessage `json:"sensors,omitempty"`
	Log        []string        `json:"log,omitempty"`
	DurationMs int64           `json:"durationMs,omitempty"`
	Violation  bool            `json:"violation,omitempty"`
}

type actionWS struct {
	Type      string `json:"type"`
	Direction string `json:"direction,omitempty"`
}

var actionTypeNames = []string{"idle", "move", "rotate", "fire"}
var moveDirNames = []string{"forward", "left", "right", "backward"}

// buildTickTankStateWS converts a logTankEntry into the WebSocket payload for one
// tank. When ranked is true, sensors, memory (embedded in sensors blob), and log
// lines are omitted — they contain private per-tank state that must not be sent
// to unauthenticated observers (WS-1 server-side data stripping).
func buildTickTankStateWS(tankID, version string, e logTankEntry, cfg db.VersionConfig, info tankInfoWS, ranked bool) tickTankStateWS {
	s := parseSensors(e.Sensors)
	base := makeTankStateWS(tankID, version,
		pointWS{X: s.Position.X, Y: s.Position.Y},
		cardinalFromInt(s.Facing), s.HP, cfg, info,
	)
	t := tickTankStateWS{tankStateWS: base}
	if e.DurationMs > 0 {
		t.DurationMs = e.DurationMs
	}
	if e.Violation {
		t.Violation = true
	}
	// Omit sensors and log for ranked matches — private opponent data (WS-1).
	if !ranked {
		if len(e.Log) > 0 {
			t.Log = e.Log
		}
		if len(e.Sensors) > 0 {
			t.Sensors = e.Sensors
		}
	}
	// Convert int ActionType to human-readable string; omit idle actions.
	if e.Action.Type > 0 && e.Action.Type < len(actionTypeNames) {
		dir := ""
		if e.Action.Direction < len(moveDirNames) {
			dir = moveDirNames[e.Action.Direction]
		}
		t.Action = &actionWS{Type: actionTypeNames[e.Action.Type], Direction: dir}
	}
	return t
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
	TankID  string           `json:"tankId"`
	Version string           `json:"version"`
	Name    string           `json:"name,omitempty"`
	Config  db.VersionConfig `json:"config"`
}

type tickEntry struct {
	Tick        int           `json:"tick"`
	A           logTankEntry  `json:"a"`
	B           logTankEntry  `json:"b"`
	Projectiles []logProjEntry `json:"projectiles"`
}

type logProjEntry struct {
	X     int    `json:"x"`
	Y     int    `json:"y"`
	Dir   string `json:"dir"`
	Owner int    `json:"owner"`
}

// logTankEntry holds one tank's data per tick as written by match-runner.
type logTankEntry struct {
	Sensors    json.RawMessage `json:"sensors"`   // raw pass-through to frontend
	Action     logAction       `json:"action"`
	DurationMs int64           `json:"durationMs"`
	Violation  bool            `json:"violation"`
	Log        []string        `json:"log"`
}

type logAction struct {
	Type      int `json:"Type"`      // tankmaze.ActionType: 0=Idle,1=Move,2=Rotate,3=Fire
	Direction int `json:"Direction"` // tankmaze.MoveDirection: 0=Forward,1=Left,2=Right,3=Backward
}

// logSensors is a minimal decode of tankmaze.Sensors used only for position/facing/HP.
// Field names are capitalised to match Go's default JSON serialisation (no json tags on SDK struct).
type logSensors struct {
	Facing   int `json:"Facing"`
	Position struct {
		X int `json:"X"` // col
		Y int `json:"Y"` // row
	} `json:"Position"`
	HP int `json:"HP"`
}

// parseSensors decodes only the position/facing/HP subset of a raw sensors blob.
func parseSensors(raw json.RawMessage) logSensors {
	var s logSensors
	_ = json.Unmarshal(raw, &s)
	return s
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

func isValidUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

func (h *handler) handleConnect(ctx context.Context, req events.APIGatewayWebsocketProxyRequest) (events.APIGatewayProxyResponse, error) {
	matchID := req.QueryStringParameters["matchId"]
	if matchID == "" || !isValidUUID(matchID) {
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
		return h.handleReplaySeek(ctx, connID, msg)
	case "REPLAY_SPEED":
		return h.handleReplaySpeed(ctx, connID, msg)
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
		isRanked := match.MatchType == "ranked"
		if err := h.streamReplayWithSnapshot(ctx, connID, match.TickLogS3Key, fromTick, speed, isRanked); err != nil {
			log.Printf("observe: stream replay for match %s: %v", match.MatchID, err)
		}
		return resp(200), nil
	}

	// Ended match with no tick log (forfeit or match-runner failure): send a
	// snapshot built from the match record then immediately send MATCH_OVER so
	// the frontend shows the result instead of a frozen, unresponsive canvas.
	if match.Status == "ended" {
		verA, errA := h.store.GetVersion(ctx, match.TankA.TankID, match.TankA.Version)
		verB, errB := h.store.GetVersion(ctx, match.TankB.TankID, match.TankB.Version)
		if errA != nil || errB != nil {
			_ = h.sendErr(ctx, connID, "internal_error", "could not load version records")
			return resp(200), nil
		}
		mazeGrid, mazeErr := h.loadMaze(ctx, match)
		if mazeErr != nil {
			log.Printf("observe: load maze for forfeit match %s: %v", match.MatchID, mazeErr)
			_ = h.sendErr(ctx, connID, "internal_error", "failed to load match maze — try refreshing")
			return resp(200), nil
		}
		infoA := h.lookupTankInfo(ctx, match.TankA)
		infoB := h.lookupTankInfo(ctx, match.TankB)
		snap := snapshotPayload{
			MatchID:     match.MatchID,
			Status:      "ended",
			Maze:        mazeGrid,
			TankA:       makeTankStateWS(match.TankA.TankID, match.TankA.Version, pointWS{0, 0}, "N", 100, verA.Config, infoA),
			TankB:       makeTankStateWS(match.TankB.TankID, match.TankB.Version, pointWS{0, 0}, "N", 100, verB.Config, infoB),
			Projectiles: []projWS{},
			Tick:        0,
			TotalTicks:  0,
		}
		_ = h.send(ctx, connID, wsEnvelope{Type: "MATCH_SNAPSHOT", Payload: snap})
		if match.Result != nil {
			_ = h.send(ctx, connID, wsEnvelope{Type: "MATCH_OVER", Payload: matchOverPayload{
				Winner: dbWinnerToString(match.Result.Winner),
				Reason: match.Result.Reason,
				Stats: matchOverStats{
					DamageA:      match.Result.DamageA,
					DamageB:      match.Result.DamageB,
					MovesA:       match.Result.MovesA,
					MovesB:       match.Result.MovesB,
					TicksElapsed: match.Result.TicksElapsed,
					Flawless:     match.Result.Flawless,
				},
			}})
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
		log.Printf("observe: load maze for match %s: %v", match.MatchID, mazeErr)
		_ = h.sendErr(ctx, connID, "internal_error", "failed to load match maze — try refreshing")
		return resp(200), nil
	}

	infoA := h.lookupTankInfo(ctx, match.TankA)
	infoB := h.lookupTankInfo(ctx, match.TankB)

	snap := snapshotPayload{
		MatchID:     match.MatchID,
		Status:      match.Status,
		Maze:        mazeGrid,
		TankA:       makeTankStateWS(match.TankA.TankID, match.TankA.Version, pointWS{0, 0}, "N", 100, verA.Config, infoA),
		TankB:       makeTankStateWS(match.TankB.TankID, match.TankB.Version, pointWS{0, 0}, "N", 100, verB.Config, infoB),
		Projectiles: []projWS{},
		Tick:        0,
	}
	_ = h.send(ctx, connID, wsEnvelope{Type: "MATCH_SNAPSHOT", Payload: snap})
	return resp(200), nil
}

// handleReplaySeek stores the requested seek position and acknowledges it.
// The next OBSERVE will stream from this tick.
func (h *handler) handleReplaySeek(ctx context.Context, connID string, msg clientMsg) (events.APIGatewayProxyResponse, error) {
	conn, err := h.store.GetConnection(ctx, connID)
	if err != nil {
		_ = h.sendErr(ctx, connID, "internal_error", "could not load connection")
		return resp(200), nil
	}

	if msg.Tick < 0 {
		_ = h.sendErr(ctx, connID, "invalid_payload", "tick must be a non-negative integer")
		return resp(200), nil
	}

	speed := conn.ReplaySpeed
	if speed == "" {
		speed = "1"
	}
	if err := h.store.UpdateConnectionReplay(ctx, connID, msg.Tick, speed); err != nil {
		log.Printf("replay-seek: update connection: %v", err)
	}

	_ = h.send(ctx, connID, wsEnvelope{Type: "REPLAY_SEEK", Payload: replayAckMsg{Tick: msg.Tick}})
	return resp(200), nil
}

// handleReplaySpeed stores the requested playback speed and acknowledges it.
// The next OBSERVE will stream at this speed.
func (h *handler) handleReplaySpeed(ctx context.Context, connID string, msg clientMsg) (events.APIGatewayProxyResponse, error) {
	conn, err := h.store.GetConnection(ctx, connID)
	if err != nil {
		_ = h.sendErr(ctx, connID, "internal_error", "could not load connection")
		return resp(200), nil
	}

	if !validSpeed(msg.Multiplier) {
		_ = h.sendErr(ctx, connID, "invalid_payload", "multiplier must be one of: 0.25, 0.5, 1, 2, 4, 8, step")
		return resp(200), nil
	}

	if err := h.store.UpdateConnectionReplay(ctx, connID, conn.ReplayTick, msg.Multiplier); err != nil {
		log.Printf("replay-speed: update connection: %v", err)
	}

	_ = h.send(ctx, connID, wsEnvelope{Type: "REPLAY_SPEED", Payload: replayAckMsg{Speed: msg.Multiplier}})
	return resp(200), nil
}

// ---- Replay streaming -------------------------------------------------------

// streamReplayWithSnapshot downloads the gzip tick log from S3, sends a
// MATCH_SNAPSHOT (with maze from the log), then streams TICK_UPDATE events
// sleeping between ticks according to speed. Sends MATCH_OVER at the end.
// ranked controls whether private per-tank fields (sensors, log) are stripped
// from TICK_UPDATE payloads (WS-1).
func (h *handler) streamReplayWithSnapshot(ctx context.Context, connID, s3Key string, fromTick int, speed string, ranked bool) error {
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

	// Resolve display metadata from the tick log + DB (for avatarUrl).
	replayInfoA := tankInfoWS{Name: tl.Tanks.A.Name}
	replayInfoB := tankInfoWS{Name: tl.Tanks.B.Name}
	if t, err := h.store.GetTank(ctx, tl.Tanks.A.TankID); err == nil {
		replayInfoA.AvatarURL = t.AvatarURL
		if replayInfoA.Name == "" {
			replayInfoA.Name = t.Name
		}
	}
	if replayInfoA.Name == "" {
		replayInfoA.Name = tl.Tanks.A.TankID
	}
	if t, err := h.store.GetTank(ctx, tl.Tanks.B.TankID); err == nil {
		replayInfoB.AvatarURL = t.AvatarURL
		if replayInfoB.Name == "" {
			replayInfoB.Name = t.Name
		}
	}
	if replayInfoB.Name == "" {
		replayInfoB.Name = tl.Tanks.B.TankID
	}

	// Build initial TankState from the first tick that is at or after fromTick.
	// Falls back to tick 0 when seeking.
	var initA, initB tankStateWS
	for _, entry := range tl.Ticks {
		sA := parseSensors(entry.A.Sensors)
		sB := parseSensors(entry.B.Sensors)
		initA = makeTankStateWS(
			tl.Tanks.A.TankID, tl.Tanks.A.Version,
			pointWS{X: sA.Position.X, Y: sA.Position.Y},
			cardinalFromInt(sA.Facing), sA.HP, tl.Tanks.A.Config, replayInfoA,
		)
		initB = makeTankStateWS(
			tl.Tanks.B.TankID, tl.Tanks.B.Version,
			pointWS{X: sB.Position.X, Y: sB.Position.Y},
			cardinalFromInt(sB.Facing), sB.HP, tl.Tanks.B.Config, replayInfoB,
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
		projs := make([]projWS, len(entry.Projectiles))
		for i, p := range entry.Projectiles {
			ownerID := tl.Tanks.A.TankID
			if p.Owner == 1 {
				ownerID = tl.Tanks.B.TankID
			}
			projs[i] = projWS{
				Position:    pointWS{X: p.X, Y: p.Y},
				Direction:   p.Dir,
				OwnerTankID: ownerID,
			}
		}
		tick := tickPayload{
			Tick:        entry.Tick,
			TankA:       buildTickTankStateWS(tl.Tanks.A.TankID, tl.Tanks.A.Version, entry.A, tl.Tanks.A.Config, replayInfoA, ranked),
			TankB:       buildTickTankStateWS(tl.Tanks.B.TankID, tl.Tanks.B.Version, entry.B, tl.Tanks.B.Config, replayInfoB, ranked),
			Projectiles: projs,
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

// tankInfoWS holds display metadata resolved from the DB for one match participant.
type tankInfoWS struct {
	Name      string
	AvatarURL string
}

// lookupTankInfo returns the display name and avatarUrl for a match tank.
func (h *handler) lookupTankInfo(ctx context.Context, mt db.MatchTank) tankInfoWS {
	info := tankInfoWS{Name: mt.TankName}
	if t, err := h.store.GetTank(ctx, mt.TankID); err == nil {
		if info.Name == "" {
			info.Name = t.Name
		}
		info.AvatarURL = t.AvatarURL
	}
	if info.Name == "" {
		info.Name = mt.TankID
	}
	return info
}

// makeTankStateWS builds a tankStateWS from individual fields.
func makeTankStateWS(tankID, version string, pos pointWS, facing string, hp int, cfg db.VersionConfig, info tankInfoWS) tankStateWS {
	return tankStateWS{
		TankID:    tankID,
		Version:   version,
		Position:  pos,
		Facing:    facing,
		HP:        hp,
		AvatarURL: info.AvatarURL,
		Config: configWS{
			Name:        info.Name,
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

// dbWinnerToString converts the db.MatchResult.Winner *int (0=A, 1=B, nil=both_lose)
// to the "a"/"b"/null string expected by the frontend MATCH_OVER payload.
func dbWinnerToString(w *int) *string {
	if w == nil {
		return nil
	}
	var s string
	if *w == 0 {
		s = "a"
	} else {
		s = "b"
	}
	return &s
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
