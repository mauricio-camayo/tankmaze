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

// ---- Types: server → client messages ----------------------------------------

type matchSnapshotMsg struct {
	Event     string          `json:"event"`
	MatchID   string          `json:"matchId"`
	MatchType string          `json:"matchType"`
	Status    string          `json:"status"`
	TankA     db.MatchTank    `json:"tankA"`
	TankB     db.MatchTank    `json:"tankB"`
	MazeSeed  string          `json:"mazeSeed,omitempty"`
	MapID     string          `json:"mapId,omitempty"`
	Result    *db.MatchResult `json:"result,omitempty"`
}

type tankSnap struct {
	Position [2]int `json:"position"` // [row, col]
	Facing   int    `json:"facing"`
	HP       int    `json:"hp"`
}

type tickUpdateMsg struct {
	Event       string     `json:"event"`
	Tick        int        `json:"tick"`
	TankA       tankSnap   `json:"tankA"`
	TankB       tankSnap   `json:"tankB"`
	Projectiles []struct{} `json:"projectiles"` // populated by match-runner; empty in replay
}

type matchOverStats struct {
	DamageA      int  `json:"damageA"`
	DamageB      int  `json:"damageB"`
	MovesA       int  `json:"movesA"`
	MovesB       int  `json:"movesB"`
	TicksElapsed int  `json:"ticksElapsed"`
	Flawless     bool `json:"flawless"`
}

type matchOverMsg struct {
	Event  string         `json:"event"`
	Winner *string        `json:"winner"` // "a", "b", or null
	Reason string         `json:"reason"`
	Stats  matchOverStats `json:"stats"`
}

type replayAckMsg struct {
	Event string `json:"event"`
	Tick  int    `json:"tick,omitempty"`
	Speed string `json:"speed,omitempty"`
}

type errMsg struct {
	Event   string `json:"event"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ---- Types: S3 tick log -----------------------------------------------------

// tickLog is the gzip-compressed JSON written to S3 by match-runner.
// Only the fields needed by the wss-handler are unmarshalled; the rest
// (sensors detail, actions, log lines, etc.) are intentionally ignored.
type tickLog struct {
	MatchID string      `json:"matchId"`
	Ticks   []tickEntry `json:"ticks"`
	Result  *logResult  `json:"result"`
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

	// Send MATCH_SNAPSHOT with whatever is known at this point.
	snapshot := matchSnapshotMsg{
		Event:     "MATCH_SNAPSHOT",
		MatchID:   match.MatchID,
		MatchType: match.MatchType,
		Status:    match.Status,
		TankA:     match.TankA,
		TankB:     match.TankB,
		MazeSeed:  match.MazeSeed,
		MapID:     match.MapID,
		Result:    match.Result,
	}
	if err := h.send(ctx, connID, snapshot); err != nil {
		return resp(200), nil
	}

	// Stream replay only for completed matches that have a tick log.
	if match.Status != "ended" || match.TickLogS3Key == "" {
		return resp(200), nil
	}

	fromTick := conn.ReplayTick
	speed := conn.ReplaySpeed
	if speed == "" {
		speed = "1"
	}

	if err := h.streamReplay(ctx, connID, match.TickLogS3Key, fromTick, speed); err != nil {
		log.Printf("observe: stream replay for match %s: %v", match.MatchID, err)
	}

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

	_ = h.send(ctx, connID, replayAckMsg{Event: "REPLAY_SEEK", Tick: p.Tick})
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

	_ = h.send(ctx, connID, replayAckMsg{Event: "REPLAY_SPEED", Speed: p.Multiplier})
	return resp(200), nil
}

// ---- Replay streaming -------------------------------------------------------

// streamReplay downloads the gzip tick log from S3 and sends TICK_UPDATE
// events to the observer connection, sleeping between ticks according to speed.
// A MATCH_OVER event is sent after the last tick.
func (h *handler) streamReplay(ctx context.Context, connID, s3Key string, fromTick int, speed string) error {
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
		msg := tickUpdateMsg{
			Event: "TICK_UPDATE",
			Tick:  entry.Tick,
			TankA: tankSnap{
				// Position is stored as Point{X:col, Y:row}; send as [row, col].
				Position: [2]int{entry.A.Sensors.Position.Y, entry.A.Sensors.Position.X},
				Facing:   entry.A.Sensors.Facing,
				HP:       entry.A.Sensors.HP,
			},
			TankB: tankSnap{
				Position: [2]int{entry.B.Sensors.Position.Y, entry.B.Sensors.Position.X},
				Facing:   entry.B.Sensors.Facing,
				HP:       entry.B.Sensors.HP,
			},
		}
		if err := h.send(ctx, connID, msg); err != nil {
			return err
		}
	}

	if tl.Result != nil {
		over := matchOverMsg{
			Event:  "MATCH_OVER",
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
		_ = h.send(ctx, connID, over)
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
	return h.send(ctx, connID, errMsg{Event: "ERROR", Code: code, Message: message})
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
