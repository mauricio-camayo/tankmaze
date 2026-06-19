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
//	GET    /gamedays                           – list all game days (no auth required)
//	POST   /gamedays                           – create game day + EventBridge schedules (admin only)
//	DELETE /gamedays/{id}                      – cancel game day (admin only, no phase started)
//	GET    /gamedays/{id}                      – Game Day bracket and phase status
//	PATCH  /gamedays/{id}                      – update phase schedule (admin only); ?force=true allows phase-status overrides on started/past game days
//	GET    /maps                               – list active maps (no auth required)
//	POST   /maps                               – create map (admin only)
//	PATCH  /maps/{id}                          – update map name/description/isActive (admin only)
//	GET    /admin/users                        – list all Cognito users (admin only)
//	PATCH  /admin/users/{sub}                  – enable/disable user (admin only)
//	PATCH  /admin/users/{sub}/role             – toggle platform-admin group (admin only, no self-demotion)
//	DELETE /admin/users/{sub}                  – delete user + all their tanks (admin only)
//	GET    /admin/tanks                        – list all tanks (admin only)
//	PATCH  /admin/tanks/{id}                   – rename any tank (admin only)
//	DELETE /admin/tanks/{id}                   – force-delete any tank (admin only)
package main

import (
	"bytes"
	crand "crypto/rand"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	cognitoidp "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	cognitotypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	lambdasvc "github.com/aws/aws-sdk-go-v2/service/lambda"
	ltypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	schedulersvc "github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"

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
	Source string           `json:"source"`
	Config db.VersionConfig `json:"config"`
}

type updateTankBody struct {
	Name      string  `json:"name"`
	AvatarURL *string `json:"avatarUrl,omitempty"`
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

type createGameDayBody struct {
	Name                string   `json:"name"`
	RegistrationCloseAt string   `json:"registrationCloseAt"`
	RoundRobinAt        string   `json:"roundRobinAt"`
	FinalAt             string   `json:"finalAt"`
	Autofill            bool     `json:"autofill"`
	ForcedMapIDs        []string `json:"forcedMapIds"`
	RandomMaps          bool     `json:"randomMaps"`
}

type patchGameDayBody struct {
	Name                string           `json:"name,omitempty"`
	RegistrationCloseAt string           `json:"registrationCloseAt,omitempty"`
	RoundRobinAt        string           `json:"roundRobinAt,omitempty"`
	FinalAt             string           `json:"finalAt,omitempty"`
	Autofill            *bool            `json:"autofill"`
	ForcedMapIDs        *[]string        `json:"forcedMapIds"`
	RandomMaps          *bool            `json:"randomMaps"`
	// PhaseOverride is only accepted when the request includes ?force=true.
	// Keys: "roundRobin", "final", or an elimination round key (e.g. "r1").
	// Value: "upcoming" | "running" | "complete" | "cancelled"
	PhaseOverride       map[string]string `json:"phaseOverride,omitempty"`
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

type adminUpdateUserBody struct {
	Disabled *bool `json:"disabled"`
}

type adminUpdateTankBody struct {
	Name string `json:"name"`
}

type adminUserResp struct {
	Sub     string `json:"sub"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	IsAdmin bool   `json:"isAdmin"`
}

// ---- Handler ----------------------------------------------------------------

type handler struct {
	store                  *db.Store
	s3                     *s3.Client
	cb                     *codebuild.Client
	lambdaSvc              *lambdasvc.Client
	cognito                *cognitoidp.Client
	schedulerSvc           *schedulersvc.Client
	wasmBucket             string
	logsBucket             string
	codebuildProject       string
	matchRunnerFunc        string
	versionsTable          string // forwarded to CodeBuild as env override
	scoutTankID            string
	scoutVersion           string
	bruiserTankID          string
	bruiserVersion         string
	rangerTankID           string
	rangerVersion          string
	randyTankID            string
	randyVersion           string
	userPoolID             string
	schedulerRoleArn       string
	schedulerDLQArn        string
	tournamentSchedulerArn string
}

var h *handler

func main() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}

	h = &handler{
		store:                  db.New(dynamodb.NewFromConfig(cfg)),
		s3:                     s3.NewFromConfig(cfg),
		cb:                     codebuild.NewFromConfig(cfg),
		lambdaSvc:              lambdasvc.NewFromConfig(cfg),
		cognito:                cognitoidp.NewFromConfig(cfg),
		schedulerSvc:           schedulersvc.NewFromConfig(cfg),
		wasmBucket:             os.Getenv("WASM_BUCKET"),
		logsBucket:             os.Getenv("MATCH_LOGS_BUCKET"),
		codebuildProject:       os.Getenv("CODEBUILD_PROJECT"),
		matchRunnerFunc:        os.Getenv("MATCH_RUNNER_FUNCTION"),
		versionsTable:          os.Getenv("TANK_VERSIONS_TABLE"),
		scoutTankID:            os.Getenv("SCOUT_TANK_ID"),
		scoutVersion:           os.Getenv("SCOUT_VERSION"),
		bruiserTankID:          os.Getenv("BRUISER_TANK_ID"),
		bruiserVersion:         os.Getenv("BRUISER_VERSION"),
		rangerTankID:           os.Getenv("RANGER_TANK_ID"),
		rangerVersion:          os.Getenv("RANGER_VERSION"),
		randyTankID:            os.Getenv("RANDY_TANK_ID"),
		randyVersion:           os.Getenv("RANDY_VERSION"),
		userPoolID:             os.Getenv("USER_POOL_ID"),
		schedulerRoleArn:       os.Getenv("SCHEDULER_INVOKE_ROLE_ARN"),
		schedulerDLQArn:        os.Getenv("SCHEDULER_DLQ_ARN"),
		tournamentSchedulerArn: os.Getenv("TOURNAMENT_SCHEDULER_FUNCTION"),
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
	case method == "GET" && rawPath == "tanks/ai":
		return h.getAiTanks(ctx)
	case method == "GET" && len(parts) == 2 && parts[0] == "tanks":
		return h.getTank(ctx, req, parts[1])
	case method == "DELETE" && len(parts) == 2 && parts[0] == "tanks":
		return h.deleteTank(ctx, req, parts[1])
	case method == "PATCH" && len(parts) == 2 && parts[0] == "tanks":
		return h.updateTank(ctx, req, parts[1])
	case method == "POST" && len(parts) == 3 && parts[0] == "tanks" && parts[2] == "versions":
		return h.submitVersion(ctx, req, parts[1])
	case method == "GET" && len(parts) == 5 && parts[0] == "tanks" && parts[2] == "versions" && parts[4] == "status":
		return h.getVersionStatus(ctx, req, parts[1], parts[3])
	case method == "GET" && len(parts) == 5 && parts[0] == "tanks" && parts[2] == "versions" && parts[4] == "source":
		return h.getVersionSource(ctx, req, parts[1], parts[3])
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
	case method == "GET" && rawPath == "gamedays":
		return h.listGameDays(ctx)
	case method == "POST" && rawPath == "gamedays":
		return h.createGameDay(ctx, req)
	case method == "DELETE" && len(parts) == 2 && parts[0] == "gamedays":
		return h.deleteGameDay(ctx, req, parts[1])
	case method == "GET" && len(parts) == 2 && parts[0] == "gamedays":
		return h.getGameDay(ctx, req, parts[1])
	case method == "PATCH" && len(parts) == 2 && parts[0] == "gamedays":
		return h.patchGameDay(ctx, req, parts[1])
	case method == "POST" && len(parts) == 3 && parts[0] == "gamedays" && parts[2] == "roster":
		return h.addRosterEntry(ctx, req, parts[1])
	case method == "DELETE" && len(parts) == 4 && parts[0] == "gamedays" && parts[2] == "roster":
		return h.removeRosterEntry(ctx, req, parts[1], parts[3])

	// Maps
	case method == "GET" && rawPath == "maps":
		return h.listMaps(ctx, req)
	case method == "POST" && rawPath == "maps":
		return h.createMap(ctx, req)
	case method == "PATCH" && len(parts) == 2 && parts[0] == "maps":
		return h.updateMap(ctx, req, parts[1])

	// Admin
	case method == "GET" && rawPath == "admin/users":
		return h.adminListUsers(ctx, req)
	case method == "PATCH" && len(parts) == 3 && parts[0] == "admin" && parts[1] == "users":
		return h.adminUpdateUser(ctx, req, parts[2])
	case method == "PATCH" && len(parts) == 4 && parts[0] == "admin" && parts[1] == "users" && parts[3] == "role":
		return h.adminToggleUserRole(ctx, req, parts[2])
	case method == "DELETE" && len(parts) == 3 && parts[0] == "admin" && parts[1] == "users":
		return h.adminDeleteUser(ctx, req, parts[2])
	case method == "GET" && rawPath == "admin/tanks":
		return h.adminListTanks(ctx, req)
	case method == "PATCH" && len(parts) == 3 && parts[0] == "admin" && parts[1] == "tanks":
		return h.adminUpdateTank(ctx, req, parts[2])
	case method == "DELETE" && len(parts) == 3 && parts[0] == "admin" && parts[1] == "tanks":
		return h.adminDeleteTank(ctx, req, parts[2])

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
	if target := req.QueryStringParameters["userId"]; target != "" && isAdmin(req) {
		uid = target
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

	forkFrom := req.QueryStringParameters["forkFrom"]
	forkVersion := req.QueryStringParameters["forkVersion"]

	var body createTankBody
	// Body is optional for forks — the name can be set/changed in the editor.
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil && forkFrom == "" {
		return errResp(http.StatusBadRequest, "invalid request body"), nil
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" && forkFrom == "" {
		return errResp(http.StatusBadRequest, "name is required"), nil
	}
	if len(body.Name) > maxTankNameLen {
		return errResp(http.StatusBadRequest, fmt.Sprintf("name must be %d characters or fewer", maxTankNameLen)), nil
	}

	// Validate fork source before creating the tank record.
	var srcVer *db.TankVersion
	var isAIFork bool
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
		// Detect AI-origin forks by checking tankId prefix or source tank userId.
		if strings.HasPrefix(forkFrom, "builtin-") {
			isAIFork = true
		} else if srcTank, err := h.store.GetTank(ctx, forkFrom); err == nil && srcTank.UserID == "__ai__" {
			isAIFork = true
		}
	}

	tankID := newUUID()
	now := time.Now().Unix()

	// Carry avatarUrl forward from the fork source.
	var forkAvatarURL string
	if forkFrom != "" {
		if srcTank, err := h.store.GetTank(ctx, forkFrom); err == nil {
			forkAvatarURL = srcTank.AvatarURL
		}
	}

	tank := db.Tank{
		TankID:            tankID,
		UserID:            uid,
		AuthorName:        authorName(req),
		Name:              body.Name,
		CreatedAt:         now,
		LastActiveAt:      now,
		ForkedFromTankID:  forkFrom,
		ForkedFromVersion: forkVersion,
		AvatarURL:         forkAvatarURL,
	}
	if err := h.store.PutTank(ctx, tank); err != nil {
		log.Printf("create tank: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	if srcVer != nil {
		newSrcKey := fmt.Sprintf("%s/v0.1/source.go", tankID)

		var srcWriteErr error
		if isAIFork {
			// AI source is a full package main file — read it, convert to
			// body-only dot-import style, then write the converted bytes.
			obj, err := h.s3.GetObject(ctx, &s3.GetObjectInput{
				Bucket: aws.String(h.wasmBucket),
				Key:    aws.String(srcVer.SourceS3Key),
			})
			if err != nil {
				log.Printf("fork read AI source: %v", err)
				srcWriteErr = err
			} else {
				raw, err := io.ReadAll(obj.Body)
				obj.Body.Close()
				if err != nil {
					log.Printf("fork read AI source body: %v", err)
					srcWriteErr = err
				} else {
					converted := []byte(convertAISource(string(raw)))
					_, srcWriteErr = h.s3.PutObject(ctx, &s3.PutObjectInput{
						Bucket:      aws.String(h.wasmBucket),
						Key:         aws.String(newSrcKey),
						Body:        bytes.NewReader(converted),
						ContentType: aws.String("text/plain; charset=utf-8"),
						Tagging:     aws.String("versionType=minor"),
					})
					if srcWriteErr != nil {
						log.Printf("fork write converted source: %v", srcWriteErr)
					}
				}
			}
		} else {
			_, srcWriteErr = h.s3.CopyObject(ctx, &s3.CopyObjectInput{
				Bucket:           aws.String(h.wasmBucket),
				CopySource:       aws.String(h.wasmBucket + "/" + srcVer.SourceS3Key),
				Key:              aws.String(newSrcKey),
				TaggingDirective: s3types.TaggingDirectiveReplace,
				Tagging:          aws.String("versionType=minor"),
			})
			if srcWriteErr != nil {
				log.Printf("fork copy source: %v", srcWriteErr)
			}
		}

		if srcWriteErr == nil {
			ver := db.TankVersion{
				TankID:        tankID,
				Version:       "v0.1",
				VersionType:   "minor",
				Config:        srcVer.Config,
				SourceS3Key:   newSrcKey,
				CompileStatus: "no_source",
				CreatedAt:     time.Now().Unix(),
			}
			if putErr := h.store.PutVersion(ctx, ver); putErr != nil {
				log.Printf("fork put version: %v", putErr)
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
	versions, err := h.store.ListVersionsByTank(ctx, tankID)
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	type getTankResponse struct {
		db.Tank
		Versions []db.TankVersion `json:"versions"`
	}

	if tank.UserID != uid && !isAdmin(req) {
		// Public view: only the latest major version, build artifacts and compile
		// metadata stripped.
		var latestMaj *db.TankVersion
		var latestMajNum int
		for i, v := range versions {
			if v.VersionType != "major" {
				continue
			}
			maj, _, _, ok := parseVersion(v.Version)
			if !ok {
				continue
			}
			if latestMaj == nil || maj > latestMajNum {
				cp := versions[i]
				cp.SourceS3Key = ""
				cp.WasmS3Key = ""
				cp.WasmSHA256 = ""
				cp.CompileStatus = ""
				cp.CompileError = ""
				latestMaj = &cp
				latestMajNum = maj
			}
		}
		pub := make([]db.TankVersion, 0, 1)
		if latestMaj != nil {
			pub = append(pub, *latestMaj)
		}
		return jsonResp(http.StatusOK, getTankResponse{Tank: tank, Versions: pub}), nil
	}

	// Lazy backfill: if the owner has no authorName stored, derive it from their
	// current JWT and persist it so it appears on the leaderboard going forward.
	if tank.AuthorName == "" {
		if name := authorName(req); name != "" {
			tank.AuthorName = name
			if err := h.store.UpdateAuthorName(ctx, tankID, name); err != nil {
				log.Printf("backfill authorName %s: %v", tankID, err)
			}
		}
	}

	return jsonResp(http.StatusOK, getTankResponse{Tank: tank, Versions: versions}), nil
}

func (h *handler) getAiTanks(ctx context.Context) (events.APIGatewayV2HTTPResponse, error) {
	type aiTankResponse struct {
		db.Tank
		Versions []db.TankVersion `json:"versions"`
	}
	pairs := [][2]string{
		{h.scoutTankID, h.scoutVersion},
		{h.bruiserTankID, h.bruiserVersion},
		{h.rangerTankID, h.rangerVersion},
		{h.randyTankID, h.randyVersion},
	}
	results := make([]aiTankResponse, 0, len(pairs))
	for _, p := range pairs {
		if p[0] == "" {
			continue
		}
		tank, err := h.store.GetTank(ctx, p[0])
		if err != nil {
			continue
		}
		versions, err := h.store.ListVersionsByTank(ctx, p[0])
		if err != nil || versions == nil {
			versions = []db.TankVersion{}
		}
		results = append(results, aiTankResponse{Tank: tank, Versions: versions})
	}
	return jsonResp(http.StatusOK, results), nil
}

func (h *handler) deleteTank(ctx context.Context, req events.APIGatewayV2HTTPRequest, tankID string) (events.APIGatewayV2HTTPResponse, error) {
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
	for _, v := range versions {
		if len(v.RegisteredForGameDays) > 0 {
			return errResp(http.StatusConflict, "tank is registered for a game day and cannot be deleted"), nil
		}
	}
	for _, v := range versions {
		if err := h.store.DeleteVersion(ctx, tankID, v.Version); err != nil {
			log.Printf("delete version %s/%s: %v", tankID, v.Version, err)
			return errResp(http.StatusInternalServerError, "internal error"), nil
		}
	}
	if err := h.store.DeleteTank(ctx, tankID); err != nil {
		log.Printf("delete tank %s: %v", tankID, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return events.APIGatewayV2HTTPResponse{StatusCode: http.StatusNoContent}, nil
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
		Config:        body.Config,
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

func (h *handler) getVersionSource(ctx context.Context, req events.APIGatewayV2HTTPRequest, tankID, version string) (events.APIGatewayV2HTTPResponse, error) {
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
	isAiTank := tankID == h.scoutTankID || tankID == h.bruiserTankID || tankID == h.rangerTankID || tankID == h.randyTankID
	if tank.UserID != uid && !isAiTank {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	ver, err := h.store.GetVersion(ctx, tankID, version)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "version not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if ver.SourceS3Key == "" {
		return errResp(http.StatusNotFound, "source not available"), nil
	}
	out, err := h.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(h.wasmBucket),
		Key:    aws.String(ver.SourceS3Key),
	})
	if err != nil {
		log.Printf("get source %s/%s: %v", tankID, version, err)
		return errResp(http.StatusInternalServerError, "failed to fetch source"), nil
	}
	defer out.Body.Close()
	srcBytes, err := io.ReadAll(out.Body)
	if err != nil {
		return errResp(http.StatusInternalServerError, "failed to read source"), nil
	}
	return jsonResp(http.StatusOK, map[string]string{"source": string(srcBytes)}), nil
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
	if rc, err := time.Parse(time.RFC3339, gd.Schedule.RegistrationClose); err != nil || !time.Now().Before(rc) {
		return errResp(http.StatusConflict, "game day registration is closed"), nil
	}
	if gd.Phases.RoundRobin.Status != "upcoming" && !isAdmin(req) {
		return errResp(http.StatusConflict, "game day registration is closed"), nil
	}
	for _, id := range ver.RegisteredForGameDays {
		if id == body.GameDayID {
			return errResp(http.StatusConflict, "already registered for this game day"), nil
		}
	}

	if err := h.store.AddVersionRegistration(ctx, tankID, version, body.GameDayID); err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if err := retryOnConflict(func() error {
		return h.store.AddRosterEntry(ctx, body.GameDayID, tankID, version, tank.Name)
	}); err != nil {
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
	var body struct {
		GameDayID string `json:"gameDayId"`
	}
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil || body.GameDayID == "" {
		return errResp(http.StatusBadRequest, "gameDayId is required"), nil
	}
	if err := h.store.RemoveVersionRegistration(ctx, tankID, version, body.GameDayID); err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if err := retryOnConflict(func() error {
		return h.store.RemoveRosterEntry(ctx, body.GameDayID, tankID)
	}); err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]bool{"deregistered": true}), nil
}

func (h *handler) updateTank(ctx context.Context, req events.APIGatewayV2HTTPRequest, tankID string) (events.APIGatewayV2HTTPResponse, error) {
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
	var body updateTankBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return errResp(http.StatusBadRequest, "invalid request body"), nil
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" && body.AvatarURL == nil {
		return errResp(http.StatusBadRequest, "name or avatarUrl is required"), nil
	}
	if body.Name != "" {
		if len(body.Name) > maxTankNameLen {
			return errResp(http.StatusBadRequest, fmt.Sprintf("name must be %d characters or fewer", maxTankNameLen)), nil
		}
		if err := h.store.UpdateTankName(ctx, tankID, body.Name); err != nil {
			log.Printf("update tank name %s: %v", tankID, err)
			return errResp(http.StatusInternalServerError, "internal error"), nil
		}
	}
	if body.AvatarURL != nil {
		if err := h.store.UpdateTankAvatarURL(ctx, tankID, *body.AvatarURL); err != nil {
			log.Printf("update tank avatar %s: %v", tankID, err)
			return errResp(http.StatusInternalServerError, "internal error"), nil
		}
	}
	return jsonResp(http.StatusOK, map[string]string{"name": tank.Name}), nil
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
		case "ranger":
			oppTankID, oppVersion = h.rangerTankID, h.rangerVersion
		case "randy":
			oppTankID, oppVersion = h.randyTankID, h.randyVersion
		default:
			return errResp(http.StatusBadRequest, "unknown AI opponent; use 'scout', 'bruiser', 'ranger', or 'randy'"), nil
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
		Rank           int    `json:"rank"`
		TankID         string `json:"tankId"`
		TankName       string `json:"tankName"`
		AuthorUsername string `json:"authorUsername"`
		GlobalScore    int    `json:"globalScore"`
		BestFinish     *int   `json:"bestFinish"`
		GameDays       int    `json:"gameDays"`
		LastActiveAt   int64  `json:"lastActiveAt"`
	}
	result := make([]entry, len(tanks))
	for i, t := range tanks {
		result[i] = entry{
			Rank:           i + 1,
			TankID:         t.TankID,
			TankName:       t.Name,
			AuthorUsername: authorNameOrID(t),
			GlobalScore:    t.GlobalScore,
			BestFinish:     t.BestFinish,
			GameDays:       t.GameDaysCount,
			LastActiveAt:   t.LastActiveAt,
		}
	}
	return jsonResp(http.StatusOK, result), nil
}

func (h *handler) listGameDays(ctx context.Context) (events.APIGatewayV2HTTPResponse, error) {
	gds, err := h.store.ListGameDays(ctx)
	if err != nil {
		log.Printf("list gamedays: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if gds == nil {
		gds = []db.GameDay{}
	}
	return jsonResp(http.StatusOK, gds), nil
}

// gameDayDisplayName appends a date suffix to baseName.
// Same-day events: "Name · Jan 2". Multi-day events: "Name · Jan 2 – Jan 3".
func gameDayDisplayName(baseName string, rrAt, finalAt time.Time) string {
	rrDate := rrAt.UTC().Format("Jan 2")
	finalDate := finalAt.UTC().Format("Jan 2")
	suffix := rrDate
	if finalDate != rrDate {
		suffix = rrDate + " – " + finalDate
	}
	if baseName == "" {
		return suffix
	}
	return baseName + " · " + suffix
}

// gameDayBaseName strips the date suffix added by gameDayDisplayName.
func gameDayBaseName(displayName string) string {
	if idx := strings.LastIndex(displayName, " · "); idx >= 0 {
		return displayName[:idx]
	}
	return displayName
}

func (h *handler) createGameDay(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "admin access required"), nil
	}

	var body createGameDayBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return errResp(http.StatusBadRequest, "invalid request body"), nil
	}

	parseAt := func(s, field string) (time.Time, bool) {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, false
		}
		return t.UTC(), true
	}
	regClose, ok := parseAt(body.RegistrationCloseAt, "registrationCloseAt")
	if !ok {
		return errResp(http.StatusBadRequest, "registrationCloseAt must be ISO 8601"), nil
	}
	rrAt, ok := parseAt(body.RoundRobinAt, "roundRobinAt")
	if !ok {
		return errResp(http.StatusBadRequest, "roundRobinAt must be ISO 8601"), nil
	}
	finalAt, ok := parseAt(body.FinalAt, "finalAt")
	if !ok {
		return errResp(http.StatusBadRequest, "finalAt must be ISO 8601"), nil
	}

	if !regClose.Before(rrAt) {
		return errResp(http.StatusBadRequest, "registration must close before round robin"), nil
	}
	if !rrAt.Before(finalAt) {
		return errResp(http.StatusBadRequest, "round robin must start before final"), nil
	}
	const maxElimRounds = 5
	elimTimes := make([]time.Time, maxElimRounds)
	for i := 0; i < maxElimRounds; i++ {
		t := finalAt.Add(-time.Duration(maxElimRounds-i) * 30 * time.Minute)
		if t.Before(rrAt) {
			t = rrAt
		}
		elimTimes[i] = t
	}
	elimination := make([]string, maxElimRounds)
	for i, t := range elimTimes {
		elimination[i] = t.UTC().Format(time.RFC3339)
	}

	gameDayID := newUUID()
	now := time.Now().Unix()
	gd := db.GameDay{
		GameDayID: gameDayID,
		Name:      gameDayDisplayName(strings.TrimSpace(body.Name), rrAt, finalAt),
		Schedule: db.GameDaySchedule{
			RegistrationClose: body.RegistrationCloseAt,
			RoundRobin:        body.RoundRobinAt,
			Elimination:       elimination,
			Final:             body.FinalAt,
		},
		Phases: db.GameDayPhases{
			RoundRobin: db.PhaseStatus{Status: "upcoming"},
			Final:      db.PhaseStatus{Status: "upcoming"},
		},
		CreatedAt:    now,
		Autofill:     body.Autofill,
		ForcedMapIDs: body.ForcedMapIDs,
		RandomMaps:   body.RandomMaps,
	}

	if err := h.store.PutGameDay(ctx, gd); err != nil {
		log.Printf("put gameday: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	atExpr := func(t time.Time) string {
		return "at(" + t.Format("2006-01-02T15:04:05") + ")"
	}
	phases := []struct {
		name  string
		phase string
		expr  string
	}{
		{gameDayID + "-reg-close", "registration_close", atExpr(regClose)},
		{gameDayID + "-rr", "round_robin", atExpr(rrAt)},
		{gameDayID + "-elim-r1", "elimination_r1", atExpr(elimTimes[0])},
		{gameDayID + "-elim-r2", "elimination_r2", atExpr(elimTimes[1])},
		{gameDayID + "-elim-r3", "elimination_r3", atExpr(elimTimes[2])},
		{gameDayID + "-elim-r4", "elimination_r4", atExpr(elimTimes[3])},
		{gameDayID + "-elim-r5", "elimination_r5", atExpr(elimTimes[4])},
		{gameDayID + "-final", "final", atExpr(finalAt)},
	}

	for _, p := range phases {
		payload, _ := json.Marshal(map[string]string{"gameDayId": gameDayID, "phase": p.phase})
		target := &schedulertypes.Target{
			Arn:     aws.String(h.tournamentSchedulerArn),
			RoleArn: aws.String(h.schedulerRoleArn),
			Input:   aws.String(string(payload)),
		}
		if h.schedulerDLQArn != "" {
			target.DeadLetterConfig = &schedulertypes.DeadLetterConfig{Arn: aws.String(h.schedulerDLQArn)}
		}
		_, err := h.schedulerSvc.CreateSchedule(ctx, &schedulersvc.CreateScheduleInput{
			Name:                        aws.String(p.name),
			GroupName:                   aws.String("tankmaze-gamedays"),
			ScheduleExpression:          aws.String(p.expr),
			ScheduleExpressionTimezone:  aws.String("UTC"),
			FlexibleTimeWindow: &schedulertypes.FlexibleTimeWindow{
				Mode: schedulertypes.FlexibleTimeWindowModeOff,
			},
			Target:                target,
			ActionAfterCompletion: schedulertypes.ActionAfterCompletionDelete,
		})
		if err != nil {
			log.Printf("create schedule %s: %v", p.name, err)
			// Best-effort cleanup: delete the game day record since schedules couldn't be created.
			if delErr := h.store.DeleteGameDay(ctx, gameDayID); delErr != nil {
				log.Printf("rollback delete gameday %s: %v", gameDayID, delErr)
			}
			return errResp(http.StatusInternalServerError, "failed to create schedules"), nil
		}
	}

	return jsonResp(http.StatusCreated, gd), nil
}

// upsertSchedule creates or updates an EventBridge Scheduler schedule so that
// the phase fires at the given time. It is best-effort: errors are logged but
// do not surface to the caller. Schedules in the past are skipped.
func (h *handler) upsertSchedule(ctx context.Context, name, phase, gameDayID string, at time.Time) {
	if h.schedulerSvc == nil || h.schedulerRoleArn == "" || h.tournamentSchedulerArn == "" {
		return
	}
	if !at.After(time.Now()) {
		log.Printf("upsertSchedule %s: time is in the past — skipping", name)
		return
	}
	expr := "at(" + at.UTC().Format("2006-01-02T15:04:05") + ")"
	payload, _ := json.Marshal(map[string]string{"gameDayId": gameDayID, "phase": phase})
	target := &schedulertypes.Target{
		Arn:     aws.String(h.tournamentSchedulerArn),
		RoleArn: aws.String(h.schedulerRoleArn),
		Input:   aws.String(string(payload)),
	}
	if h.schedulerDLQArn != "" {
		target.DeadLetterConfig = &schedulertypes.DeadLetterConfig{Arn: aws.String(h.schedulerDLQArn)}
	}
	ftw := &schedulertypes.FlexibleTimeWindow{Mode: schedulertypes.FlexibleTimeWindowModeOff}

	_, err := h.schedulerSvc.UpdateSchedule(ctx, &schedulersvc.UpdateScheduleInput{
		Name:                        aws.String(name),
		GroupName:                   aws.String("tankmaze-gamedays"),
		ScheduleExpression:          aws.String(expr),
		ScheduleExpressionTimezone:  aws.String("UTC"),
		FlexibleTimeWindow:          ftw,
		Target:                      target,
		ActionAfterCompletion:       schedulertypes.ActionAfterCompletionDelete,
	})
	if err == nil {
		return
	}
	var notFound *schedulertypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		log.Printf("update schedule %s: %v", name, err)
		return
	}
	// Schedule doesn't exist (already fired or was never created) — create it.
	if _, createErr := h.schedulerSvc.CreateSchedule(ctx, &schedulersvc.CreateScheduleInput{
		Name:                        aws.String(name),
		GroupName:                   aws.String("tankmaze-gamedays"),
		ScheduleExpression:          aws.String(expr),
		ScheduleExpressionTimezone:  aws.String("UTC"),
		FlexibleTimeWindow:          ftw,
		Target:                      target,
		ActionAfterCompletion:       schedulertypes.ActionAfterCompletionDelete,
	}); createErr != nil {
		log.Printf("create schedule %s (after update 404): %v", name, createErr)
	}
}

func (h *handler) deleteGameDay(ctx context.Context, req events.APIGatewayV2HTTPRequest, gameDayID string) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "admin access required"), nil
	}
	gd, err := h.store.GetGameDay(ctx, gameDayID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "game day not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	force := req.QueryStringParameters["force"] == "true"
	if !force && gd.Phases.RoundRobin.Status != "upcoming" {
		return errResp(http.StatusConflict, "game day has already started"), nil
	}

	scheduleNames := []string{
		gameDayID + "-reg-close",
		gameDayID + "-rr",
		gameDayID + "-elim-r1",
		gameDayID + "-elim-r2",
		gameDayID + "-elim-r3",
		gameDayID + "-elim-r4",
		gameDayID + "-elim-r5",
		gameDayID + "-final",
	}
	for _, name := range scheduleNames {
		if _, delErr := h.schedulerSvc.DeleteSchedule(ctx, &schedulersvc.DeleteScheduleInput{
			Name:      aws.String(name),
			GroupName: aws.String("tankmaze-gamedays"),
		}); delErr != nil {
			// 404 is fine — schedule may have already fired and self-deleted.
			log.Printf("delete schedule %s: %v (ignored)", name, delErr)
		}
	}

	if err := h.store.DeleteGameDay(ctx, gameDayID); err != nil {
		log.Printf("delete gameday %s: %v", gameDayID, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	var cleanupFailed []string
	for _, t := range gd.RegisteredTanks {
		if err := h.store.RemoveVersionRegistration(ctx, t.TankID, t.Version, gameDayID); err != nil {
			cleanupFailed = append(cleanupFailed, t.TankID+"@"+t.Version)
		}
	}
	if len(cleanupFailed) > 0 {
		log.Printf("delete gameday %s: failed to clean up registrations for %v (stale entries remain)", gameDayID, cleanupFailed)
	}
	return events.APIGatewayV2HTTPResponse{StatusCode: http.StatusNoContent}, nil
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

func (h *handler) patchGameDay(ctx context.Context, req events.APIGatewayV2HTTPRequest, gameDayID string) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "admin access required"), nil
	}

	force := req.QueryStringParameters["force"] == "true"

	existing, err := h.store.GetGameDay(ctx, gameDayID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "game day not found"), nil
	} else if err != nil {
		log.Printf("patch gameday %s: %v", gameDayID, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if !force {
		if finalAt, parseErr := time.Parse(time.RFC3339, existing.Schedule.Final); parseErr == nil && finalAt.Before(time.Now()) {
			return errResp(http.StatusConflict, "game day has already concluded"), nil
		}
	}

	var body patchGameDayBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return errResp(http.StatusBadRequest, "invalid request body"), nil
	}

	// ?force=true: apply phase status overrides and skip all schedule guards.
	if force && len(body.PhaseOverride) > 0 {
		validStatuses := map[string]bool{"upcoming": true, "running": true, "complete": true, "cancelled": true}
		for phase, status := range body.PhaseOverride {
			if !validStatuses[status] {
				return errResp(http.StatusBadRequest, "phaseOverride status must be one of: upcoming, running, complete, cancelled"), nil
			}
			ps := db.PhaseStatus{Status: status}
			if applyErr := h.store.UpdateGameDayPhase(ctx, gameDayID, phase, ps); applyErr != nil {
				log.Printf("patch gameday %s phase %s: %v", gameDayID, phase, applyErr)
				return errResp(http.StatusInternalServerError, "internal error"), nil
			}
		}
		// Re-fetch and return after phase overrides (no schedule changes in force mode).
		gd, err := h.store.GetGameDay(ctx, gameDayID)
		if err != nil {
			return errResp(http.StatusInternalServerError, "internal error"), nil
		}
		return jsonResp(http.StatusOK, gd), nil
	}

	parseAt := func(s string) (time.Time, bool) {
		if s == "" {
			return time.Time{}, true
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, false
		}
		return t.UTC(), true
	}

	for _, f := range []string{body.RegistrationCloseAt, body.RoundRobinAt, body.FinalAt} {
		if _, ok := parseAt(f); !ok {
			return errResp(http.StatusBadRequest, "timestamps must be ISO 8601"), nil
		}
	}

	// Validate ordering on the merged schedule.
	mergedRegClose := existing.Schedule.RegistrationClose
	mergedRRAt := existing.Schedule.RoundRobin
	mergedFinalAt := existing.Schedule.Final
	if body.RegistrationCloseAt != "" {
		mergedRegClose = body.RegistrationCloseAt
	}
	if body.RoundRobinAt != "" {
		mergedRRAt = body.RoundRobinAt
	}
	if body.FinalAt != "" {
		mergedFinalAt = body.FinalAt
	}
	if rc, ok1 := parseAt(mergedRegClose); ok1 {
		if rr, ok2 := parseAt(mergedRRAt); ok2 && !rc.Before(rr) {
			return errResp(http.StatusBadRequest, "registration must close before round robin"), nil
		}
	}
	if rr, ok1 := parseAt(mergedRRAt); ok1 {
		if fn, ok2 := parseAt(mergedFinalAt); ok2 && !rr.Before(fn) {
			return errResp(http.StatusBadRequest, "round robin must start before final"), nil
		}
	}

	// Determine base name: admin-supplied overrides existing; otherwise strip date suffix.
	patchBaseName := strings.TrimSpace(body.Name)
	if patchBaseName == "" {
		patchBaseName = gameDayBaseName(existing.Name)
	}
	// Recompute full display name from merged schedule.
	mergedRR, _ := parseAt(mergedRRAt)
	mergedFinal, _ := parseAt(mergedFinalAt)
	patchName := gameDayDisplayName(patchBaseName, mergedRR, mergedFinal)

	// When FinalAt changes, recompute the derived elimination round times so
	// the DB schedule.elimination array stays consistent with schedule.final.
	var patchElimTimes [5]time.Time
	if body.FinalAt != "" {
		fn, _ := parseAt(body.FinalAt)
		rr, _ := parseAt(mergedRRAt)
		for i := 0; i < 5; i++ {
			t := fn.Add(-time.Duration(5-i) * 30 * time.Minute)
			if t.Before(rr) {
				t = rr
			}
			patchElimTimes[i] = t
		}
	}

	upd := db.GameDayUpdate{
		Name:                patchName,
		RegistrationCloseAt: body.RegistrationCloseAt,
		RoundRobinAt:        body.RoundRobinAt,
		FinalAt:             body.FinalAt,
		Autofill:            body.Autofill,
		ForcedMapIDs:        body.ForcedMapIDs,
		RandomMaps:          body.RandomMaps,
	}
	if body.FinalAt != "" {
		elim := make([]string, 5)
		for i, t := range patchElimTimes {
			elim[i] = t.UTC().Format(time.RFC3339)
		}
		upd.EliminationAt = elim
	}

	if err := h.store.UpdateGameDay(ctx, gameDayID, upd); errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "game day not found"), nil
	} else if errors.Is(err, db.ErrGameDayStarted) {
		return errResp(http.StatusConflict, "game day has already started"), nil
	} else if err != nil {
		log.Printf("patch gameday %s: %v", gameDayID, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	// Sync EventBridge schedules for any rescheduled phases. Best-effort:
	// errors are logged inside upsertSchedule and don't fail the response.
	if body.RegistrationCloseAt != "" {
		if rc, ok := parseAt(body.RegistrationCloseAt); ok {
			h.upsertSchedule(ctx, gameDayID+"-reg-close", "registration_close", gameDayID, rc)
		}
	}
	if body.RoundRobinAt != "" {
		if rr, ok := parseAt(body.RoundRobinAt); ok {
			h.upsertSchedule(ctx, gameDayID+"-rr", "round_robin", gameDayID, rr)
		}
	}
	if body.FinalAt != "" {
		fn, _ := parseAt(body.FinalAt)
		h.upsertSchedule(ctx, gameDayID+"-final", "final", gameDayID, fn)
		for i, t := range patchElimTimes {
			h.upsertSchedule(ctx, fmt.Sprintf("%s-elim-r%d", gameDayID, i+1), fmt.Sprintf("elimination_r%d", i+1), gameDayID, t)
		}
	}

	gd, err := h.store.GetGameDay(ctx, gameDayID)
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, gd), nil
}

func (h *handler) addRosterEntry(ctx context.Context, req events.APIGatewayV2HTTPRequest, gameDayID string) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "admin access required"), nil
	}
	gd, err := h.store.GetGameDay(ctx, gameDayID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "game day not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if gd.Phases.RoundRobin.Status != "upcoming" {
		return errResp(http.StatusConflict, "game day has already started"), nil
	}
	var body struct {
		TankID  string `json:"tankId"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil || body.TankID == "" || body.Version == "" {
		return errResp(http.StatusBadRequest, "tankId and version are required"), nil
	}
	if _, _, isMajor, ok := parseVersion(body.Version); !ok || !isMajor {
		return errResp(http.StatusUnprocessableEntity, "version must be a major version (e.g. v1)"), nil
	}
	// AI tanks (builtin-*) may be added more than once so the bracket can be
	// padded with multiple instances of the same bot.
	if !strings.HasPrefix(body.TankID, "builtin-") {
		for _, t := range gd.RegisteredTanks {
			if t.TankID == body.TankID {
				return errResp(http.StatusConflict, "tank is already registered for this game day"), nil
			}
		}
	}
	tankName := ""
	if t, err := h.store.GetTank(ctx, body.TankID); err == nil {
		tankName = t.Name
	}
	if err := retryOnConflict(func() error {
		return h.store.AddRosterEntry(ctx, gameDayID, body.TankID, body.Version, tankName)
	}); err != nil {
		log.Printf("add roster entry gameday=%s tank=%s: %v", gameDayID, body.TankID, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	// Mirror the registration on the TankVersion record so TankDetail shows the
	// Withdraw button. Skip for AI/built-in tanks — they have no version record.
	if !strings.HasPrefix(body.TankID, "builtin-") {
		if err := h.store.AddVersionRegistration(ctx, body.TankID, body.Version, gameDayID); err != nil {
			log.Printf("add version registration gameday=%s tank=%s version=%s: %v", gameDayID, body.TankID, body.Version, err)
		}
	}
	return events.APIGatewayV2HTTPResponse{StatusCode: http.StatusNoContent}, nil
}

func (h *handler) removeRosterEntry(ctx context.Context, req events.APIGatewayV2HTTPRequest, gameDayID, tankID string) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "admin access required"), nil
	}
	gd, err := h.store.GetGameDay(ctx, gameDayID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "game day not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if gd.Phases.RoundRobin.Status != "upcoming" {
		return errResp(http.StatusConflict, "game day has already started"), nil
	}
	// Capture the version before removing so we can update the TankVersion record.
	var removedVersion string
	for _, t := range gd.RegisteredTanks {
		if t.TankID == tankID {
			removedVersion = t.Version
			break
		}
	}
	if err := retryOnConflict(func() error {
		return h.store.RemoveRosterEntry(ctx, gameDayID, tankID)
	}); err != nil {
		log.Printf("remove roster entry gameday=%s tank=%s: %v", gameDayID, tankID, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	// Mirror the deregistration on the TankVersion record. Skip for AI/built-in tanks.
	if removedVersion != "" && !strings.HasPrefix(tankID, "builtin-") {
		if err := h.store.RemoveVersionRegistration(ctx, tankID, removedVersion, gameDayID); err != nil {
			log.Printf("remove version registration gameday=%s tank=%s version=%s: %v", gameDayID, tankID, removedVersion, err)
		}
	}
	return events.APIGatewayV2HTTPResponse{StatusCode: http.StatusNoContent}, nil
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

func authorName(req events.APIGatewayV2HTTPRequest) string {
	if req.RequestContext.Authorizer == nil || req.RequestContext.Authorizer.JWT == nil {
		return ""
	}
	claims := req.RequestContext.Authorizer.JWT.Claims
	for _, key := range []string{"name", "given_name", "email"} {
		if v := claims[key]; v != "" {
			if key == "email" {
				if at := strings.Index(v, "@"); at > 0 {
					return v[:at]
				}
			}
			return v
		}
	}
	log.Printf("authorName: no usable name claim found; available claims: %v", claims)
	return ""
}

func authorNameOrID(t db.Tank) string {
	if t.AuthorName != "" {
		return t.AuthorName
	}
	return t.UserID
}

// ---- Admin handlers ---------------------------------------------------------

func cognitoAttr(attrs []cognitotypes.AttributeType, name string) string {
	for _, a := range attrs {
		if aws.ToString(a.Name) == name {
			return aws.ToString(a.Value)
		}
	}
	return ""
}

func (h *handler) getUsernameBySub(ctx context.Context, sub string) (string, error) {
	out, err := h.cognito.ListUsers(ctx, &cognitoidp.ListUsersInput{
		UserPoolId: aws.String(h.userPoolID),
		Filter:     aws.String(fmt.Sprintf(`sub = "%s"`, sub)),
		Limit:      aws.Int32(1),
	})
	if err != nil {
		return "", fmt.Errorf("list users: %w", err)
	}
	if len(out.Users) == 0 {
		return "", fmt.Errorf("user not found")
	}
	return aws.ToString(out.Users[0].Username), nil
}

func (h *handler) adminListUsers(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}

	listInput := &cognitoidp.ListUsersInput{
		UserPoolId: aws.String(h.userPoolID),
		Limit:      aws.Int32(50),
	}
	if tok := req.QueryStringParameters["nextToken"]; tok != "" {
		listInput.PaginationToken = aws.String(tok)
	}
	usersOut, err := h.cognito.ListUsers(ctx, listInput)
	if err != nil {
		log.Printf("admin list users: %v", err)
		return errResp(http.StatusInternalServerError, "failed to list users"), nil
	}

	adminOut, _ := h.cognito.ListUsersInGroup(ctx, &cognitoidp.ListUsersInGroupInput{
		UserPoolId: aws.String(h.userPoolID),
		GroupName:  aws.String("platform-admin"),
	})
	adminSubs := map[string]bool{}
	if adminOut != nil {
		for _, u := range adminOut.Users {
			if sub := cognitoAttr(u.Attributes, "sub"); sub != "" {
				adminSubs[sub] = true
			}
		}
	}

	users := make([]adminUserResp, 0, len(usersOut.Users))
	for _, u := range usersOut.Users {
		sub := cognitoAttr(u.Attributes, "sub")
		users = append(users, adminUserResp{
			Sub:     sub,
			Email:   cognitoAttr(u.Attributes, "email"),
			Name:    cognitoAttr(u.Attributes, "name"),
			Enabled: u.Enabled,
			IsAdmin: adminSubs[sub],
		})
	}

	resp := map[string]interface{}{"users": users}
	if usersOut.PaginationToken != nil {
		resp["nextToken"] = *usersOut.PaginationToken
	}
	return jsonResp(http.StatusOK, resp), nil
}

func (h *handler) adminUpdateUser(ctx context.Context, req events.APIGatewayV2HTTPRequest, sub string) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	var body adminUpdateUserBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil || body.Disabled == nil {
		return errResp(http.StatusBadRequest, "disabled field required"), nil
	}
	username, err := h.getUsernameBySub(ctx, sub)
	if err != nil {
		return errResp(http.StatusNotFound, "user not found"), nil
	}
	if *body.Disabled {
		_, err = h.cognito.AdminDisableUser(ctx, &cognitoidp.AdminDisableUserInput{
			UserPoolId: aws.String(h.userPoolID),
			Username:   aws.String(username),
		})
	} else {
		_, err = h.cognito.AdminEnableUser(ctx, &cognitoidp.AdminEnableUserInput{
			UserPoolId: aws.String(h.userPoolID),
			Username:   aws.String(username),
		})
	}
	if err != nil {
		log.Printf("admin update user %s: %v", sub, err)
		return errResp(http.StatusInternalServerError, "failed to update user"), nil
	}
	return jsonResp(http.StatusOK, map[string]string{"status": "ok"}), nil
}

func (h *handler) adminToggleUserRole(ctx context.Context, req events.APIGatewayV2HTTPRequest, sub string) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	if sub == userID(req) {
		return errResp(http.StatusBadRequest, "cannot modify your own admin role"), nil
	}
	username, err := h.getUsernameBySub(ctx, sub)
	if err != nil {
		return errResp(http.StatusNotFound, "user not found"), nil
	}
	groupsOut, err := h.cognito.AdminListGroupsForUser(ctx, &cognitoidp.AdminListGroupsForUserInput{
		UserPoolId: aws.String(h.userPoolID),
		Username:   aws.String(username),
	})
	if err != nil {
		log.Printf("list groups for user %s: %v", sub, err)
		return errResp(http.StatusInternalServerError, "failed to get user groups"), nil
	}
	isCurrentlyAdmin := false
	for _, g := range groupsOut.Groups {
		if aws.ToString(g.GroupName) == "platform-admin" {
			isCurrentlyAdmin = true
			break
		}
	}
	if isCurrentlyAdmin {
		_, err = h.cognito.AdminRemoveUserFromGroup(ctx, &cognitoidp.AdminRemoveUserFromGroupInput{
			UserPoolId: aws.String(h.userPoolID),
			Username:   aws.String(username),
			GroupName:  aws.String("platform-admin"),
		})
	} else {
		_, err = h.cognito.AdminAddUserToGroup(ctx, &cognitoidp.AdminAddUserToGroupInput{
			UserPoolId: aws.String(h.userPoolID),
			Username:   aws.String(username),
			GroupName:  aws.String("platform-admin"),
		})
	}
	if err != nil {
		log.Printf("toggle admin role for %s: %v", sub, err)
		return errResp(http.StatusInternalServerError, "failed to toggle role"), nil
	}
	return jsonResp(http.StatusOK, map[string]bool{"isAdmin": !isCurrentlyAdmin}), nil
}

func (h *handler) adminDeleteUser(ctx context.Context, req events.APIGatewayV2HTTPRequest, sub string) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	if sub == userID(req) {
		return errResp(http.StatusBadRequest, "cannot delete yourself"), nil
	}
	username, err := h.getUsernameBySub(ctx, sub)
	if err != nil {
		return errResp(http.StatusNotFound, "user not found"), nil
	}
	tanks, err := h.store.ScanTanksByScore(ctx)
	if err != nil {
		log.Printf("scan tanks for deleted user %s: %v", sub, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	for _, t := range tanks {
		if t.UserID != sub {
			continue
		}
		versions, _ := h.store.ListVersionsByTank(ctx, t.TankID)
		for _, v := range versions {
			if err := h.store.DeleteVersion(ctx, t.TankID, v.Version); err != nil {
				log.Printf("delete version %s/%s: %v", t.TankID, v.Version, err)
			}
		}
		if err := h.store.DeleteTank(ctx, t.TankID); err != nil {
			log.Printf("delete tank %s: %v", t.TankID, err)
		}
	}
	if _, err := h.cognito.AdminDeleteUser(ctx, &cognitoidp.AdminDeleteUserInput{
		UserPoolId: aws.String(h.userPoolID),
		Username:   aws.String(username),
	}); err != nil {
		log.Printf("cognito delete user %s: %v", sub, err)
		return errResp(http.StatusInternalServerError, "failed to delete user"), nil
	}
	return jsonResp(http.StatusOK, map[string]string{"status": "deleted"}), nil
}

func (h *handler) adminListTanks(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	cursor := req.QueryStringParameters["nextToken"]
	tanks, nextCursor, err := h.store.ScanTanksPage(ctx, 50, cursor)
	if err != nil {
		log.Printf("admin list tanks: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	resp := map[string]interface{}{"tanks": tanks}
	if nextCursor != "" {
		resp["nextToken"] = nextCursor
	}
	return jsonResp(http.StatusOK, resp), nil
}

func (h *handler) adminUpdateTank(ctx context.Context, req events.APIGatewayV2HTTPRequest, tankID string) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	var body adminUpdateTankBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil || body.Name == "" {
		return errResp(http.StatusBadRequest, "name is required"), nil
	}
	if err := h.store.UpdateTankName(ctx, tankID, body.Name); err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]string{"status": "ok"}), nil
}

func (h *handler) adminDeleteTank(ctx context.Context, req events.APIGatewayV2HTTPRequest, tankID string) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	if _, err := h.store.GetTank(ctx, tankID); errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "tank not found"), nil
	}
	versions, err := h.store.ListVersionsByTank(ctx, tankID)
	if err == nil {
		for _, v := range versions {
			if err := h.store.DeleteVersion(ctx, tankID, v.Version); err != nil {
				log.Printf("admin delete version %s/%s: %v", tankID, v.Version, err)
			}
		}
	}
	if err := h.store.DeleteTank(ctx, tankID); err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]string{"status": "deleted"}), nil
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

// convertAISource transforms a full package main AI source file into the
// package tank dot-import style expected by compileWasm. It replaces
// "package main" with "package tank", replaces the named import block with
// a single dot-import of the SDK, strips //go:wasmimport / //go:noescape
// directives, func main, func encode, and the WASM host-import stubs, then
// renames func tick → func Tick.
func convertAISource(src string) string {
	lines := strings.Split(src, "\n")
	var out []string
	skipDepth := 0
	inImport := false
	skipVarLine := false

	funcsToRemove := []string{
		"func main()",
		"func encode(",
		"func sensorsGet(",
		"func configRegister(",
		"func actionPut(",
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Replace package main with package tank.
		if strings.HasPrefix(trimmed, "package ") {
			if trimmed == "package main" {
				out = append(out, "package tank")
			}
			continue
		}

		// Strip import block and emit dot-import instead.
		if trimmed == "import (" {
			inImport = true
			continue
		}
		if inImport {
			if trimmed == ")" {
				inImport = false
				out = append(out, `import . "github.com/tankmaze/sdk"`)
			}
			continue
		}

		if strings.HasPrefix(trimmed, "//go:") {
			continue
		}

		if skipDepth > 0 {
			for _, ch := range line {
				if ch == '{' {
					skipDepth++
				} else if ch == '}' {
					skipDepth--
				}
			}
			continue
		}

		if strings.HasPrefix(trimmed, "var Config ") || strings.HasPrefix(trimmed, "var cfgJSON ") {
			skipVarLine = true
		}
		if skipVarLine {
			for _, ch := range line {
				if ch == '{' {
					skipDepth++
				} else if ch == '}' {
					skipDepth--
				}
			}
			if skipDepth == 0 {
				skipVarLine = false
			}
			continue
		}

		isRemovedFunc := false
		for _, sig := range funcsToRemove {
			if strings.HasPrefix(trimmed, sig) {
				isRemovedFunc = true
				break
			}
		}
		if isRemovedFunc {
			for _, ch := range line {
				if ch == '{' {
					skipDepth++
				} else if ch == '}' {
					skipDepth--
				}
			}
			continue
		}

		line = strings.Replace(line, "func tick(", "func Tick(", 1)
		line = strings.ReplaceAll(line, "tankmaze.", "")

		// Strip any immediately-preceding comment line when opening a
		// surviving var block, so stripPreamble's \n\nvar  token lands
		// directly before "var (" instead of before the comment.
		if strings.TrimSpace(line) == "var (" {
			for len(out) > 0 {
				prev := strings.TrimSpace(out[len(out)-1])
				if strings.HasPrefix(prev, "//") {
					out = out[:len(out)-1]
				} else {
					break
				}
			}
		}

		out = append(out, line)
	}

	result := make([]string, 0, len(out))
	blanks := 0
	for _, l := range out {
		if strings.TrimSpace(l) == "" {
			blanks++
			if blanks <= 2 {
				result = append(result, l)
			}
		} else {
			blanks = 0
			result = append(result, l)
		}
	}
	for len(result) > 0 && strings.TrimSpace(result[0]) == "" {
		result = result[1:]
	}
	return strings.Join(result, "\n")
}

// retryOnConflict calls fn up to maxConflictRetries+1 times, retrying whenever
// db.ErrConflict is returned (optimistic-locking failure on GameDay writes).
func retryOnConflict(fn func() error) error {
	const maxConflictRetries = 5
	for attempt := 0; attempt <= maxConflictRetries; attempt++ {
		err := fn()
		if errors.Is(err, db.ErrConflict) {
			log.Printf("retryOnConflict: attempt %d — retrying", attempt+1)
			continue
		}
		return err
	}
	return fmt.Errorf("too many optimistic lock conflicts")
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
