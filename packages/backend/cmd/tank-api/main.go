// Package main implements the TankMaze HTTP REST API Lambda.
//
// Routes:
//
//	POST   /tanks                              – create tank (optional ?forkFrom=&forkVersion= for fork)
//	GET    /tanks                              – list caller's tanks
//	GET    /tanks/{id}                         – tank detail + version history
//	PUT    /tanks/{id}/avatar                  – upload a custom avatar image (owner only, PNG/JPEG, max 512KB)
//	GET    /me/profile                          – caller's durable display name (item 225; survives federated IdP re-login, unlike the ID token's given_name claim)
//	PUT    /me/profile/picture                 – upload a custom profile picture (PNG/JPEG, max 512KB)
//	POST   /tanks/{id}/versions               – submit Go source → triggers CodeBuild
//	GET    /tanks/{id}/versions/{v}/status    – poll compile status
//	POST   /tanks/{id}/versions/{v}/promote   – promote minor → next major
//	POST   /tanks/{id}/versions/{v}/register  – register major for a Game Day
//	DELETE /tanks/{id}/versions/{v}/register  – withdraw Game Day registration
//	POST   /tanks/{id}/score-transfer         – transfer Global Score to another tank
//	POST   /matches                            – start a match; opponent.type is "ai", "own", "informal" (item 37, challenge another author's tank), or "rematch" (item 37, re-run a previous ranked match by opponent.matchId) — all unranked
//	GET    /matches/{id}                       – match metadata + result
//	GET    /matches/{id}/ticks                 – redirect to pre-signed S3 tick log URL
//	GET    /matches/{id}/export                – owner-only; decompresses tick log on demand, returns { url } to a short-lived presigned JSON download (item 35)
//	GET    /rankings                           – global leaderboard
//	GET    /users/{sub}                         – public author profile (name, picture, public tank list; no email; no auth required)
//	GET    /gamedays                           – list all game days (no auth required)
//	POST   /gamedays                           – create game day + EventBridge schedules (admin only)
//	DELETE /gamedays/{id}                      – cancel game day (admin only, no phase started)
//	GET    /gamedays/{id}                      – Game Day bracket and phase status
//	PATCH  /gamedays/{id}                      – update phase schedule (admin only); ?force=true allows phase-status overrides on started/past game days
//	GET    /gameday-series                     – list recurring Game Day series (admin only, item 238)
//	POST   /gameday-series                     – create a recurring series; materializes its first occurrence immediately (admin only)
//	DELETE /gameday-series/{id}                – cancel a series (stops future materialization only; existing occurrences are untouched, admin only)
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
//	POST   /admin/tanks/{id}/versions/{v}/reset-compile – force-reset stuck "compiling" status (admin only)
//	POST   /auth/forgot-password               – enumeration-safe forgot-password trigger (no auth required, always 202)
//	GET    /friends                             – list caller's friends + incoming/outgoing requests
//	POST   /friends/requests                    – send a friend request ({toUserId})
//	POST   /friends/requests/{fromUserId}/accept – accept an incoming friend request
//	POST   /friends/requests/{fromUserId}/reject – reject an incoming friend request (or cancel an outgoing one, from the other side)
//	DELETE /friends/{friendId}                  – remove a friend, or cancel your own outgoing request
//	POST   /friends/block                       – block a user ({targetUserId}); removes any friendship, blocks future requests (item 226)
//	POST   /friends/unblock                     – unblock a user ({targetUserId}); only the user who placed the block may call this
//	POST   /messages                            – send a chat message ({toUserId, body}); accepted-friends only (item 223 Part 2)
//	GET    /messages/{userId}                   – conversation history with that user; ?since=<messageId> for polling new messages only
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	crand "crypto/rand"
	"encoding/base64"
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
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	cognitoidp "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	cognitotypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	lambdasvc "github.com/aws/aws-sdk-go-v2/service/lambda"
	ltypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	schedulersvc "github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"

	"github.com/tankmaze/backend/internal/db"
	"github.com/tankmaze/backend/internal/scheduling"
)

const (
	maxSourceBytes    = 1 * 1024 * 1024  // 1 MiB
	maxWASMBytes      = 10 * 1024 * 1024 // 10 MiB (SEC-WASM-SIZE)
	maxTankNameLen    = 64
	testMatchTTLDays  = 7
	tickLogPresignTTL = 15 * time.Minute
	// minScheduleLeadTime is the minimum gap a Game Day phase's scheduled
	// time must have over "now" at creation/edit-validation time. Below this,
	// by the time the request finishes processing the target time could
	// already be at or past "now" — upsertSchedule's own past-time guard
	// would then silently skip rescheduling EventBridge (item 254). Catching
	// it here, before any write happens, turns that into an immediate,
	// actionable 400 instead of a plausible-looking success that silently
	// doesn't take effect.
	minScheduleLeadTime = 60 * time.Second
)

// ---- Request / response body types ------------------------------------------

type createTankBody struct {
	Name string `json:"name"`
}

type patchMySettingsBody struct {
	Tier string `json:"tier"`
}

type patchMyProfileBody struct {
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
	Type    string `json:"type"`              // "ai" | "own" | "informal" | "rematch"
	Name    string `json:"name,omitempty"`    // ai: "scout" | "bruiser" | "ranger" | "randy"
	TankID  string `json:"tankId,omitempty"`  // own/informal: opponent tank
	Version string `json:"version,omitempty"` // own/informal: opponent version
	MatchID string `json:"matchId,omitempty"` // rematch: the original ranked match to re-run
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
	Name                string    `json:"name,omitempty"`
	RegistrationCloseAt string    `json:"registrationCloseAt,omitempty"`
	RoundRobinAt        string    `json:"roundRobinAt,omitempty"`
	FinalAt             string    `json:"finalAt,omitempty"`
	Autofill            *bool     `json:"autofill"`
	ForcedMapIDs        *[]string `json:"forcedMapIds"`
	RandomMaps          *bool     `json:"randomMaps"`
	// PhaseOverride is only accepted when the request includes ?force=true.
	// Keys: "roundRobin", "final", or an elimination round key (e.g. "r1").
	// Value: "upcoming" | "running" | "complete" | "cancelled"
	PhaseOverride map[string]string `json:"phaseOverride,omitempty"`
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
	Sub         string `json:"sub"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	IsAdmin     bool   `json:"isAdmin"`
	Tier        string `json:"tier"`
	Idp         string `json:"idp"`
	CreatedAt   string `json:"createdAt"`
	LastLoginAt *int64 `json:"lastLoginAt"`
	TankCount   int    `json:"tankCount"`
	TankLimit   int    `json:"tankLimit"`
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
	assetsBucket           string
	region                 string
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
	forgotPasswordWorkerFn string
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
		assetsBucket:           os.Getenv("TANK_ASSETS_BUCKET"),
		region:                 cfg.Region,
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
		forgotPasswordWorkerFn: os.Getenv("FORGOT_PASSWORD_WORKER_FUNCTION"),
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
	case method == "PUT" && len(parts) == 3 && parts[0] == "tanks" && parts[2] == "avatar":
		return h.uploadTankAvatar(ctx, req, parts[1])
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
	case method == "GET" && len(parts) == 3 && parts[0] == "matches" && parts[2] == "export":
		return h.getMatchExport(ctx, req, parts[1])

	// Auth
	case method == "POST" && rawPath == "auth/forgot-password":
		return h.forgotPassword(ctx, req)

	// Friends
	case method == "GET" && rawPath == "friends":
		return h.listFriends(ctx, req)
	case method == "POST" && rawPath == "friends/requests":
		return h.sendFriendRequest(ctx, req)
	case method == "POST" && len(parts) == 4 && parts[0] == "friends" && parts[1] == "requests" && parts[3] == "accept":
		return h.respondFriendRequest(ctx, req, parts[2], true)
	case method == "POST" && len(parts) == 4 && parts[0] == "friends" && parts[1] == "requests" && parts[3] == "reject":
		return h.respondFriendRequest(ctx, req, parts[2], false)
	case method == "DELETE" && len(parts) == 2 && parts[0] == "friends":
		return h.removeFriend(ctx, req, parts[1])
	case method == "POST" && rawPath == "friends/block":
		return h.blockUser(ctx, req)
	case method == "POST" && rawPath == "friends/unblock":
		return h.unblockUser(ctx, req)
	case method == "POST" && rawPath == "messages":
		return h.sendMessage(ctx, req)
	case method == "GET" && len(parts) == 2 && parts[0] == "messages":
		return h.listMessages(ctx, req, parts[1])

	// Rankings and Game Days
	case method == "GET" && rawPath == "rankings":
		return h.getRankings(ctx, req)
	case method == "GET" && len(parts) == 2 && parts[0] == "users":
		return h.getPublicUserProfile(ctx, parts[1])
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

	// Recurring Game Day series (item 238)
	case method == "GET" && rawPath == "gameday-series":
		return h.listGameDaySeries(ctx, req)
	case method == "POST" && rawPath == "gameday-series":
		return h.createGameDaySeries(ctx, req)
	case method == "DELETE" && len(parts) == 2 && parts[0] == "gameday-series":
		return h.cancelGameDaySeries(ctx, req, parts[1])

	// Maps
	case method == "GET" && rawPath == "maps":
		return h.listMaps(ctx, req)
	case method == "POST" && rawPath == "maps":
		return h.createMap(ctx, req)
	case method == "PATCH" && len(parts) == 2 && parts[0] == "maps":
		return h.updateMap(ctx, req, parts[1])

	// User settings / subscription
	case method == "GET" && rawPath == "me/settings":
		return h.getMySettings(ctx, req)
	case method == "PATCH" && rawPath == "me/settings":
		return h.patchMySettings(ctx, req)

	// User profile
	case method == "GET" && rawPath == "me/profile":
		return h.getMyProfile(ctx, req)
	case method == "PATCH" && rawPath == "me/profile":
		return h.patchMyProfile(ctx, req)
	case method == "PUT" && rawPath == "me/profile/picture":
		return h.uploadProfilePicture(ctx, req)

	// Ad config (public read, admin write)
	case method == "GET" && rawPath == "config/ads":
		return h.getAdConfig(ctx)
	case method == "GET" && rawPath == "admin/config/ads":
		return h.adminGetAdConfig(ctx, req)
	case method == "PATCH" && rawPath == "admin/config/ads":
		return h.adminPatchAdConfig(ctx, req)

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
	case method == "POST" && len(parts) == 6 && parts[0] == "admin" && parts[1] == "tanks" && parts[3] == "versions" && parts[5] == "reset-compile":
		return h.adminResetCompile(ctx, req, parts[2], parts[4])

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

	// Enforce per-tier tank limit (skip for admin users viewing as another user).
	us, err := h.store.GetUserSettings(ctx, uid)
	if err != nil {
		log.Printf("get user settings: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	tankLimit, _ := db.TierLimits(us.Tier)
	existingTanks, err := h.store.ListTanksByUser(ctx, uid)
	if err != nil {
		log.Printf("list tanks for limit: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if len(existingTanks) >= tankLimit {
		return errResp(http.StatusForbidden, fmt.Sprintf("tank limit reached (%d/%d for %s tier)", len(existingTanks), tankLimit, us.Tier)), nil
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

	// Item 218's AI-tank author override (getRankings) never extended to
	// this endpoint (item 231) — built-in tanks otherwise show whichever
	// real Cognito account happens to own the seeded record.
	if h.isAiTankID(tankID) {
		tank.AuthorName = aiTankAuthorName
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
	// Skipped for AI tanks (item 231) — otherwise viewing a built-in tank
	// through this endpoint (e.g. the admin Game Day roster) permanently
	// overwrites its stored AuthorName with the *viewing* admin's own name.
	if tank.AuthorName == "" && !h.isAiTankID(tankID) {
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
		// Every tank reachable here is one of the four built-in AI tanks by
		// construction (item 231, for consistency with getRankings/getTank/
		// adminListTanks — not currently rendered anywhere, but the same
		// stale-owner data would surface here too if that changes).
		tank.AuthorName = aiTankAuthorName
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

	// Enforce compilation quota; increment counter after version is committed.
	cus, err := h.store.GetUserSettings(ctx, uid)
	if err != nil {
		log.Printf("get user settings for compile: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	cus, _ = db.ResetWindowIfExpired(cus)
	_, compileLimit := db.TierLimits(cus.Tier)
	if cus.CompilationsThisWindow >= compileLimit {
		return errResp(http.StatusTooManyRequests, fmt.Sprintf("compilation limit reached (%d/%d for %s tier)", cus.CompilationsThisWindow, compileLimit, cus.Tier)), nil
	}
	if incErr := h.store.IncrementCompilations(ctx, uid, cus.WindowStart); incErr != nil {
		log.Printf("increment compilations: %v", incErr)
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
	// Auto-heal stuck "compiling" records: CodeBuild's post_build phase does not
	// run on TIMED_OUT, leaving DynamoDB permanently at "compiling".
	if ver.CompileStatus == "compiling" {
		if ver.BuildID != "" {
			if u := h.resolveStuckBuild(ctx, tankID, version, ver.BuildID); u != nil {
				return jsonResp(http.StatusOK, map[string]interface{}{
					"version":       ver.Version,
					"compileStatus": u.Status,
					"compileError":  u.CompileError,
				}), nil
			}
		} else if ver.CompileStartedAt > 0 && time.Now().Unix()-ver.CompileStartedAt > 900 {
			// Fallback for legacy records without a build ID: mark timed out after 15 min.
			errMsg := "compile timed out"
			_ = h.store.UpdateVersionCompile(ctx, tankID, version, db.CompileUpdate{
				Status: "failed", CompileError: errMsg,
			})
			return jsonResp(http.StatusOK, map[string]interface{}{
				"version":       ver.Version,
				"compileStatus": "failed",
				"compileError":  errMsg,
			}), nil
		}
	}

	return jsonResp(http.StatusOK, map[string]interface{}{
		"version":       ver.Version,
		"compileStatus": ver.CompileStatus,
		"compileError":  ver.CompileError,
	}), nil
}

// resolveStuckBuild queries CodeBuild for the given build ID and auto-heals
// the DynamoDB record when the build is in a terminal state. Returns the
// applied CompileUpdate, or nil if the build is still in progress.
func (h *handler) resolveStuckBuild(ctx context.Context, tankID, version, buildID string) *db.CompileUpdate {
	out, err := h.cb.BatchGetBuilds(ctx, &codebuild.BatchGetBuildsInput{Ids: []string{buildID}})
	if err != nil || len(out.Builds) == 0 {
		log.Printf("resolveStuckBuild %s/%s BatchGetBuilds: %v", tankID, version, err)
		return nil
	}
	build := out.Builds[0]
	var u db.CompileUpdate
	switch build.BuildStatus {
	case cbtypes.StatusTypeInProgress:
		return nil
	case cbtypes.StatusTypeTimedOut:
		u = db.CompileUpdate{Status: "failed", CompileError: "compile timed out"}
	case cbtypes.StatusTypeStopped:
		u = db.CompileUpdate{Status: "failed", CompileError: "compile was cancelled"}
	case cbtypes.StatusTypeSucceeded:
		// post_build ran and should have updated DynamoDB; if we're here it didn't.
		u = db.CompileUpdate{Status: "failed", CompileError: "compile completed but result was not recorded"}
	default: // FAILED, FAULT
		u = db.CompileUpdate{Status: "failed", CompileError: "build failed (see CodeBuild logs)"}
	}
	if updErr := h.store.UpdateVersionCompile(ctx, tankID, version, u); updErr != nil {
		log.Printf("resolveStuckBuild update %s/%s: %v", tankID, version, updErr)
	}
	return &u
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
	isAiTank := h.isAiTankID(tankID)
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

	// SEC-WASM-SIZE: reject oversized binaries before they enter the major
	// version track and get loaded by Wazero at match time.
	if ver.WasmS3Key != "" {
		headOut, headErr := h.s3.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(h.wasmBucket),
			Key:    aws.String(ver.WasmS3Key),
		})
		if headErr != nil {
			log.Printf("promote: head wasm %s: %v", ver.WasmS3Key, headErr)
			return errResp(http.StatusInternalServerError, "internal error"), nil
		}
		if headOut.ContentLength != nil && *headOut.ContentLength > maxWASMBytes {
			return errResp(http.StatusUnprocessableEntity, "compiled WASM binary exceeds 10 MiB size limit"), nil
		}
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
	if ver.Disqualified {
		return errResp(http.StatusUnprocessableEntity, "this version is disqualified and cannot register for Game Days"), nil
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

type uploadAvatarBody struct {
	// Data is the raw image bytes, base64-encoded — small enough at the
	// 512KB cap below that a JSON string field is simpler than wiring up
	// real multipart/form-data parsing through API Gateway, and matches
	// this file's existing convention of JSON-body-with-string-fields for
	// uploads (see submitVersionBody.Source for Go source uploads).
	Data        string `json:"data"`
	ContentType string `json:"contentType"`
}

const maxAvatarBytes = 512 * 1024

// uploadTankAvatar stores a user-uploaded avatar image in S3 under
// tank-avatars/{tankId}/avatar.{ext} (public-read via bucket policy, see
// StorageStack) and updates the Tank record's avatarUrl. See item 158.
func (h *handler) uploadTankAvatar(ctx context.Context, req events.APIGatewayV2HTTPRequest, tankID string) (events.APIGatewayV2HTTPResponse, error) {
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

	var body uploadAvatarBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return errResp(http.StatusBadRequest, "invalid request body"), nil
	}

	var ext string
	switch body.ContentType {
	case "image/png":
		ext = "png"
	case "image/jpeg":
		ext = "jpg"
	default:
		return errResp(http.StatusBadRequest, "contentType must be image/png or image/jpeg"), nil
	}

	imgBytes, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		return errResp(http.StatusBadRequest, "data must be valid base64"), nil
	}
	if len(imgBytes) == 0 {
		return errResp(http.StatusBadRequest, "data is required"), nil
	}
	if len(imgBytes) > maxAvatarBytes {
		return errResp(http.StatusBadRequest, fmt.Sprintf("avatar must be %d bytes or fewer", maxAvatarBytes)), nil
	}

	key := fmt.Sprintf("tank-avatars/%s/avatar.%s", tankID, ext)
	if _, err := h.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(h.assetsBucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(imgBytes),
		ContentType: aws.String(body.ContentType),
	}); err != nil {
		log.Printf("upload avatar %s: %v", tankID, err)
		return errResp(http.StatusInternalServerError, "failed to upload avatar"), nil
	}

	url := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", h.assetsBucket, h.region, key)
	if err := h.store.UpdateTankAvatarURL(ctx, tankID, url); err != nil {
		log.Printf("update tank avatar url %s: %v", tankID, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]string{"avatarUrl": url}), nil
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

	// Rematch (item 37) derives both sides entirely from the original ranked
	// match rather than from body.TankID/Version, so it's handled as its own
	// branch before the "caller's own tank" checks below (which assume
	// body.TankID names the caller's tank — not true for a rematch, where
	// the caller might be either TankA or TankB of the original match).
	if body.Opponent.Type == "rematch" {
		return h.startRematch(ctx, uid, body)
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

	case "informal":
		// Item 37: same shape as "own", except the opponent tank is
		// deliberately *not* required to belong to the caller — that's the
		// whole point of an Informal match (challenging another author).
		if body.Opponent.TankID == "" || body.Opponent.Version == "" {
			return errResp(http.StatusBadRequest, "opponent tankId and version required for type=informal"), nil
		}
		if _, err := h.store.GetTank(ctx, body.Opponent.TankID); errors.Is(err, db.ErrNotFound) {
			return errResp(http.StatusNotFound, "opponent tank not found"), nil
		} else if err != nil {
			return errResp(http.StatusInternalServerError, "internal error"), nil
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
		matchType = "informal"

	default:
		return errResp(http.StatusBadRequest, "opponent type must be 'ai', 'own', 'informal', or 'rematch'"), nil
	}

	// Item 234: Challenge (informal) reuses the same optional map picker
	// Test vs AI already has — mapId omitted still defaults to a random
	// maze below, exactly like Ranked (§6.4), but an explicit choice is
	// now allowed too rather than rejected outright.
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

// startRematch is the "rematch" branch of POST /matches (item 37): re-runs a
// previous ranked Game Day matchup between the exact same two tank/version
// pairs, unranked, with a freshly generated maze. Either participating
// author may trigger it — there's no separate invite/accept step, mirroring
// how Informal and Test matches also start immediately on request.
func (h *handler) startRematch(ctx context.Context, uid string, body startMatchBody) (events.APIGatewayV2HTTPResponse, error) {
	if body.Opponent.MatchID == "" {
		return errResp(http.StatusBadRequest, "opponent matchId is required for type=rematch"), nil
	}
	orig, err := h.store.GetMatch(ctx, body.Opponent.MatchID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "match not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if orig.MatchType != "ranked" {
		return errResp(http.StatusBadRequest, "rematch is only available for ranked Game Day matches"), nil
	}
	// Game Day autofill (item 248) may suffix TankID (e.g. "builtin-scout#2")
	// to distinguish repeated copies of the same built-in AI — never a real
	// Tank record, so strip it back off before looking one up.
	tankA, err := h.store.GetTank(ctx, db.RealTankID(orig.TankA.TankID))
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	tankB, err := h.store.GetTank(ctx, db.RealTankID(orig.TankB.TankID))
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if tankA.UserID != uid && tankB.UserID != uid {
		return errResp(http.StatusForbidden, "you did not participate in this match"), nil
	}

	matchID := newUUID()
	match := db.Match{
		MatchID:   matchID,
		MatchType: "rematch",
		Status:    "scheduled",
		MazeSeed:  strconv.FormatInt(rand.Int63(), 10),
		TankA:     orig.TankA,
		TankB:     orig.TankB,
		CreatedAt: time.Now().Unix(),
		TTL:       time.Now().AddDate(0, 0, testMatchTTLDays).Unix(),
	}
	if err := h.store.PutMatch(ctx, match); err != nil {
		log.Printf("put rematch: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	payload, _ := json.Marshal(map[string]string{"matchId": matchID})
	if _, err := h.lambdaSvc.Invoke(ctx, &lambdasvc.InvokeInput{
		FunctionName:   aws.String(h.matchRunnerFunc),
		InvocationType: ltypes.InvocationTypeEvent,
		Payload:        payload,
	}); err != nil {
		log.Printf("invoke match-runner for rematch %s: %v", matchID, err)
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

// getMatchExport implements item 35 (functional spec §9.5): Tank Authors can
// export the full match data as a JSON file from any replay. Nothing is
// pre-generated — the plain-JSON copy is decompressed from the source tick
// log and written to S3 only when a participating owner actually requests
// it, under exports/<matchId>.json with its own short lifecycle rule
// (storage-stack.ts), independent of the 7-day source tick log expiration.
// Returns the presigned download URL as JSON rather than a redirect so the
// frontend can navigate the browser to it directly (a fetch()-followed
// redirect to S3 would hit CORS; a page navigation/download does not).
func (h *handler) getMatchExport(ctx context.Context, req events.APIGatewayV2HTTPRequest, matchID string) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	match, err := h.store.GetMatch(ctx, matchID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "match not found"), nil
	}
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	// Game Day autofill (item 248) may suffix TankID (e.g. "builtin-scout#2")
	// to distinguish repeated copies of the same built-in AI — never a real
	// Tank record, so strip it back off before looking one up.
	tankA, err := h.store.GetTank(ctx, db.RealTankID(match.TankA.TankID))
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	tankB, err := h.store.GetTank(ctx, db.RealTankID(match.TankB.TankID))
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if tankA.UserID != uid && tankB.UserID != uid {
		return errResp(http.StatusForbidden, "you did not participate in this match"), nil
	}
	if match.TickLogS3Key == "" {
		return errResp(http.StatusNotFound, "tick log not yet available"), nil
	}

	obj, err := h.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(h.logsBucket),
		Key:    aws.String(match.TickLogS3Key),
	})
	var nsk *s3types.NoSuchKey
	if errors.As(err, &nsk) {
		// Source tick log already expired off S3 (7-day lifecycle rule) —
		// distinct from a generic error so the frontend can drop the Export
		// button for this match instead of showing it as a transient failure.
		return errResp(http.StatusGone, "export no longer available — match data has expired"), nil
	}
	if err != nil {
		log.Printf("get tick log for export %s: %v", matchID, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	defer obj.Body.Close()

	gz, err := gzip.NewReader(obj.Body)
	if err != nil {
		log.Printf("gunzip tick log for export %s: %v", matchID, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	plain, err := io.ReadAll(gz)
	gz.Close()
	if err != nil {
		log.Printf("read tick log for export %s: %v", matchID, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	exportKey := fmt.Sprintf("exports/%s.json", matchID)
	if _, err := h.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(h.logsBucket),
		Key:         aws.String(exportKey),
		Body:        bytes.NewReader(plain),
		ContentType: aws.String("application/json"),
	}); err != nil {
		log.Printf("put export %s: %v", matchID, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	presigner := s3.NewPresignClient(h.s3)
	presigned, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     aws.String(h.logsBucket),
		Key:                        aws.String(exportKey),
		ResponseContentDisposition: aws.String(fmt.Sprintf(`attachment; filename="%s.json"`, matchID)),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = tickLogPresignTTL
	})
	if err != nil {
		log.Printf("presign export %s: %v", matchID, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]string{"url": presigned.URL}), nil
}

// ---- Auth --------------------------------------------------------------

// forgotPassword is deliberately timing-safe (item 217): it always returns
// 202 immediately, before any Cognito lookup, IdP branch decision, or email
// send happens. The actual work — looking up the email, deciding whether the
// account is native email+password or a Google/Facebook IdP account, and
// sending the appropriate email — runs in the background via an async
// (fire-and-forget) invocation of forgot-password-worker, so response timing
// never depends on which branch runs or whether the email exists at all.
func (h *handler) forgotPassword(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	var body struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil || strings.TrimSpace(body.Email) == "" {
		return errResp(http.StatusBadRequest, "email required"), nil
	}
	payload, _ := json.Marshal(map[string]string{"email": strings.TrimSpace(body.Email)})
	if _, err := h.lambdaSvc.Invoke(ctx, &lambdasvc.InvokeInput{
		FunctionName:   aws.String(h.forgotPasswordWorkerFn),
		InvocationType: ltypes.InvocationTypeEvent,
		Payload:        payload,
	}); err != nil {
		log.Printf("invoke forgot-password-worker: %v", err)
	}
	return events.APIGatewayV2HTTPResponse{StatusCode: http.StatusAccepted}, nil
}

// ---- Rankings and Game Days -------------------------------------------------

// aiTankAuthorName and aiTankAvatarURL are what the Leaderboard shows for
// built-in AI tanks (item 218) instead of resolving whatever real Cognito
// account happens to own the seeded tank record.
const (
	aiTankAuthorName = "TankMaze AI-Tank"
	aiTankAvatarURL  = "/avatar.png"
)

// isAiTankID reports whether tankID belongs to one of the built-in AI tanks
// (Scout/Bruiser/Ranger/Randy), checked both by the well-known "builtin-"
// prefix and by matching the configured env-var IDs directly.
func (h *handler) isAiTankID(tankID string) bool {
	return strings.HasPrefix(tankID, "builtin-") ||
		tankID == h.scoutTankID || tankID == h.bruiserTankID || tankID == h.rangerTankID || tankID == h.randyTankID
}

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
		AuthorUserID   string `json:"authorUserId,omitempty"`
		AuthorPicture  string `json:"authorPicture,omitempty"`
		AvatarURL      string `json:"avatarUrl,omitempty"`
		ActiveVersion  string `json:"activeVersion,omitempty"`
		GlobalScore    int    `json:"globalScore"`
		BestFinish     *int   `json:"bestFinish"`
		GameDays       int    `json:"gameDays"`
		LastActiveAt   int64  `json:"lastActiveAt"`
	}
	// Cache picture lookups per unique author — many rows share the same
	// owner, and each lookup is a Cognito ListUsers call (item 213, reusing
	// item 210's lookupUserPicture).
	pictureCache := make(map[string]string)
	result := make([]entry, len(tanks))
	for i, t := range tanks {
		authorUsername := authorNameOrID(t)
		var picture string
		if h.isAiTankID(t.TankID) {
			authorUsername = aiTankAuthorName
			picture = aiTankAvatarURL
		} else {
			cached, ok := pictureCache[t.UserID]
			if !ok {
				cached = h.lookupUserPicture(ctx, t.UserID)
				pictureCache[t.UserID] = cached
			}
			picture = cached
		}
		// Item 227: activeVersion was declared on the frontend type and
		// displayed on Leaderboard.tsx, but never actually populated here —
		// the latest major version is the same one getTank's public view
		// (and now item 37's Challenge flow) treats as "the" challengeable
		// version, so reuse that definition rather than inventing a new one.
		var activeVersion string
		if versions, err := h.store.ListVersionsByTank(ctx, t.TankID); err == nil {
			activeVersion = latestMajorVersion(versions)
		}
		result[i] = entry{
			Rank:           i + 1,
			TankID:         t.TankID,
			TankName:       t.Name,
			AuthorUsername: authorUsername,
			AuthorUserID:   t.UserID,
			AuthorPicture:  picture,
			AvatarURL:      t.AvatarURL,
			ActiveVersion:  activeVersion,
			GlobalScore:    t.GlobalScore,
			BestFinish:     t.BestFinish,
			GameDays:       t.GameDaysCount,
			LastActiveAt:   t.LastActiveAt,
		}
	}
	return jsonResp(http.StatusOK, result), nil
}

// latestMajorVersion returns the highest-numbered major version string in
// versions (e.g. "v3"), or "" if the tank has no promoted major version yet.
// Shared by getRankings (item 227) and getTank's public-view major selection.
func latestMajorVersion(versions []db.TankVersion) string {
	best := ""
	bestNum := -1
	for _, v := range versions {
		if v.VersionType != "major" {
			continue
		}
		maj, _, _, ok := parseVersion(v.Version)
		if !ok || maj <= bestNum {
			continue
		}
		best, bestNum = v.Version, maj
	}
	return best
}

// publicTankSummary is the subset of a Tank record safe to show on another
// user's public profile — deliberately a hand-built allow-list rather than
// returning db.Tank directly, so a future field added to Tank doesn't leak
// here by default.
type publicTankSummary struct {
	TankID        string `json:"tankId"`
	Name          string `json:"name"`
	AvatarURL     string `json:"avatarUrl,omitempty"`
	GlobalScore   int    `json:"globalScore"`
	BestFinish    *int   `json:"bestFinish"`
	GameDaysCount int    `json:"gameDaysCount"`
	LastActiveAt  int64  `json:"lastActiveAt"`
}

// lookupUserPicture returns a user's avatar by sub, preferring the durable
// UserSettings.AvatarURL (item 229 — immune to Google/Facebook's IdP
// attribute mapping re-syncing Cognito's "picture" attribute on every
// federated login) over the Cognito attribute (set directly for
// non-federated accounts, or pre-fix federated ones). Deliberately extracts
// only the "picture" Cognito attribute — never "email" or any other — so
// callers on public/other-user paths (item 210's getPublicUserProfile, item
// 213's getRankings) can't accidentally leak email just by reusing this helper.
func (h *handler) lookupUserPicture(ctx context.Context, sub string) string {
	if us, err := h.store.GetUserSettings(ctx, sub); err == nil && us.AvatarURL != "" {
		return us.AvatarURL
	}
	out, err := h.cognito.ListUsers(ctx, &cognitoidp.ListUsersInput{
		UserPoolId: aws.String(h.userPoolID),
		Filter:     aws.String(fmt.Sprintf(`sub = "%s"`, sub)),
		Limit:      aws.Int32(1),
	})
	if err != nil || len(out.Users) == 0 {
		return ""
	}
	return cognitoAttr(out.Users[0].Attributes, "picture")
}

// getPublicUserProfile is GET /users/{sub} — public, no auth required (same
// as /rankings, which links here). Returns only a display name, an optional
// picture, and a public tank list. Email is never read for this path: the
// display name comes from the authorName already denormalized onto the
// user's own tanks (same field patchMyProfile/getTank backfill), and the
// only Cognito attribute ever extracted is "picture" — never "email" or any
// other attribute, so there's no attribute list here for email to
// accidentally leak from. See item 210.
func (h *handler) getPublicUserProfile(ctx context.Context, sub string) (events.APIGatewayV2HTTPResponse, error) {
	tanks, err := h.store.ListTanksByUser(ctx, sub)
	if err != nil {
		log.Printf("getPublicUserProfile list tanks %s: %v", sub, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	// Name resolution order matches getMyProfile/resolveUserDisplay (item
	// 225): UserSettings.DisplayName first, tanks' authorName as fallback.
	name := sub
	if us, err := h.store.GetUserSettings(ctx, sub); err == nil && us.DisplayName != "" {
		name = us.DisplayName
	} else {
		for _, t := range tanks {
			if t.AuthorName != "" {
				name = t.AuthorName
				break
			}
		}
	}

	picture := h.lookupUserPicture(ctx, sub)

	if len(tanks) == 0 && picture == "" && name == sub {
		return errResp(http.StatusNotFound, "user not found"), nil
	}

	publicTanks := make([]publicTankSummary, len(tanks))
	for i, t := range tanks {
		publicTanks[i] = publicTankSummary{
			TankID:        t.TankID,
			Name:          t.Name,
			AvatarURL:     t.AvatarURL,
			GlobalScore:   t.GlobalScore,
			BestFinish:    t.BestFinish,
			GameDaysCount: t.GameDaysCount,
			LastActiveAt:  t.LastActiveAt,
		}
	}

	return jsonResp(http.StatusOK, map[string]interface{}{
		"sub":     sub,
		"name":    name,
		"picture": picture,
		"tanks":   publicTanks,
	}), nil
}

// ---- Friends (item 223) --------------------------------------------------

// resolveUserDisplay returns the same {name, picture} a viewer would see on
// that user's public profile (getPublicUserProfile) — reused here so a
// friends list never has to duplicate that name/picture resolution logic.
// Name resolution order matches getMyProfile (item 225): UserSettings
// .DisplayName first, since it's the only copy immune to federated-login
// resync; tanks' authorName as a fallback for names set before that fix.
func (h *handler) resolveUserDisplay(ctx context.Context, sub string) (name, picture string) {
	name = sub
	if us, err := h.store.GetUserSettings(ctx, sub); err == nil && us.DisplayName != "" {
		name = us.DisplayName
	} else if tanks, err := h.store.ListTanksByUser(ctx, sub); err == nil {
		for _, t := range tanks {
			if t.AuthorName != "" {
				name = t.AuthorName
				break
			}
		}
	}
	return name, h.lookupUserPicture(ctx, sub)
}

type friendEntry struct {
	UserID  string `json:"userId"`
	Name    string `json:"name"`
	Picture string `json:"picture,omitempty"`
	// LastMessageAt/LastMessageFromMe (item 223 chat) are only populated for
	// accepted friends — the frontend uses them to render an unread badge
	// without a separate read-receipt schema: it's "unread" if there's a
	// message not from me newer than this conversation's locally-stored
	// last-seen timestamp.
	LastMessageAt     *int64 `json:"lastMessageAt,omitempty"`
	LastMessageFromMe bool   `json:"lastMessageFromMe,omitempty"`
}

// listFriends buckets the caller's friendships into accepted friends,
// requests they've received (incoming), and requests they've sent
// (outgoing) — a single Query against the caller's own partition per
// db.ListFriendships's dual-item design.
func (h *handler) listFriends(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	rows, err := h.store.ListFriendships(ctx, uid)
	if err != nil {
		log.Printf("listFriends: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	friends := []friendEntry{}
	incoming := []friendEntry{}
	outgoing := []friendEntry{}
	blocked := []friendEntry{}
	for _, f := range rows {
		name, picture := h.resolveUserDisplay(ctx, f.FriendID)
		entry := friendEntry{UserID: f.FriendID, Name: name, Picture: picture}
		switch {
		case f.Status == db.FriendshipBlocked:
			// Item 226: only surface blocks the caller placed — a user they
			// blocked has no reason to see it reflected in their own list.
			if f.RequestedBy == uid {
				blocked = append(blocked, entry)
			}
		case f.Status == db.FriendshipAccepted:
			if last, err := h.store.GetLatestMessage(ctx, db.ConversationID(uid, f.FriendID)); err == nil {
				sentAt := last.SentAt
				entry.LastMessageAt = &sentAt
				entry.LastMessageFromMe = last.SenderID == uid
			}
			friends = append(friends, entry)
		case f.RequestedBy == uid:
			outgoing = append(outgoing, entry)
		default:
			incoming = append(incoming, entry)
		}
	}
	return jsonResp(http.StatusOK, map[string]interface{}{
		"friends":  friends,
		"incoming": incoming,
		"outgoing": outgoing,
		"blocked":  blocked,
	}), nil
}

func (h *handler) sendFriendRequest(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	var body struct {
		ToUserID string `json:"toUserId"`
	}
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil || strings.TrimSpace(body.ToUserID) == "" {
		return errResp(http.StatusBadRequest, "toUserId required"), nil
	}
	toUserID := strings.TrimSpace(body.ToUserID)
	if toUserID == uid {
		return errResp(http.StatusBadRequest, "cannot friend yourself"), nil
	}
	existing, err := h.store.GetFriendship(ctx, uid, toUserID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		log.Printf("sendFriendRequest lookup: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if err == nil {
		if existing.Status == db.FriendshipBlocked {
			// Item 226: deliberately generic — doesn't reveal to the sender
			// which of the two of them placed the block.
			return errResp(http.StatusForbidden, "unable to send friend request"), nil
		}
		if existing.Status == db.FriendshipAccepted {
			return errResp(http.StatusConflict, "already friends"), nil
		}
		return errResp(http.StatusConflict, "friend request already pending"), nil
	}
	if err := h.store.SendFriendRequest(ctx, uid, toUserID); err != nil {
		log.Printf("sendFriendRequest: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusCreated, map[string]string{"status": "pending"}), nil
}

// respondFriendRequest handles both accept and reject — a rejected request
// and a cancelled outgoing request are the same operation on this data model
// (delete both sides of the pairing), so reject reuses RemoveFriendship.
func (h *handler) respondFriendRequest(ctx context.Context, req events.APIGatewayV2HTTPRequest, fromUserID string, accept bool) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	existing, err := h.store.GetFriendship(ctx, uid, fromUserID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "no pending request"), nil
	}
	if err != nil {
		log.Printf("respondFriendRequest lookup: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if existing.Status != db.FriendshipPending || existing.RequestedBy == uid {
		return errResp(http.StatusConflict, "no pending incoming request from this user"), nil
	}
	if accept {
		if err := h.store.AcceptFriendRequest(ctx, uid, fromUserID); err != nil {
			log.Printf("acceptFriendRequest: %v", err)
			return errResp(http.StatusInternalServerError, "internal error"), nil
		}
	} else if err := h.store.RemoveFriendship(ctx, uid, fromUserID); err != nil {
		log.Printf("rejectFriendRequest: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]string{"status": "ok"}), nil
}

// removeFriend deletes an accepted friendship — also reused by the frontend
// for withdrawing an outgoing request the current user sent (same underlying
// operation, see respondFriendRequest's comment).
func (h *handler) removeFriend(ctx context.Context, req events.APIGatewayV2HTTPRequest, friendID string) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	if err := h.store.RemoveFriendship(ctx, uid, friendID); err != nil {
		log.Printf("removeFriend: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]string{"status": "ok"}), nil
}

// blockUser is POST /friends/block (item 226). Removes any existing
// friendship (equivalent to unfriend) and replaces it with a blocked-status
// pairing that also stops the target from sending a new friend request.
func (h *handler) blockUser(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	var body struct {
		TargetUserID string `json:"targetUserId"`
	}
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil || strings.TrimSpace(body.TargetUserID) == "" {
		return errResp(http.StatusBadRequest, "targetUserId required"), nil
	}
	targetUserID := strings.TrimSpace(body.TargetUserID)
	if targetUserID == uid {
		return errResp(http.StatusBadRequest, "cannot block yourself"), nil
	}
	if err := h.store.BlockUser(ctx, uid, targetUserID); err != nil {
		log.Printf("blockUser: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]string{"status": "blocked"}), nil
}

// unblockUser is POST /friends/unblock (item 226) — only the user who placed
// the block may lift it.
func (h *handler) unblockUser(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	var body struct {
		TargetUserID string `json:"targetUserId"`
	}
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil || strings.TrimSpace(body.TargetUserID) == "" {
		return errResp(http.StatusBadRequest, "targetUserId required"), nil
	}
	targetUserID := strings.TrimSpace(body.TargetUserID)
	err := h.store.UnblockUser(ctx, uid, targetUserID)
	if errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "no block exists"), nil
	}
	if errors.Is(err, db.ErrNotBlocker) {
		return errResp(http.StatusForbidden, "only the user who placed the block can unblock"), nil
	}
	if err != nil {
		log.Printf("unblockUser: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]string{"status": "ok"}), nil
}

const maxMessageBodyLen = 2000

// canMessage reports whether uid and otherID are accepted friends —
// blocking (item 226) always clears the friendship first, so an "accepted"
// status already implies no block exists between them; no separate check
// needed.
func (h *handler) canMessage(ctx context.Context, uid, otherID string) (bool, error) {
	f, err := h.store.GetFriendship(ctx, uid, otherID)
	if errors.Is(err, db.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return f.Status == db.FriendshipAccepted, nil
}

// sendMessage is POST /messages (item 223 Part 2 chat): {toUserId, body}.
func (h *handler) sendMessage(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	var body struct {
		ToUserID string `json:"toUserId"`
		Body     string `json:"body"`
	}
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return errResp(http.StatusBadRequest, "invalid request body"), nil
	}
	toUserID := strings.TrimSpace(body.ToUserID)
	text := strings.TrimSpace(body.Body)
	if toUserID == "" || text == "" {
		return errResp(http.StatusBadRequest, "toUserId and body are required"), nil
	}
	if len(text) > maxMessageBodyLen {
		return errResp(http.StatusBadRequest, fmt.Sprintf("body exceeds %d characters", maxMessageBodyLen)), nil
	}
	ok, err := h.canMessage(ctx, uid, toUserID)
	if err != nil {
		log.Printf("sendMessage friendship check: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if !ok {
		return errResp(http.StatusForbidden, "you can only message accepted friends"), nil
	}
	msg, err := h.store.SendMessage(ctx, uid, toUserID, text)
	if err != nil {
		log.Printf("sendMessage: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusCreated, msg), nil
}

// listMessages is GET /messages/{userId}?since=<messageId> (item 223 Part 2).
// Without "since", returns the most recent page of history; with it, only
// messages after that cursor — the polling case.
func (h *handler) listMessages(ctx context.Context, req events.APIGatewayV2HTTPRequest, otherUserID string) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	ok, err := h.canMessage(ctx, uid, otherUserID)
	if err != nil {
		log.Printf("listMessages friendship check: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if !ok {
		return errResp(http.StatusForbidden, "you can only view messages with accepted friends"), nil
	}
	since := req.QueryStringParameters["since"]
	limit := int32(50)
	if since != "" {
		limit = 200 // polling: don't miss a burst of messages between polls
	}
	messages, err := h.store.ListMessages(ctx, db.ConversationID(uid, otherUserID), since, limit)
	if err != nil {
		log.Printf("listMessages: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, messages), nil
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

	gd, err := h.materializer().Materialize(ctx, scheduling.Params{
		Name:                strings.TrimSpace(body.Name),
		RegistrationCloseAt: regClose,
		RoundRobinAt:        rrAt,
		FinalAt:             finalAt,
		Autofill:            body.Autofill,
		ForcedMapIDs:        body.ForcedMapIDs,
		RandomMaps:          body.RandomMaps,
	})
	if err != nil {
		log.Printf("materialize gameday: %v", err)
		return errResp(http.StatusInternalServerError, "failed to create game day"), nil
	}

	return jsonResp(http.StatusCreated, gd), nil
}

// materializer builds a scheduling.Materializer from this handler's already-
// configured scheduler client/ARNs, shared by createGameDay and the
// recurring-series handlers (item 238).
func (h *handler) materializer() *scheduling.Materializer {
	return &scheduling.Materializer{
		Store:                  h.store,
		SchedulerSvc:           h.schedulerSvc,
		SchedulerRoleArn:       h.schedulerRoleArn,
		TournamentSchedulerArn: h.tournamentSchedulerArn,
		SchedulerDLQArn:        h.schedulerDLQArn,
	}
}

type createGameDaySeriesBody struct {
	Name      string `json:"name"`
	Frequency string `json:"frequency"` // weekly | monthly | every_n_days
	// ByMonthDay (monthly only, 1-31) and IntervalDays (every_n_days only,
	// >=1) are validated against Frequency below.
	ByMonthDay   int `json:"byMonthDay"`
	IntervalDays int `json:"intervalDays"`
	// The first occurrence's own schedule — its gaps become the template
	// reapplied to every later occurrence (registrationLeadSeconds /
	// finalLeadSeconds).
	RegistrationCloseAt string   `json:"registrationCloseAt"`
	RoundRobinAt        string   `json:"roundRobinAt"`
	FinalAt             string   `json:"finalAt"`
	Autofill            bool     `json:"autofill"`
	ForcedMapIDs        []string `json:"forcedMapIds"`
	RandomMaps          bool     `json:"randomMaps"`
	// MaxOccurrences is 0 for indefinite repetition, or a fixed repeat count.
	MaxOccurrences int `json:"maxOccurrences"`
}

// createGameDaySeries creates a recurring Game Day series (item 238) and
// materializes its first occurrence immediately, so an admin sees it in the
// Game Day list right away rather than waiting for the next
// series-materializer tick. Subsequent occurrences are created by that
// rolling job as each prior one's roundRobin time approaches.
func (h *handler) createGameDaySeries(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "admin access required"), nil
	}
	var body createGameDaySeriesBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return errResp(http.StatusBadRequest, "invalid request body"), nil
	}

	switch body.Frequency {
	case db.FreqWeekly:
	case db.FreqMonthly:
		if body.ByMonthDay < 1 || body.ByMonthDay > 31 {
			return errResp(http.StatusBadRequest, "byMonthDay must be between 1 and 31"), nil
		}
	case db.FreqEveryNDays:
		if body.IntervalDays < 1 {
			return errResp(http.StatusBadRequest, "intervalDays must be at least 1"), nil
		}
	default:
		return errResp(http.StatusBadRequest, "frequency must be weekly, monthly, or every_n_days"), nil
	}
	if body.MaxOccurrences < 0 {
		return errResp(http.StatusBadRequest, "maxOccurrences must be 0 (indefinite) or positive"), nil
	}

	parseAt := func(s string) (time.Time, bool) {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, false
		}
		return t.UTC(), true
	}
	regClose, ok := parseAt(body.RegistrationCloseAt)
	if !ok {
		return errResp(http.StatusBadRequest, "registrationCloseAt must be ISO 8601"), nil
	}
	rrAt, ok := parseAt(body.RoundRobinAt)
	if !ok {
		return errResp(http.StatusBadRequest, "roundRobinAt must be ISO 8601"), nil
	}
	finalAt, ok := parseAt(body.FinalAt)
	if !ok {
		return errResp(http.StatusBadRequest, "finalAt must be ISO 8601"), nil
	}
	if !regClose.Before(rrAt) {
		return errResp(http.StatusBadRequest, "registration must close before round robin"), nil
	}
	if !rrAt.Before(finalAt) {
		return errResp(http.StatusBadRequest, "round robin must start before final"), nil
	}

	seriesID := newUUID()
	series := db.GameDaySeries{
		SeriesID:                seriesID,
		Name:                    strings.TrimSpace(body.Name),
		Frequency:               body.Frequency,
		ByMonthDay:              body.ByMonthDay,
		IntervalDays:            body.IntervalDays,
		RegistrationLeadSeconds: int64(rrAt.Sub(regClose).Seconds()),
		FinalLeadSeconds:        int64(finalAt.Sub(rrAt).Seconds()),
		Autofill:                body.Autofill,
		ForcedMapIDs:            body.ForcedMapIDs,
		RandomMaps:              body.RandomMaps,
		MaxOccurrences:          body.MaxOccurrences,
		OccurrencesCreated:      0,
		NextOccurrenceAt:        rrAt.Format(time.RFC3339),
		Status:                  db.SeriesStatusActive,
		CreatedAt:               time.Now().Unix(),
	}
	if err := h.store.PutGameDaySeries(ctx, series); err != nil {
		log.Printf("put gameday series: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	gd, err := h.materializer().Materialize(ctx, scheduling.Params{
		Name:                series.Name,
		RegistrationCloseAt: regClose,
		RoundRobinAt:        rrAt,
		FinalAt:             finalAt,
		Autofill:            body.Autofill,
		ForcedMapIDs:        body.ForcedMapIDs,
		RandomMaps:          body.RandomMaps,
		SeriesID:            seriesID,
	})
	if err != nil {
		log.Printf("materialize first occurrence for series %s: %v", seriesID, err)
		if delErr := h.store.CancelGameDaySeries(ctx, seriesID); delErr != nil {
			log.Printf("rollback cancel series %s: %v", seriesID, delErr)
		}
		return errResp(http.StatusInternalServerError, "failed to create first occurrence"), nil
	}

	finished := body.MaxOccurrences == 1
	nextAt := db.NextOccurrenceTime(series, rrAt)
	if err := h.store.AdvanceGameDaySeries(ctx, seriesID, series.NextOccurrenceAt, nextAt.Format(time.RFC3339), finished); err != nil {
		// Non-fatal: the series and its first occurrence both already exist.
		// Worst case, series-materializer's next tick re-evaluates this
		// series against a stale NextOccurrenceAt — acceptable for a
		// low-traffic admin feature, logged loudly so it's visible if it
		// ever actually recurs.
		log.Printf("advance gameday series %s past first occurrence: %v", seriesID, err)
	}

	return jsonResp(http.StatusCreated, map[string]interface{}{"series": series, "firstOccurrence": gd}), nil
}

func (h *handler) listGameDaySeries(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "admin access required"), nil
	}
	all, err := h.store.ListGameDaySeries(ctx)
	if err != nil {
		log.Printf("list gameday series: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, all), nil
}

// cancelGameDaySeries stops future materialization only — already-materialized
// occurrences are untouched (item 238, point 6). Cancelling one occurrence
// remains a separate action via the existing DELETE /gamedays/{id}.
func (h *handler) cancelGameDaySeries(ctx context.Context, req events.APIGatewayV2HTTPRequest, seriesID string) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "admin access required"), nil
	}
	if _, err := h.store.GetGameDaySeries(ctx, seriesID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return errResp(http.StatusNotFound, "series not found"), nil
		}
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if err := h.store.CancelGameDaySeries(ctx, seriesID); err != nil {
		log.Printf("cancel gameday series %s: %v", seriesID, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return events.APIGatewayV2HTTPResponse{StatusCode: http.StatusNoContent}, nil
}

// upsertSchedule creates or updates an EventBridge Scheduler schedule so that
// the phase fires at the given time. It is best-effort: errors are logged but
// do not surface to the caller. Schedules in the past are skipped.
// upsertSchedule creates or updates the named EventBridge schedule. Returns
// nil when the scheduler integration isn't configured at all (a deliberate
// deployment-mode branch, e.g. localserver — not a failure the caller needs
// to know about). Any other early return is a real failure to sync
// EventBridge with the caller's intent and is now returned as an error
// (item 254) instead of only being logged — the caller (patchGameDay) surfaces
// it in the response so an admin edit can never look like it succeeded while
// silently leaving the real trigger untouched.
func (h *handler) upsertSchedule(ctx context.Context, name, phase, gameDayID string, at time.Time) error {
	if h.schedulerSvc == nil || h.schedulerRoleArn == "" || h.tournamentSchedulerArn == "" {
		return nil
	}
	if !at.After(time.Now()) {
		err := fmt.Errorf("target time %s is not in the future", at.UTC().Format(time.RFC3339))
		log.Printf("upsertSchedule %s: %v — skipping", name, err)
		return err
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
		Name:                       aws.String(name),
		GroupName:                  aws.String("tankmaze-gamedays"),
		ScheduleExpression:         aws.String(expr),
		ScheduleExpressionTimezone: aws.String("UTC"),
		FlexibleTimeWindow:         ftw,
		Target:                     target,
		ActionAfterCompletion:      schedulertypes.ActionAfterCompletionDelete,
	})
	if err == nil {
		return nil
	}
	var notFound *schedulertypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		log.Printf("update schedule %s: %v", name, err)
		return fmt.Errorf("update schedule: %w", err)
	}
	// Schedule doesn't exist (already fired or was never created) — create it.
	if _, createErr := h.schedulerSvc.CreateSchedule(ctx, &schedulersvc.CreateScheduleInput{
		Name:                       aws.String(name),
		GroupName:                  aws.String("tankmaze-gamedays"),
		ScheduleExpression:         aws.String(expr),
		ScheduleExpressionTimezone: aws.String("UTC"),
		FlexibleTimeWindow:         ftw,
		Target:                     target,
		ActionAfterCompletion:      schedulertypes.ActionAfterCompletionDelete,
	}); createErr != nil {
		log.Printf("create schedule %s (after update 404): %v", name, createErr)
		return fmt.Errorf("create schedule: %w", createErr)
	}
	return nil
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
	// cleanupFailures (item 255) collects anything below that didn't fully
	// clean up so the caller can be warned instead of a plain silent
	// success — same pattern item 254 added for patchGameDay's
	// rescheduleFailures.
	var cleanupFailures []string
	for _, name := range scheduleNames {
		if _, delErr := h.schedulerSvc.DeleteSchedule(ctx, &schedulersvc.DeleteScheduleInput{
			Name:      aws.String(name),
			GroupName: aws.String("tankmaze-gamedays"),
		}); delErr != nil {
			// ResourceNotFoundException is fine — the schedule already fired
			// and self-deleted (ActionAfterCompletion: DELETE), or was never
			// created. Anything else (throttling, a transient AWS error, a
			// permissions failure) previously got silently swallowed the
			// same way, leaving a stale EventBridge schedule alive after the
			// admin was told the delete succeeded — mirrors upsertSchedule's
			// error-typing (item 254).
			var notFound *schedulertypes.ResourceNotFoundException
			if errors.As(delErr, &notFound) {
				continue
			}
			log.Printf("delete schedule %s: %v", name, delErr)
			cleanupFailures = append(cleanupFailures, "schedule:"+name)
		}
	}

	if err := h.store.DeleteGameDay(ctx, gameDayID); err != nil {
		log.Printf("delete gameday %s: %v", gameDayID, err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	for _, t := range gd.RegisteredTanks {
		if err := h.store.RemoveVersionRegistration(ctx, t.TankID, t.Version, gameDayID); err != nil {
			cleanupFailures = append(cleanupFailures, "registration:"+t.TankID+"@"+t.Version)
		}
	}
	if len(cleanupFailures) > 0 {
		log.Printf("delete gameday %s: cleanup failures %v (stale entries remain)", gameDayID, cleanupFailures)
		return jsonResp(http.StatusOK, struct {
			CleanupFailures []string `json:"cleanupFailures"`
		}{cleanupFailures}), nil
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

	// Reject up front (item 254) any newly-set phase time that isn't
	// comfortably in the future — by request-processing time it could
	// already be at or past "now", which would make upsertSchedule silently
	// skip rescheduling EventBridge later while this handler still reports
	// success. Only fields actually present in this PATCH are checked; an
	// existing, already-past field left untouched is fine.
	leadCutoff := time.Now().Add(minScheduleLeadTime)
	for _, f := range []struct{ label, val string }{
		{"registrationCloseAt", body.RegistrationCloseAt},
		{"roundRobinAt", body.RoundRobinAt},
		{"finalAt", body.FinalAt},
	} {
		if f.val == "" {
			continue
		}
		if t, ok := parseAt(f.val); ok && !t.After(leadCutoff) {
			return errResp(http.StatusBadRequest, fmt.Sprintf("%s must be at least %s in the future", f.label, minScheduleLeadTime)), nil
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

	// Sync EventBridge schedules for any rescheduled phases. The DB write
	// above already succeeded, so a sync failure here doesn't fail the
	// response — but (item 254) it's now collected and returned in
	// rescheduleFailures rather than only logged, so an admin can never see
	// a plain success when the real trigger didn't actually move.
	var rescheduleFailures []string
	trackReschedule := func(phase string, err error) {
		if err != nil {
			log.Printf("patch gameday %s: reschedule %s: %v", gameDayID, phase, err)
			rescheduleFailures = append(rescheduleFailures, phase)
		}
	}
	if body.RegistrationCloseAt != "" {
		if rc, ok := parseAt(body.RegistrationCloseAt); ok {
			trackReschedule("registration_close", h.upsertSchedule(ctx, gameDayID+"-reg-close", "registration_close", gameDayID, rc))
		}
	}
	if body.RoundRobinAt != "" {
		if rr, ok := parseAt(body.RoundRobinAt); ok {
			trackReschedule("round_robin", h.upsertSchedule(ctx, gameDayID+"-rr", "round_robin", gameDayID, rr))
		}
	}
	if body.FinalAt != "" {
		fn, _ := parseAt(body.FinalAt)
		trackReschedule("final", h.upsertSchedule(ctx, gameDayID+"-final", "final", gameDayID, fn))
		for i, t := range patchElimTimes {
			phase := fmt.Sprintf("elimination_r%d", i+1)
			trackReschedule(phase, h.upsertSchedule(ctx, fmt.Sprintf("%s-elim-r%d", gameDayID, i+1), phase, gameDayID, t))
		}
	}

	gd, err := h.store.GetGameDay(ctx, gameDayID)
	if err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	if len(rescheduleFailures) > 0 {
		return jsonResp(http.StatusOK, struct {
			db.GameDay
			RescheduleFailures []string `json:"rescheduleFailures"`
		}{gd, rescheduleFailures}), nil
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
	now := time.Now().Unix()
	if err := h.store.UpdateVersionCompile(ctx, tankID, version, db.CompileUpdate{
		Status:           "compiling",
		CompileStartedAt: now,
	}); err != nil {
		log.Printf("set compiling %s/%s: %v", tankID, version, err)
	}
	overrides := []cbtypes.EnvironmentVariable{
		{Name: aws.String("TANK_ID"), Value: aws.String(tankID), Type: cbtypes.EnvironmentVariableTypePlaintext},
		{Name: aws.String("VERSION"), Value: aws.String(version), Type: cbtypes.EnvironmentVariableTypePlaintext},
		{Name: aws.String("SOURCE_S3_KEY"), Value: aws.String(sourceKey), Type: cbtypes.EnvironmentVariableTypePlaintext},
		{Name: aws.String("OUTPUT_WASM_KEY"), Value: aws.String(wasmKey), Type: cbtypes.EnvironmentVariableTypePlaintext},
		{Name: aws.String("TANK_VERSIONS_TABLE"), Value: aws.String(h.versionsTable), Type: cbtypes.EnvironmentVariableTypePlaintext},
	}
	out, err := h.cb.StartBuild(ctx, &codebuild.StartBuildInput{
		ProjectName:                  aws.String(h.codebuildProject),
		EnvironmentVariablesOverride: overrides,
	})
	if err != nil {
		log.Printf("start codebuild %s/%s: %v", tankID, version, err)
		if updErr := h.store.UpdateVersionCompile(ctx, tankID, version, db.CompileUpdate{
			Status:       "failed",
			CompileError: "failed to start build",
		}); updErr != nil {
			log.Printf("revert compile status: %v", updErr)
		}
		return
	}
	// Store the build ID so getVersionStatus can check CodeBuild if the build
	// times out (post_build doesn't run on TIMED_OUT, leaving status stuck at "compiling").
	if out.Build != nil && out.Build.Id != nil {
		if updErr := h.store.UpdateVersionCompile(ctx, tankID, version, db.CompileUpdate{
			Status:           "compiling",
			BuildID:          aws.ToString(out.Build.Id),
			CompileStartedAt: now,
		}); updErr != nil {
			log.Printf("store build ID %s/%s: %v", tankID, version, updErr)
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

// identityProviderName extracts the first linked IdP's display name (e.g.
// "Google", "Facebook") from a Cognito "identities" attribute value, a JSON
// array like [{"providerName":"Google", ...}]. Native email+password
// accounts have no "identities" attribute at all. Duplicated from (rather
// than imported from) cmd/forgot-password-worker, which uses a different
// fallback label for its own purpose — see item 241.
func identityProviderName(identitiesJSON string) string {
	if identitiesJSON == "" {
		return "Email/Password"
	}
	var identities []struct {
		ProviderName string `json:"providerName"`
	}
	if err := json.Unmarshal([]byte(identitiesJSON), &identities); err != nil || len(identities) == 0 {
		return "Email/Password"
	}
	return identities[0].ProviderName
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
		tier := db.TierFree
		displayName := ""
		var lastLoginAt *int64
		if us, err := h.store.GetUserSettings(ctx, sub); err == nil {
			tier = us.Tier
			displayName = us.DisplayName
			if us.LastLoginAt != 0 {
				lastLoginAt = &us.LastLoginAt
			}
		}
		email := cognitoAttr(u.Attributes, "email")
		// Prefer UserSettings.DisplayName (item 225) — the only copy immune
		// to Google/Facebook's IdP attribute mapping re-syncing given_name on
		// every login — falling back to given_name (set directly for
		// non-federated accounts, or pre-fix federated ones) and then email.
		// Distinct from item 220, which is about the client not refreshing a
		// saved name.
		name := displayName
		if name == "" {
			name = cognitoAttr(u.Attributes, "given_name")
		}
		if name == "" {
			name = email
		}
		createdAt := ""
		if u.UserCreateDate != nil {
			createdAt = u.UserCreateDate.UTC().Format(time.RFC3339)
		}
		tankLimit, _ := db.TierLimits(tier)
		tankCount := 0
		if tanks, err := h.store.ListTanksByUser(ctx, sub); err == nil {
			tankCount = len(tanks)
		}
		users = append(users, adminUserResp{
			Sub:         sub,
			Email:       email,
			Name:        name,
			Enabled:     u.Enabled,
			IsAdmin:     adminSubs[sub],
			Tier:        tier,
			Idp:         identityProviderName(cognitoAttr(u.Attributes, "identities")),
			CreatedAt:   createdAt,
			LastLoginAt: lastLoginAt,
			TankCount:   tankCount,
			TankLimit:   tankLimit,
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
	// Item 218's AI-tank author override (getRankings) never extended to
	// this endpoint (item 231) — the admin tank table otherwise shows
	// whichever real Cognito account happens to own the seeded record.
	for i := range tanks {
		if h.isAiTankID(tanks[i].TankID) {
			tanks[i].AuthorName = aiTankAuthorName
		}
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

// adminResetCompile force-sets a tank version's compileStatus to "failed" so a
// user can re-submit. Used to unblock records stuck at "compiling" because
// CodeBuild timed out without running post_build.
func (h *handler) adminResetCompile(ctx context.Context, req events.APIGatewayV2HTTPRequest, tankID, version string) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	if _, err := h.store.GetVersion(ctx, tankID, version); errors.Is(err, db.ErrNotFound) {
		return errResp(http.StatusNotFound, "version not found"), nil
	}
	if err := h.store.UpdateVersionCompile(ctx, tankID, version, db.CompileUpdate{
		Status:       "failed",
		CompileError: "reset by admin",
	}); err != nil {
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]string{"status": "reset"}), nil
}

// ---- User settings handlers ---------------------------------------------------

func (h *handler) getMySettings(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	us, err := h.store.GetUserSettings(ctx, uid)
	if err != nil {
		log.Printf("getMySettings: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	us, _ = db.ResetWindowIfExpired(us)
	tankLimit, compileLimit := db.TierLimits(us.Tier)
	return jsonResp(http.StatusOK, map[string]interface{}{
		"tier":                   us.Tier,
		"compilationsThisWindow": us.CompilationsThisWindow,
		"windowStart":            us.WindowStart,
		"tankLimit":              tankLimit,
		"compilationLimit":       compileLimit,
	}), nil
}

func (h *handler) patchMySettings(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "admin only"), nil
	}
	var body patchMySettingsBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return errResp(http.StatusBadRequest, "invalid request body"), nil
	}
	targetUID := req.QueryStringParameters["userId"]
	if targetUID == "" {
		return errResp(http.StatusBadRequest, "userId query param required"), nil
	}
	switch body.Tier {
	case db.TierFree, db.TierBuilder, db.TierPro:
	default:
		return errResp(http.StatusBadRequest, "tier must be free, builder, or pro"), nil
	}
	us, err := h.store.GetUserSettings(ctx, targetUID)
	if err != nil {
		log.Printf("patchMySettings get: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	us.UserID = targetUID
	us.Tier = body.Tier
	if err := h.store.PutUserSettings(ctx, us); err != nil {
		log.Printf("patchMySettings put: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]string{"tier": us.Tier}), nil
}

// patchMyProfile updates the caller's display name. It writes three copies:
// the Cognito given_name attribute (kept for backward compatibility and as
// the JWT-visible value for non-federated accounts), the denormalized
// authorName copy on each of their tanks (same field the lazy-backfill in
// getTank populates from JWT claims when empty), and — since item 225 —
// UserSettings.DisplayName, which is the only one of the three that survives
// a federated (Google/Facebook) re-login: Cognito's IdP attribute mapping
// re-applies given_name from the provider on every sign-in, silently
// reverting it, and a user with no tanks yet has no authorName to fall back
// on either. DisplayName has neither problem, so it's now the source of
// truth read by getMyProfile, resolveUserDisplay, getPublicUserProfile, and
// adminListUsers. Email is immutable here by design.
func (h *handler) patchMyProfile(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	var body patchMyProfileBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return errResp(http.StatusBadRequest, "invalid request body"), nil
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		return errResp(http.StatusBadRequest, "name is required"), nil
	}

	username, err := h.getUsernameBySub(ctx, uid)
	if err != nil {
		return errResp(http.StatusNotFound, "user not found"), nil
	}
	_, err = h.cognito.AdminUpdateUserAttributes(ctx, &cognitoidp.AdminUpdateUserAttributesInput{
		UserPoolId: aws.String(h.userPoolID),
		Username:   aws.String(username),
		UserAttributes: []cognitotypes.AttributeType{
			{Name: aws.String("given_name"), Value: aws.String(name)},
		},
	})
	if err != nil {
		log.Printf("patchMyProfile update attrs %s: %v", uid, err)
		return errResp(http.StatusInternalServerError, "failed to update profile"), nil
	}

	us, err := h.store.GetUserSettings(ctx, uid)
	if err != nil {
		log.Printf("patchMyProfile get settings %s: %v", uid, err)
	} else {
		us.UserID = uid
		us.DisplayName = name
		if err := h.store.PutUserSettings(ctx, us); err != nil {
			log.Printf("patchMyProfile put settings %s: %v", uid, err)
		}
	}

	tanks, err := h.store.ListTanksByUser(ctx, uid)
	if err != nil {
		log.Printf("patchMyProfile list tanks %s: %v", uid, err)
	} else {
		for _, t := range tanks {
			if err := h.store.UpdateAuthorName(ctx, t.TankID, name); err != nil {
				log.Printf("patchMyProfile backfill authorName %s: %v", t.TankID, err)
			}
		}
	}

	return jsonResp(http.StatusOK, map[string]string{"name": name}), nil
}

// getMyProfile is GET /me/profile (item 225, avatar added item 229) —
// returns the caller's durable display name, preferring
// UserSettings.DisplayName (set by patchMyProfile, immune to federated IdP
// re-login resync) over the tanks' denormalized authorName (for names set
// before this fix shipped) over the JWT's own given_name/name/email claim
// (new users with neither yet set); and the caller's durable avatar,
// UserSettings.AvatarURL (set by uploadProfilePicture, immune to the same
// federated resync for Cognito's "picture" attribute), empty if never
// uploaded so the frontend falls back to the JWT's own picture claim.
func (h *handler) getMyProfile(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}
	name := ""
	picture := ""
	if us, err := h.store.GetUserSettings(ctx, uid); err == nil {
		name = us.DisplayName
		picture = us.AvatarURL
	}
	if name == "" {
		if tanks, err := h.store.ListTanksByUser(ctx, uid); err == nil {
			for _, t := range tanks {
				if t.AuthorName != "" {
					name = t.AuthorName
					break
				}
			}
		}
	}
	if name == "" {
		name = authorName(req)
	}
	return jsonResp(http.StatusOK, map[string]string{"name": name, "picture": picture}), nil
}

// uploadProfilePicture is PUT /me/profile/picture (item 198) — reuses item
// 158's upload pattern (JSON body of base64 data + contentType, same
// 512KB/PNG-JPEG validation, same S3 bucket) but under the user-avatars/
// prefix, and updates Cognito's picture attribute (mutable per auth-stack.ts)
// via AdminUpdateUserAttributes instead of a Tank record's avatarUrl.
func (h *handler) uploadProfilePicture(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	uid := userID(req)
	if uid == "" {
		return errResp(http.StatusUnauthorized, "unauthorized"), nil
	}

	var body uploadAvatarBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return errResp(http.StatusBadRequest, "invalid request body"), nil
	}

	var ext string
	switch body.ContentType {
	case "image/png":
		ext = "png"
	case "image/jpeg":
		ext = "jpg"
	default:
		return errResp(http.StatusBadRequest, "contentType must be image/png or image/jpeg"), nil
	}

	imgBytes, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		return errResp(http.StatusBadRequest, "data must be valid base64"), nil
	}
	if len(imgBytes) == 0 {
		return errResp(http.StatusBadRequest, "data is required"), nil
	}
	if len(imgBytes) > maxAvatarBytes {
		return errResp(http.StatusBadRequest, fmt.Sprintf("avatar must be %d bytes or fewer", maxAvatarBytes)), nil
	}

	key := fmt.Sprintf("user-avatars/%s/avatar.%s", uid, ext)
	if _, err := h.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(h.assetsBucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(imgBytes),
		ContentType: aws.String(body.ContentType),
	}); err != nil {
		log.Printf("upload profile picture %s: %v", uid, err)
		return errResp(http.StatusInternalServerError, "failed to upload picture"), nil
	}

	url := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", h.assetsBucket, h.region, key)

	username, err := h.getUsernameBySub(ctx, uid)
	if err != nil {
		return errResp(http.StatusNotFound, "user not found"), nil
	}
	_, err = h.cognito.AdminUpdateUserAttributes(ctx, &cognitoidp.AdminUpdateUserAttributesInput{
		UserPoolId: aws.String(h.userPoolID),
		Username:   aws.String(username),
		UserAttributes: []cognitotypes.AttributeType{
			{Name: aws.String("picture"), Value: aws.String(url)},
		},
	})
	if err != nil {
		log.Printf("uploadProfilePicture update attrs %s: %v", uid, err)
		return errResp(http.StatusInternalServerError, "failed to update profile"), nil
	}

	// Durable copy (item 229) — survives the next Google/Facebook re-login,
	// which otherwise re-syncs the Cognito "picture" attribute above back to
	// the provider's photo via auth-stack.ts's IdP attribute mapping.
	if us, err := h.store.GetUserSettings(ctx, uid); err != nil {
		log.Printf("uploadProfilePicture get settings %s: %v", uid, err)
	} else {
		us.UserID = uid
		us.AvatarURL = url
		if err := h.store.PutUserSettings(ctx, us); err != nil {
			log.Printf("uploadProfilePicture put settings %s: %v", uid, err)
		}
	}

	return jsonResp(http.StatusOK, map[string]string{"picture": url}), nil
}

// ---- Ad config handlers -------------------------------------------------------

// getAdConfig is the public endpoint GET /config/ads — no auth required.
func (h *handler) getAdConfig(ctx context.Context) (events.APIGatewayV2HTTPResponse, error) {
	cfg, err := h.store.GetAdConfig(ctx)
	if err != nil {
		log.Printf("getAdConfig: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]interface{}{
		"enabled":      cfg.Enabled,
		"publisherId":  cfg.PublisherID,
		"topSlotId":    cfg.TopSlotID,
		"rightSlotId":  cfg.RightSlotID,
		"bottomSlotId": cfg.BottomSlotID,
	}), nil
}

func (h *handler) adminGetAdConfig(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	cfg, err := h.store.GetAdConfig(ctx)
	if err != nil {
		log.Printf("adminGetAdConfig: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]interface{}{
		"enabled":      cfg.Enabled,
		"publisherId":  cfg.PublisherID,
		"topSlotId":    cfg.TopSlotID,
		"rightSlotId":  cfg.RightSlotID,
		"bottomSlotId": cfg.BottomSlotID,
	}), nil
}

type patchAdConfigBody struct {
	Enabled      *bool  `json:"enabled"`
	PublisherID  string `json:"publisherId"`
	TopSlotID    string `json:"topSlotId"`
	RightSlotID  string `json:"rightSlotId"`
	BottomSlotID string `json:"bottomSlotId"`
}

func (h *handler) adminPatchAdConfig(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !isAdmin(req) {
		return errResp(http.StatusForbidden, "forbidden"), nil
	}
	var body patchAdConfigBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return errResp(http.StatusBadRequest, "invalid request body"), nil
	}
	existing, err := h.store.GetAdConfig(ctx)
	if err != nil {
		log.Printf("adminPatchAdConfig get: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	// When enabled is present the client is sending a full config; replace all fields.
	if body.Enabled != nil {
		existing.Enabled = *body.Enabled
		existing.PublisherID = body.PublisherID
		existing.TopSlotID = body.TopSlotID
		existing.RightSlotID = body.RightSlotID
		existing.BottomSlotID = body.BottomSlotID
	} else {
		if body.PublisherID != "" {
			existing.PublisherID = body.PublisherID
		}
		if body.TopSlotID != "" {
			existing.TopSlotID = body.TopSlotID
		}
		if body.RightSlotID != "" {
			existing.RightSlotID = body.RightSlotID
		}
		if body.BottomSlotID != "" {
			existing.BottomSlotID = body.BottomSlotID
		}
	}
	if err := h.store.PutAdConfig(ctx, existing); err != nil {
		log.Printf("adminPatchAdConfig put: %v", err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	return jsonResp(http.StatusOK, map[string]string{"status": "ok"}), nil
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
