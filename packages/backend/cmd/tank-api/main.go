// Package main implements the TankMaze HTTP REST API Lambda.
//
// Routes:
//
//	POST   /tanks                              – create tank (optional ?forkFrom=&forkVersion= for fork)
//	GET    /tanks                              – list caller's tanks
//	GET    /tanks/{id}                         – tank detail + version history
//	POST   /tanks/{id}/versions               – submit Go source → triggers CodeBuild
//	GET    /tanks/{id}/versions/{v}/status    – poll compile status
//	POST   /tanks/{id}/versions/{v}/promote   – promote minor → next major
//	POST   /tanks/{id}/versions/{v}/register  – register major for a Game Day
//	DELETE /tanks/{id}/versions/{v}/register  – withdraw Game Day registration
//	POST   /tanks/{id}/score-transfer         – transfer Global Score to another tank
//	POST   /matches                            – start a test match
//	GET    /matches/{id}                       – match metadata + result
//	GET    /matches/{id}/ticks                 – redirect to pre-signed S3 tick log URL
//	GET    /rankings                           – global leaderboard
//	GET    /gamedays/{id}                      – Game Day bracket and phase status
//	GET    /maps                               – list active maps (no auth required)
//	POST   /maps                               – create map (admin only)
//	PATCH  /maps/{id}                          – update map name/description/isActive (admin only)
package main

import (
	"bytes"
	crand "crypto/rand"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	lambdasvc "github.com/aws/aws-sdk-go-v2/service/lambda"
	ltypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/tankmaze/backend/internal/db"
)

const (
	maxSourceBytes   = 1 * 1024 * 1024 // 1 MiB
	maxTankNameLen   = 64
	testMatchTTLDays = 7
	tickLogPresignTTL = 15 * time.Minute
)

// ---- Request / response body types ------------------------------------------

type createTankBody struct {
	Name string `json:"name"`
}

type submitVersionBody struct {
	Source string `json:"source"`
}

type registerVersionBody struct {
	GameDayID string `json:"gameDayId"`
}

type scoreTransferBody struct {
	TargetTankID string `json:"targetTankId"`
}

type startMatchBody struct {
	TankID   string       `json:"tankId"`
	Version  string       `json:"version"`
	Opponent opponentSpec `json:"opponent"`
	MapID    string       `json:"mapId,omitempty"`
}

type opponentSpec struct {
	Type    string `json:"type"`              // "ai" | "own"
	Name    string `json:"name,omitempty"`    // ai: "scout" | "bruiser"
	TankID  string `json:"tankId,omitempty"`  // own: opponent tank
	Version string `json:"version,omitempty"` // own: opponent version
}

type createMapBody struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Layout      [][]bool `json:"layout"`
}

type updateMapBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsActive    *bool  `json:"isActive"`
}

// ---- Handler ----------------------------------------------------------------

type handler struct {
	store            *db.Store
	s3               *s3.Client
	cb               *codebuild.Client
	lambdaSvc        *lambdasvc.Client
	wasmBucket       string
	logsBucket       string
	codebuildProject string
	matchRunnerFunc  string
	versionsTable    string // forwarded to CodeBuild as env override
	scoutTankID      string
	scoutVersion     string
	bruiserTankID    string
	bruiserVersion   string
}

var h *handler

func main() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}

	h = &handler{
		store:            db.New(dynamodb.NewFromConfig(cfg)),
		s3:               s3.NewFromConfig(cfg),
		cb:               codebuild.NewFromConfig(cfg),
		lambdaSvc:        lambdasvc.NewFromConfig(cfg),
		wasmBucket:       os.Getenv("WASM_BUCKET"),
		logsBucket:       os.Getenv("MATCH_LOGS_BUCKET"),
		codebuildProject: os.Getenv("CODEBUILD_PROJECT"),
		matchRunnerFunc:  os.Getenv("MATCH_RUNNER_FUNCTION"),
		versionsTable:    os.Getenv("TANK_VERSIONS_TABLE"),
		scoutTankID:      os.Getenv("SCOUT_TANK_ID"),
		scoutVersion:     os.Getenv("SCOUT_VERSION"),
		bruiserTankID:    os.Getenv("BRUISER_TANK_ID"),
		bruiserVersion:   os.Getenv("BRUISER_VERSION"),
	}

	lambda.Start(h.handle)
}

// handle routes HTTP API v2 proxy events to individual handlers.
func (h *handler) handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	method := req.RequestContext.HTTP.Method
	rawPath := strings.Trim(req.RawPath, "/")
	parts := strings.Split(rawPath, "/")

	switch {
	// Tanks
	case method == "GET" && rawPath == "tanks":
		return h.listTanks(ctx, req)
	case method == "POST" && rawPath == "tanks":
		return h.createTank(ctx, req)
	case method == "GET" && len(parts) == 2 && parts[0] == "tanks":
		return h.getTank(ctx, req, parts[1])
	case method == "POST" && len(parts) == 3 && parts[0] == "tanks" && parts[2] == "versions":
		return h.submitVersion(ctx, req, parts[1])
	case method == "GET" && len(parts) == 5 && parts[0] == "tanks" && parts[2] == "versions" && parts[4] == "status":
		return h.getVersionStatus(ctx, req, parts[1], parts[3])
	case method == "POST" && len(parts) == 5 && parts[0] == "tanks" && parts[2] == "versions" && parts[4] == "promote":
		return h.promoteVersion(ctx, req, parts[1], parts[3])
	case method == "POST" && len(parts) == 5 && parts[0] == "tanks" && parts[2] == "versions" && parts[4] == "register":
		return h.registerVersion(ctx, req, parts[1], parts[3])
	case method == "DELETE" && len(parts) == 5 && parts[0] == "tanks" && parts[2] == "versions" && parts[4] == "register":
		return h.deregisterVersion(ctx, req, parts[1], parts[3])
	case method == "POST" && len(parts) == 3 && parts[0] == "tanks" && parts[2] == "score-transfer":
		return h.scoreTransfer(ctx, req, parts[1])

	// Matches
	case method == "POST" && rawPath == "matches":
		return h.startMatch(ctx, req)
	case method == "GET" && len(parts) == 2 && parts[0] == "matches":
		return h.getMatch(ctx, req, parts[1])
	case method == "GET" && len(parts) == 3 && parts[0] == "matches" && parts[2] == "ticks":
		return h.getMatchTicks(ctx, req, parts[1])

	// Rankings and Game Days
	case method == "GET" && rawPath == "rankings":
		return h.getRankings(ctx, req)
	case method == "GET" && len(parts) == 2 && parts[0] == "gamedays":
		return h.getGameDay(ctx, req, parts[1])

	// Maps
	case method == "GET" && rawPath == "maps":
		return h.listMaps(ctx, req)
	case method == "POST" && rawPath == "maps":
		return h.createMap(ctx, req)
	case method == "PATCH" && len(parts) == 2 && parts[0] == "maps":
		return h.updateMap(ctx, req, parts[1])

	default:
		return errResp(http.StatusNotFound, "not found"), nil
	}
}

// ---- Tank handlers ----------------------------------------------------------

func (h *handler) listTanks(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	tanks, err := h.store.ListTanksByUser(ctx, uid)
	if err != nil {
		log.Printf("list tanks: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, tanks), nil
}

func (h *handler) createTank(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}

	var body createTankBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return errResp(http.StatusBadRequest, "invalid request body"), nil
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		return errResp(http.StatusBadRequest, "name is required"), nil
	}
	if len(body.Name) > maxTankNameLen {
		return errResp(http.StatusBadRequest, fmt.Sprintf("name must be %d characters or fewer", maxTankNameLen)), nil
	}

	forkFrom := req.QueryStringParameters["forkFrom"]
	forkVersion := req.QueryStringParameters["forkVersion"]

	// Validate fork source before creating the tank record.
	var srcVer *db.TankVersion
	if forkFrom != "" && forkVersion != "" {
		ver, err := h.store.GetVersion(ctx, forkFrom, forkVersion)
		if errors.Is(err, db.ErrNotFound) {
			return errResp(http.StatusNotFound, "fork source not found"), nil
		}
		if err != nil {
			return errResp(http.StatusInternalServerError, "internal error"), nil
		}
		if ver.CompileStatus != "ready" || ver.SourceS3Key == "" {
			return errResp(http.StatusBadRequest, "fork source version is not ready"), nil
		}
		srcVer = &ver
	}

	tankID := newUUID()
	tank := db.Tank{
		TankID:            tankID,
		UserID:            uid,
		Name:              body.Name,
		LastActiveAt:      time.Now().Unix(),
		ForkedFromTankID:  forkFrom,
		ForkedFromVersion: forkVersion,
	}
	if err := h.store.PutTank(ctx, tank); err != nil {
		log.Printf("create tank: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	if srcVer != nil {
		newSrcKey := fmt.Sprintf("%s/v0.1/source.go", tankID)
		newWasmKey := fmt.Sprintf("%s/v0.1/tank.wasm", tankID)
		_, err := h.s3.CopyObject(ctx, &s3.CopyObjectInput{
			Bucket:           aws.String(h.wasmBucket),
			CopySource:       aws.String(h.wasmBucket + "/" + srcVer.SourceS3Key),
			Key:              aws.String(newSrcKey),
			TaggingDirective: s3types.TaggingDirectiveReplace,
			Tagging:          aws.String("versionType=minor"),
		})
		if err != nil {
			log.Printf("fork copy source: %v", err)
		} else {
			ver := db.TankVersion{
				TankID:        tankID,
				Version:       "v0.1",
				VersionType:   "minor",
				SourceS3Key:   newSrcKey,
				CompileStatus: "pending",
				CreatedAt:     time.Now().Unix(),
			}
			if putErr := h.store.PutVersion(ctx, ver); putErr != nil {
				log.Printf("fork put version: %v", putErr)
			} else {
				h.triggerBuild(ctx, tankID, "v0.1", newSrcKey, newWasmKey)
			}
		}
	}

	return jsonResp(http.StatusCreated, map[string]string{"tankId": tankID, "name": tank.Name}), nil
}

func (h *handler) getTank(ctx context.Context, req events.APIGatewayV2HTTPRequest, tankID string) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	tank, err := h.store.GetTank(ctx, tankID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "tank not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if tank.UserID != uid {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	versions, err := h.store.ListVersionsByTank(ctx, tankID)
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]interface{}{
		"tank":     tank,
		"versions": versions,
	}), nil
}

// ---- Version handlers -------------------------------------------------------

func (h *handler) submitVersion(ctx context.Context, req events.APIGatewayV2HTTPRequest, tankID string) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	tank, err := h.store.GetTank(ctx, tankID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "tank not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if tank.UserID != uid {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}

	var body submitVersionBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return errResp(http.StatusBadRequest, "invalid request body"), nil
	}
	if strings.TrimSpace(body.Source) == "" {
		return errResp(http.StatusBadRequest, "source is required"), nil
	}
	if len(body.Source) > maxSourceBytes {
		return errResp(http.StatusBadRequest, "source too large (max 1 MiB)"), nil
	}

	versions, err := h.store.ListVersionsByTank(ctx, tankID)
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	nextVer := nextMinorVersion(versions)
	sourceKey := fmt.Sprintf("%s/%s/source.go", tankID, nextVer)
	wasmKey := fmt.Sprintf("%s/%s/tank.wasm", tankID, nextVer)

	srcBytes := []byte(body.Source)
	_, err = h.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(h.wasmBucket),
		Key:         aws.String(sourceKey),
		Body:        bytes.NewReader(srcBytes),
		ContentType: aws.String("text/plain; charset=utf-8"),
		Tagging:     aws.String("versionType=minor"),
	})
	if err != nil {
		log.Printf("upload source: %v", err)
		return errResp(http.StatusInternalServerError, "failed to upload source"), nil
	}

	ver := db.TankVersion{
		TankID:        tankID,
		Version:       nextVer,
		VersionType:   "minor",
		SourceS3Key:   sourceKey,
		CompileStatus: "pending",
		CreatedAt:     time.Now().Unix(),
	}
	if err := h.store.PutVersion(ctx, ver); err != nil {
		log.Printf("put version: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	h.triggerBuild(ctx, tankID, nextVer, sourceKey, wasmKey)

	return jsonResp(http.StatusCreated, map[string]string{"version": nextVer}), nil
}

func (h *handler) getVersionStatus(ctx context.Context, req events.APIGatewayV2HTTPRequest, tankID, version string) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	tank, err := h.store.GetTank(ctx, tankID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "tank not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if tank.UserID != uid {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	ver, err := h.store.GetVersion(ctx, tankID, version)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "version not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]interface{}{
		"version":       ver.Version,
		"compileStatus": ver.CompileStatus,
		"compileError":  ver.CompileError,
	}), nil
}

func (h *handler) promoteVersion(ctx context.Context, req events.APIGatewayV2HTTPRequest, tankID, version string) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	tank, err := h.store.GetTank(ctx, tankID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "tank not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if tank.UserID != uid {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	ver, err := h.store.GetVersion(ctx, tankID, version)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "version not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if ver.VersionType != "minor" {
		return errResp(http.StatusBadRequest, "only minor versions can be promoted"), nil
	}
	if ver.CompileStatus != "ready" {
		return errResp(http.StatusBadRequest, "version must be compiled (ready) before promotion"), nil
	}

	versions, err := h.store.ListVersionsByTank(ctx, tankID)
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	newMajor := nextMajorVersion(versions)
	now := time.Now().Unix()

	major := db.TankVersion{
		TankID:        tankID,
		Version:       newMajor,
		VersionType:   "major",
		Config:        ver.Config,
		WasmS3Key:     ver.WasmS3Key,
		SourceS3Key:   ver.SourceS3Key,
		WasmSHA256:    ver.WasmSHA256,
		CompileStatus: "ready",
		CreatedAt:     now,
	}
	if err := h.store.PutVersion(ctx, major); err != nil {
		log.Printf("put major version: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	// Re-tag the WASM object as major so the 90-day minor lifecycle rule
	// does not expire it.
	if ver.WasmS3Key != "" {
		_, tagErr := h.s3.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
			Bucket: aws.String(h.wasmBucket),
			Key:    aws.String(ver.WasmS3Key),
			Tagging: &s3types.Tagging{
				TagSet: []s3types.Tag{{
					Key:   aws.String("versionType"),
					Value: aws.String("major"),
				}},
			},
		})
		if tagErr != nil {
			log.Printf("re-tag promoted wasm: %v", tagErr)
		}
	}

	// Set tank.createdAt on first promotion.
	if tank.CreatedAt == 0 {
		updated := tank
		updated.CreatedAt = now
		if putErr := h.store.PutTank(ctx, updated); putErr != nil {
			log.Printf("update tank createdAt: %v", putErr)
		}
	}

	return jsonResp(http.StatusCreated, map[string]string{"version": newMajor}), nil
}

func (h *handler) registerVersion(ctx context.Context, req events.APIGatewayV2HTTPRequest, tankID, version string) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	tank, err := h.store.GetTank(ctx, tankID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "tank not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if tank.UserID != uid {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	ver, err := h.store.GetVersion(ctx, tankID, version)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "version not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if ver.VersionType != "major" {
		return errResp(http.StatusBadRequest, "only major versions can be registered for Game Days"), nil
	}

	var body registerVersionBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil || body.GameDayID == "" {
		return errResp(http.StatusBadRequest, "gameDayId is required"), nil
	}

	gd, err := h.store.GetGameDay(ctx, body.GameDayID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "game day not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if gd.Phases.RoundRobin.Status != "upcoming" {
		return errResp(http.StatusConflict, "game day registration is closed"), nil
	}

	if err := h.store.UpdateVersionRegistration(ctx, tankID, version, body.GameDayID); err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]string{"gameDayId": body.GameDayID}), nil
}

func (h *handler) deregisterVersion(ctx context.Context, req events.APIGatewayV2HTTPRequest, tankID, version string) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	tank, err := h.store.GetTank(ctx, tankID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "tank not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if tank.UserID != uid {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	if err := h.store.UpdateVersionRegistration(ctx, tankID, version, ""); err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]bool{"deregistered": true}), nil
}

// ---- Score transfer ---------------------------------------------------------

func (h *handler) scoreTransfer(ctx context.Context, req events.APIGatewayV2HTTPRequest, tankID string) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	srcTank, err := h.store.GetTank(ctx, tankID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "source tank not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if srcTank.UserID != uid {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	if srcTank.ScoreTransferredTo != "" {
		return errResp(http.StatusConflict, "score has already been transferred"), nil
	}

	var body scoreTransferBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil || body.TargetTankID == "" {
		return errResp(http.StatusBadRequest, "targetTankId is required"), nil
	}
	if body.TargetTankID == tankID {
		return errResp(http.StatusBadRequest, "source and target tank must be different"), nil
	}

	rankings, err := h.store.ListRankingsByTank(ctx, tankID)
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	in := db.ScoreTransferInput{
		SourceTankID:   tankID,
		TargetTankID:   body.TargetTankID,
		SourceRankings: rankings,
		GlobalScore:    srcTank.GlobalScore,
		BestFinish:     srcTank.BestFinish,
		GameDaysCount:  srcTank.GameDaysCount,
		LastActiveAt:   srcTank.LastActiveAt,
	}
	if err := h.store.ScoreTransfer(ctx, in); err != nil {
		log.Printf("score transfer %s→%s: %v", tankID, body.TargetTankID, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]bool{"transferred": true}), nil
}

// ---- Match handlers ---------------------------------------------------------

func (h *handler) startMatch(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}

	var body startMatchBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return errResp(http.StatusBadRequest, "invalid request body"), nil
	}
	if body.TankID == "" || body.Version == "" {
		return errResp(http.StatusBadRequest, "tankId and version are required"), nil
	}

	tank, err := h.store.GetTank(ctx, body.TankID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "tank not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if tank.UserID != uid {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	ver, err := h.store.GetVersion(ctx, body.TankID, body.Version)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "version not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if ver.CompileStatus != "ready" {
		return errResp(http.StatusBadRequest, "version is not ready"), nil
	}

	var oppTankID, oppVersion, matchType string
	switch body.Opponent.Type {
	case "ai":
		switch body.Opponent.Name {
		case "scout":
			oppTankID, oppVersion = h.scoutTankID, h.scoutVersion
		case "bruiser":
			oppTankID, oppVersion = h.bruiserTankID, h.bruiserVersion
		default:
			return errResp(http.StatusBadRequest, "unknown AI opponent; use 'scout' or 'bruiser'"), nil
		}
		if oppTankID == "" || oppVersion == "" {
			return errResp(http.StatusServiceUnavailable, "AI opponent not configured"), nil
		}
		matchType = "test-ai"

	case "own":
		if body.Opponent.TankID == "" || body.Opponent.Version == "" {
			return errResp(http.StatusBadRequest, "opponent tankId and version required for type=own"), nil
		}
		oppTank, err := h.store.GetTank(ctx, body.Opponent.TankID)
		if errors.Is(err, db.ErrNotFound) {
			return errResp(http.StatusNotFound, "opponent tank not found"), nil
		}
		if err != nil {
			return errResp(http.StatusInternalServerError, "internal error"), nil
		}
		if oppTank.UserID != uid {
			return errResp(http.StatusForbidden, "opponent tank not owned by you"), nil
		}
		oppVer, err := h.store.GetVersion(ctx, body.Opponent.TankID, body.Opponent.Version)
		if errors.Is(err, db.ErrNotFound) {
			return errResp(http.StatusNotFound, "opponent version not found"), nil
		}
		if err != nil {
			return errResp(http.StatusInternalServerError, "internal error"), nil
		}
		if oppVer.CompileStatus != "ready" {
			return errResp(http.StatusBadRequest, "opponent version is not ready"), nil
		}
		oppTankID, oppVersion = body.Opponent.TankID, body.Opponent.Version
		matchType = "test-own"

	default:
		return errResp(http.StatusBadRequest, "opponent type must be 'ai' or 'own'"), nil
	}

	var mazeSeed, mapID string
	if body.MapID != "" {
		if _, err := h.store.GetMapByID(ctx, body.MapID); errors.Is(err, db.ErrNotFound) {
			return errResp(http.StatusNotFound, "map not found"), nil
		} else if err != nil {
			return errResp(http.StatusInternalServerError, "internal error"), nil
		}
		mapID = body.MapID
	} else {
		mazeSeed = strconv.FormatInt(rand.Int63(), 10)
	}

	matchID := newUUID()
	match := db.Match{
		MatchID:   matchID,
		MatchType: matchType,
		Status:    "scheduled",
		MazeSeed:  mazeSeed,
		MapID:     mapID,
		TankA:     db.MatchTank{TankID: body.TankID, Version: body.Version},
		TankB:     db.MatchTank{TankID: oppTankID, Version: oppVersion},
		CreatedAt: time.Now().Unix(),
		TTL:       time.Now().AddDate(0, 0, testMatchTTLDays).Unix(),
	}
	if err := h.store.PutMatch(ctx, match); err != nil {
		log.Printf("put match: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	payload, _ := json.Marshal(map[string]string{"matchId": matchID})
	if _, err := h.lambdaSvc.Invoke(ctx, &lambdasvc.InvokeInput{
		FunctionName:   aws.String(h.matchRunnerFunc),
		InvocationType: ltypes.InvocationTypeEvent,
		Payload:        payload,
	}); err != nil {
		log.Printf("invoke match-runner for %s: %v", matchID, err)
	}

	return jsonResp(http.StatusAccepted, map[string]string{"matchId": matchID}), nil
}

func (h *handler) getMatch(ctx context.Context, req events.APIGatewayV2HTTPRequest, matchID string) (events.APIGatewayV2HTTPResponse, error) {
	if userID(req) == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	match, err := h.store.GetMatch(ctx, matchID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "match not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, match), nil
}

func (h *handler) getMatchTicks(ctx context.Context, req events.APIGatewayV2HTTPRequest, matchID string) (events.APIGatewayV2HTTPResponse, error) {
	if userID(req) == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	match, err := h.store.GetMatch(ctx, matchID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "match not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if match.TickLogS3Key == "" {
		return errResp(http.StatusNotFound, "tick log not yet available"), nil
	}

	presigner := s3.NewPresignClient(h.s3)
	presigned, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(h.logsBucket),
		Key:    aws.String(match.TickLogS3Key),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = tickLogPresignTTL
	})
	if err != nil {
		log.Printf("presign tick log: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return events.APIGatewayV2HTTPResponse{
		StatusCode: http.StatusFound,
		Headers:    map[string]string{"Location": presigned.URL},
	}, nil
}

// ---- Rankings and Game Days -------------------------------------------------

func (h *handler) getRankings(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	tanks, err := h.store.ScanTanksByScore(ctx)
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	type entry struct {
		Rank          int    `json:"rank"`
		TankID        string `json:"tankId"`
		Name          string `json:"name"`
		UserID        string `json:"userId"`
		GlobalScore   int    `json:"globalScore"`
		BestFinish    *int   `json:"bestFinish"`
		GameDaysCount int    `json:"gameDaysCount"`
		LastActiveAt  int64  `json:"lastActiveAt"`
	}
	result := make([]entry, len(tanks))
	for i, t := range tanks {
		result[i] = entry{
			Rank:          i + 1,
			TankID:        t.TankID,
			Name:          t.Name,
			UserID:        t.UserID,
			GlobalScore:   t.GlobalScore,
			BestFinish:    t.BestFinish,
			GameDaysCount: t.GameDaysCount,
			LastActiveAt:  t.LastActiveAt,
		}
	}
	return jsonResp(http.StatusOK, result), nil
}

func (h *handler) getGameDay(ctx context.Context, req events.APIGatewayV2HTTPRequest, gameDayID string) (events.APIGatewayV2HTTPResponse, error) {
	gd, err := h.store.GetGameDay(ctx, gameDayID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "game day not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, gd), nil
}

// ---- Map handlers -----------------------------------------------------------

func (h *handler) listMaps(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	maps, err := h.store.ListActiveMaps(ctx)
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, maps), nil
}

func (h *handler) createMap(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "admin access required"), nil
	}

	var body createMapBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return errResp(http.StatusBadRequest, "invalid request body"), nil
	}
	if body.Slug == "" || body.Name == "" || body.Layout == nil {
		return errResp(http.StatusBadRequest, "slug, name, and layout are required"), nil
	}
	if len(body.Layout) < 5 {
		return errResp(http.StatusBadRequest, "layout must be at least 5 rows"), nil
	}
	cols := len(body.Layout[0])
	if cols < 5 {
		return errResp(http.StatusBadRequest, "layout must be at least 5 columns"), nil
	}
	for _, row := range body.Layout {
		if len(row) != cols {
			return errResp(http.StatusBadRequest, "all layout rows must have equal length"), nil
		}
	}

	if _, err := h.store.GetMapBySlug(ctx, body.Slug); err == nil {
		return errResp(http.StatusConflict, "slug already in use"), nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	m := db.Map{
		MapID:       newUUID(),
		Slug:        body.Slug,
		Name:        body.Name,
		Description: body.Description,
		Layout:      body.Layout,
		IsBuiltIn:   false,
		IsActive:    true,
		CreatedAt:   time.Now().Unix(),
	}
	if err := h.store.PutMap(ctx, m); err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusCreated, m), nil
}

func (h *handler) updateMap(ctx context.Context, req events.APIGatewayV2HTTPRequest, mapID string) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "admin access required"), nil
	}

	existing, err := h.store.GetMapByID(ctx, mapID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "map not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	var body updateMapBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return errResp(http.StatusBadRequest, "invalid request body"), nil
	}

	name, description, isActive := existing.Name, existing.Description, existing.IsActive
	if body.Name != "" {
		name = body.Name
	}
	if body.Description != "" {
		description = body.Description
	}
	if body.IsActive != nil {
		isActive = *body.IsActive
	}

	if err := h.store.UpdateMap(ctx, mapID, name, description, isActive); err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]interface{}{
		"mapId":       mapID,
		"name":        name,
		"description": description,
		"isActive":    isActive,
	}), nil
}

// ---- Helpers ----------------------------------------------------------------

// triggerBuild updates the version to "compiling" and starts a CodeBuild run.
func (h *handler) triggerBuild(ctx context.Context, tankID, version, sourceKey, wasmKey string) {
	if err := h.store.UpdateVersionCompile(ctx, tankID, version, db.CompileUpdate{Status: "compiling"}); err != nil {
		log.Printf("set compiling %s/%s: %v", tankID, version, err)
	}
	overrides := []cbtypes.EnvironmentVariable{
		{Name: aws.String("TANK_ID"), Value: aws.String(tankID), Type: cbtypes.EnvironmentVariableTypePlaintext},
		{Name: aws.String("VERSION"), Value: aws.String(version), Type: cbtypes.EnvironmentVariableTypePlaintext},
		{Name: aws.String("SOURCE_S3_KEY"), Value: aws.String(sourceKey), Type: cbtypes.EnvironmentVariableTypePlaintext},
		{Name: aws.String("OUTPUT_WASM_KEY"), Value: aws.String(wasmKey), Type: cbtypes.EnvironmentVariableTypePlaintext},
		{Name: aws.String("TANK_VERSIONS_TABLE"), Value: aws.String(h.versionsTable), Type: cbtypes.EnvironmentVariableTypePlaintext},
	}
	if _, err := h.cb.StartBuild(ctx, &codebuild.StartBuildInput{
		ProjectName:                  aws.String(h.codebuildProject),
		EnvironmentVariablesOverride: overrides,
	}); err != nil {
		log.Printf("start codebuild %s/%s: %v", tankID, version, err)
		if updErr := h.store.UpdateVersionCompile(ctx, tankID, version, db.CompileUpdate{
			Status:       "failed",
			CompileError: "failed to start build",
		}); updErr != nil {
			log.Printf("revert compile status: %v", updErr)
		}
	}
}

// userID extracts the Cognito sub from the JWT authorizer claims.
// Returns "" on public routes where Authorizer is nil.
func userID(req events.APIGatewayV2HTTPRequest) string {
	if req.RequestContext.Authorizer == nil || req.RequestContext.Authorizer.JWT == nil {
		return ""
	}
	return req.RequestContext.Authorizer.JWT.Claims["sub"]
}

// isAdmin returns true if the caller belongs to the "platform-admin" Cognito group.
func isAdmin(req events.APIGatewayV2HTTPRequest) bool {
	if req.RequestContext.Authorizer == nil || req.RequestContext.Authorizer.JWT == nil {
		return false
	}
	return strings.Contains(req.RequestContext.Authorizer.JWT.Claims["cognito:groups"], "platform-admin")
}

// jsonResp encodes body as JSON and returns an API Gateway response.
func jsonResp(code int, body interface{}) events.APIGatewayV2HTTPResponse {
	data, _ := json.Marshal(body)
	return events.APIGatewayV2HTTPResponse{
		StatusCode: code,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(data),
	}
}

// errResp returns a JSON error response with the given HTTP status.
func errResp(code int, msg string) events.APIGatewayV2HTTPResponse {
	return jsonResp(code, map[string]string{"error": msg})
}

// newUUID generates a random UUID v4.
func newUUID() string {
	var b [16]byte
	_, _ = crand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// parseVersion parses a version string like "v1", "v0.3", "v2.1".
// Returns (major, minor, isMajorVersion, ok).
func parseVersion(v string) (major, minor int, isMajor, ok bool) {
	s := strings.TrimPrefix(v, "v")
	parts := strings.SplitN(s, ".", 2)
	maj, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false, false
	}
	if len(parts) == 1 {
		return maj, 0, true, true
	}
	min, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false, false
	}
	return maj, min, false, true
}

// nextMinorVersion returns the next minor version string for the tank, given its
// existing versions. If the latest major is v1 and the latest minor under v1 is
// v1.2, it returns "v1.3". If no versions exist, returns "v0.1".
func nextMinorVersion(versions []db.TankVersion) string {
	highestMajor := 0
	for _, v := range versions {
		if v.VersionType == "major" {
			if maj, _, _, ok := parseVersion(v.Version); ok && maj > highestMajor {
				highestMajor = maj
			}
		}
	}
	highestMinor := 0
	for _, v := range versions {
		if v.VersionType == "minor" {
			if maj, min, _, ok := parseVersion(v.Version); ok && maj == highestMajor && min > highestMinor {
				highestMinor = min
			}
		}
	}
	return fmt.Sprintf("v%d.%d", highestMajor, highestMinor+1)
}

// nextMajorVersion returns the next major version string given existing versions.
func nextMajorVersion(versions []db.TankVersion) string {
	highest := 0
	for _, v := range versions {
		if v.VersionType == "major" {
			if maj, _, _, ok := parseVersion(v.Version); ok && maj > highest {
				highest = maj
			}
		}
	}
	return fmt.Sprintf("v%d", highest+1)
}
