// localserver is a self-contained HTTP + WebSocket server for local GUI testing.
// It replaces AWS (DynamoDB, S3, Lambda, CodeBuild, Cognito) with in-memory
// implementations so the full frontend can be exercised without any cloud infra.
//
// Usage:
//
//	go run ./cmd/localserver           # listen on :8080
//	go run ./cmd/localserver -port 9090
//
// Set the following in packages/frontend/.env.local:
//
//	VITE_API_ENDPOINT=http://localhost:8080
//	VITE_WS_ENDPOINT=ws://localhost:8080/ws
//	VITE_USER_POOL_ID=local
//	VITE_USER_POOL_CLIENT_ID=local
//	VITE_LOCAL_DEV=true
package main

import (
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tankmaze/backend/internal/db"
)

const (
	localUserID = "local-user"
	aiUserID    = "__ai__"
	maxSrcBytes = 1 * 1024 * 1024
	maxNameLen  = 64
)

// server holds all shared mutable state.
type server struct {
	store       *memStore
	mu          sync.RWMutex
	wasmData    map[string][]byte    // wasmKey → WASM bytes
	srcData     map[string][]byte    // sourceKey → source bytes
	liveMatches map[string]*liveMatch
}

func newServer() *server {
	return &server{
		store:       newStore(),
		wasmData:    make(map[string][]byte),
		srcData:     make(map[string][]byte),
		liveMatches: make(map[string]*liveMatch),
	}
}

func (srv *server) getWasm(key string) []byte {
	srv.mu.RLock()
	defer srv.mu.RUnlock()
	return srv.wasmData[key]
}

func (srv *server) setWasm(key string, data []byte) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	srv.wasmData[key] = data
}

// ── Main ───────────────────────────────────────────────────────────────────

func main() {
	port := flag.String("port", "8080", "HTTP listen port")
	flag.Parse()

	srv := newServer()

	log.Println("Compiling AI tanks…")
	for _, name := range []string{"scout", "bruiser"} {
		if err := srv.compileAITank(name); err != nil {
			log.Printf("  WARNING: failed to compile %s: %v", name, err)
			log.Printf("  (AI opponent %q will be unavailable)", name)
		} else {
			log.Printf("  compiled %s OK", name)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.handleWS)
	mux.HandleFunc("/", srv.route)

	addr := ":" + *port
	log.Printf("Local dev server listening on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, cors(mux)))
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── Router ─────────────────────────────────────────────────────────────────

func (srv *server) route(w http.ResponseWriter, r *http.Request) {
	method := r.Method
	rawPath := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(rawPath, "/")
	n := len(parts)

	switch {
	// Tanks
	case method == "GET" && rawPath == "tanks":
		srv.listTanks(w, r)
	case method == "POST" && rawPath == "tanks":
		srv.createTank(w, r)
	case method == "GET" && n == 2 && parts[0] == "tanks":
		srv.getTank(w, r, parts[1])
	case method == "POST" && n == 3 && parts[0] == "tanks" && parts[2] == "versions":
		srv.submitVersion(w, r, parts[1])
	case method == "GET" && n == 5 && parts[0] == "tanks" && parts[2] == "versions" && parts[4] == "status":
		srv.getVersionStatus(w, r, parts[1], parts[3])
	case method == "POST" && n == 5 && parts[0] == "tanks" && parts[2] == "versions" && parts[4] == "promote":
		srv.promoteVersion(w, r, parts[1], parts[3])
	case method == "POST" && n == 5 && parts[0] == "tanks" && parts[2] == "versions" && parts[4] == "register":
		srv.registerVersion(w, r, parts[1], parts[3])
	case method == "DELETE" && n == 5 && parts[0] == "tanks" && parts[2] == "versions" && parts[4] == "register":
		srv.deregisterVersion(w, r, parts[1], parts[3])
	case method == "POST" && n == 3 && parts[0] == "tanks" && parts[2] == "score-transfer":
		srv.scoreTransfer(w, r, parts[1])

	// Matches
	case method == "POST" && rawPath == "matches":
		srv.startMatch(w, r)
	case method == "GET" && n == 2 && parts[0] == "matches":
		srv.getMatch(w, r, parts[1])
	case method == "GET" && n == 3 && parts[0] == "matches" && parts[2] == "ticks":
		srv.getMatchTicks(w, r, parts[1])

	// Rankings / Game Days
	case method == "GET" && rawPath == "rankings":
		srv.getRankings(w, r)
	case method == "GET" && n == 2 && parts[0] == "gamedays":
		srv.getGameDay(w, r, parts[1])

	// Maps
	case method == "GET" && rawPath == "maps":
		srv.listMaps(w, r)
	case method == "POST" && rawPath == "maps":
		srv.createMap(w, r)
	case method == "PATCH" && n == 2 && parts[0] == "maps":
		srv.updateMap(w, r, parts[1])

	default:
		jsonErr(w, http.StatusNotFound, "not found")
	}
}

// ── Tank handlers ──────────────────────────────────────────────────────────

func (srv *server) listTanks(w http.ResponseWriter, _ *http.Request) {
	tanks := srv.store.listTanksByUser(localUserID)
	if tanks == nil {
		tanks = []db.Tank{}
	}
	jsonOK(w, tanks)
}

func (srv *server) createTank(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		body.Name = "My Tank"
	}
	if len(body.Name) > maxNameLen {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("name must be %d chars or fewer", maxNameLen))
		return
	}

	forkFrom := r.URL.Query().Get("forkFrom")
	forkVersion := r.URL.Query().Get("forkVersion")

	tankID := newUUID()
	now := time.Now().Unix()
	tank := db.Tank{
		TankID:            tankID,
		UserID:            localUserID,
		Name:              body.Name,
		LastActiveAt:      now,
		CreatedAt:         now,
		ForkedFromTankID:  forkFrom,
		ForkedFromVersion: forkVersion,
	}
	srv.store.putTank(tank)

	// If forking: copy source + WASM and create initial version.
	if forkFrom != "" && forkVersion != "" {
		srcVer, err := srv.store.getVersion(forkFrom, forkVersion)
		if err == nil && srcVer.CompileStatus == "ready" {
			newKey := fmt.Sprintf("%s/v0.1/tank.wasm", tankID)
			newSrcKey := fmt.Sprintf("%s/v0.1/source.go", tankID)
			if wasmBytes := srv.getWasm(srcVer.WasmS3Key); len(wasmBytes) > 0 {
				srv.setWasm(newKey, wasmBytes)
			}
			if srcBytes := srv.srcData[srcVer.SourceS3Key]; len(srcBytes) > 0 {
				srv.mu.Lock()
				srv.srcData[newSrcKey] = srcBytes
				srv.mu.Unlock()
			}
			srv.store.putVersion(db.TankVersion{
				TankID:        tankID,
				Version:       "v0.1",
				VersionType:   "minor",
				Config:        srcVer.Config,
				WasmS3Key:     newKey,
				SourceS3Key:   newSrcKey,
				WasmSHA256:    srcVer.WasmSHA256,
				CompileStatus: "ready",
				CreatedAt:     now,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"tankId": tankID, "name": body.Name})
}

func (srv *server) getTank(w http.ResponseWriter, _ *http.Request, tankID string) {
	tank, err := srv.store.getTank(tankID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "tank not found")
		return
	}
	versions := srv.store.listVersionsByTank(tankID)
	if versions == nil {
		versions = []db.TankVersion{}
	}
	jsonOK(w, map[string]any{"tank": tank, "versions": versions})
}

// ── Version handlers ────────────────────────────────────────────────────────

type submitVersionBody struct {
	Source string          `json:"source"`
	Config db.VersionConfig `json:"config"`
}

func (srv *server) submitVersion(w http.ResponseWriter, r *http.Request, tankID string) {
	if _, err := srv.store.getTank(tankID); errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "tank not found")
		return
	}

	var body submitVersionBody
	if err := readJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(body.Source) == "" {
		jsonErr(w, http.StatusBadRequest, "source is required")
		return
	}
	if len(body.Source) > maxSrcBytes {
		jsonErr(w, http.StatusBadRequest, "source too large")
		return
	}

	versions := srv.store.listVersionsByTank(tankID)
	nextVer := nextMinorVersion(versions)
	sourceKey := fmt.Sprintf("%s/%s/source.go", tankID, nextVer)

	srv.mu.Lock()
	srv.srcData[sourceKey] = []byte(body.Source)
	srv.mu.Unlock()

	ver := db.TankVersion{
		TankID:        tankID,
		Version:       nextVer,
		VersionType:   "minor",
		Config:        body.Config,
		SourceS3Key:   sourceKey,
		CompileStatus: "compiling",
		CreatedAt:     time.Now().Unix(),
	}
	srv.store.putVersion(ver)

	go func() {
		wasmBytes, sha256hex, err := srv.compileWasm(body.Source)
		if err != nil {
			srv.store.updateVersionCompile(tankID, nextVer, db.CompileUpdate{
				Status:       "failed",
				CompileError: err.Error(),
			})
			return
		}
		wasmKey := fmt.Sprintf("%s/%s/tank.wasm", tankID, nextVer)
		srv.setWasm(wasmKey, wasmBytes)
		srv.store.updateVersionCompile(tankID, nextVer, db.CompileUpdate{
			Status:    "ready",
			WasmS3Key: wasmKey,
			WasmSHA256: sha256hex,
		})
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"version": nextVer})
}

func (srv *server) getVersionStatus(w http.ResponseWriter, _ *http.Request, tankID, version string) {
	ver, err := srv.store.getVersion(tankID, version)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "version not found")
		return
	}
	jsonOK(w, map[string]any{
		"version":       ver.Version,
		"compileStatus": ver.CompileStatus,
		"compileError":  ver.CompileError,
	})
}

func (srv *server) promoteVersion(w http.ResponseWriter, _ *http.Request, tankID, version string) {
	ver, err := srv.store.getVersion(tankID, version)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "version not found")
		return
	}
	if ver.VersionType != "minor" {
		jsonErr(w, http.StatusBadRequest, "only minor versions can be promoted")
		return
	}
	if ver.CompileStatus != "ready" {
		jsonErr(w, http.StatusBadRequest, "version must be ready before promotion")
		return
	}

	versions := srv.store.listVersionsByTank(tankID)
	newMajor := nextMajorVersion(versions)
	now := time.Now().Unix()

	srv.store.putVersion(db.TankVersion{
		TankID:        tankID,
		Version:       newMajor,
		VersionType:   "major",
		Config:        ver.Config,
		WasmS3Key:     ver.WasmS3Key,
		SourceS3Key:   ver.SourceS3Key,
		WasmSHA256:    ver.WasmSHA256,
		CompileStatus: "ready",
		CreatedAt:     now,
	})

	// Copy WASM bytes under the new key.
	if newKey := fmt.Sprintf("%s/%s/tank.wasm", tankID, newMajor); ver.WasmS3Key != "" {
		if data := srv.getWasm(ver.WasmS3Key); len(data) > 0 {
			srv.setWasm(newKey, data)
		}
	}

	// Set tank.CreatedAt on first promotion.
	if t, err := srv.store.getTank(tankID); err == nil && t.CreatedAt == 0 {
		t.CreatedAt = now
		srv.store.putTank(t)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"version": newMajor})
}

func (srv *server) registerVersion(w http.ResponseWriter, r *http.Request, tankID, version string) {
	ver, err := srv.store.getVersion(tankID, version)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "version not found")
		return
	}
	if ver.VersionType != "major" {
		jsonErr(w, http.StatusBadRequest, "only major versions can register for Game Days")
		return
	}
	var body struct {
		GameDayID string `json:"gameDayId"`
	}
	if err := readJSON(r, &body); err != nil || body.GameDayID == "" {
		jsonErr(w, http.StatusBadRequest, "gameDayId is required")
		return
	}
	srv.store.updateVersionRegistration(tankID, version, body.GameDayID)
	jsonOK(w, map[string]string{"gameDayId": body.GameDayID})
}

func (srv *server) deregisterVersion(w http.ResponseWriter, _ *http.Request, tankID, version string) {
	srv.store.updateVersionRegistration(tankID, version, "")
	jsonOK(w, map[string]bool{"deregistered": true})
}

// ── Score transfer ──────────────────────────────────────────────────────────

func (srv *server) scoreTransfer(w http.ResponseWriter, r *http.Request, tankID string) {
	srcTank, err := srv.store.getTank(tankID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "source tank not found")
		return
	}
	if srcTank.ScoreTransferredTo != "" {
		jsonErr(w, http.StatusConflict, "score already transferred")
		return
	}
	var body struct {
		TargetTankID string `json:"targetTankId"`
	}
	if err := readJSON(r, &body); err != nil || body.TargetTankID == "" {
		jsonErr(w, http.StatusBadRequest, "targetTankId is required")
		return
	}
	if body.TargetTankID == tankID {
		jsonErr(w, http.StatusBadRequest, "source and target must differ")
		return
	}
	rankings := srv.store.listRankingsByTank(tankID)
	srv.store.scoreTransfer(db.ScoreTransferInput{
		SourceTankID:   tankID,
		TargetTankID:   body.TargetTankID,
		SourceRankings: rankings,
		GlobalScore:    srcTank.GlobalScore,
		BestFinish:     srcTank.BestFinish,
		GameDaysCount:  srcTank.GameDaysCount,
		LastActiveAt:   srcTank.LastActiveAt,
	})
	jsonOK(w, map[string]bool{"transferred": true})
}

// ── Match handlers ──────────────────────────────────────────────────────────

type opponentSpec struct {
	Type    string `json:"type"`
	Name    string `json:"name,omitempty"`
	TankID  string `json:"tankId,omitempty"`
	Version string `json:"version,omitempty"`
}

type startMatchBody struct {
	TankID   string       `json:"tankId"`
	Version  string       `json:"version"`
	Opponent opponentSpec `json:"opponent"`
	MapID    string       `json:"mapId,omitempty"`
}

func (srv *server) startMatch(w http.ResponseWriter, r *http.Request) {
	var body startMatchBody
	if err := readJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.TankID == "" || body.Version == "" {
		jsonErr(w, http.StatusBadRequest, "tankId and version are required")
		return
	}

	ver, err := srv.store.getVersion(body.TankID, body.Version)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "version not found")
		return
	}
	if ver.CompileStatus != "ready" {
		jsonErr(w, http.StatusBadRequest, "version is not ready")
		return
	}

	var oppTankID, oppVersion, matchType string
	switch body.Opponent.Type {
	case "ai", "": // default to ai
		aiName := body.Opponent.Name
		if aiName == "" {
			aiName = "scout"
		}
		switch aiName {
		case "scout":
			oppTankID = "__scout__"
		case "bruiser":
			oppTankID = "__bruiser__"
		case "ranger": // fallback to bruiser if ranger not available
			oppTankID = "__bruiser__"
		default:
			jsonErr(w, http.StatusBadRequest, "unknown AI: use scout or bruiser")
			return
		}
		oppVersion = "v1"
		matchType = "test-ai"

		if srv.getWasm(fmt.Sprintf("__ai__/%s/tank.wasm", strings.TrimPrefix(strings.TrimSuffix(oppTankID, "__"), "__"))) == nil {
			jsonErr(w, http.StatusServiceUnavailable, "AI opponent not compiled; restart server")
			return
		}

	case "own":
		if body.Opponent.TankID == "" || body.Opponent.Version == "" {
			jsonErr(w, http.StatusBadRequest, "opponent tankId and version required for type=own")
			return
		}
		oppVer, err := srv.store.getVersion(body.Opponent.TankID, body.Opponent.Version)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "opponent version not found")
			return
		}
		if oppVer.CompileStatus != "ready" {
			jsonErr(w, http.StatusBadRequest, "opponent version is not ready")
			return
		}
		oppTankID = body.Opponent.TankID
		oppVersion = body.Opponent.Version
		matchType = "test-own"

	default:
		jsonErr(w, http.StatusBadRequest, "opponent type must be 'ai' or 'own'")
		return
	}

	var mazeSeed, mapID string
	if body.MapID != "" {
		if _, err := srv.store.getMapByID(body.MapID); errors.Is(err, db.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "map not found")
			return
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
	}
	srv.store.putMatch(match)

	go srv.runMatch(matchID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"matchId": matchID})
}

func (srv *server) getMatch(w http.ResponseWriter, _ *http.Request, matchID string) {
	m, err := srv.store.getMatch(matchID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "match not found")
		return
	}
	jsonOK(w, m)
}

func (srv *server) getMatchTicks(w http.ResponseWriter, _ *http.Request, matchID string) {
	// In local mode we stream ticks via WebSocket; no tick-log download needed.
	// Return the minimal info so the frontend doesn't break on 404.
	m, err := srv.store.getMatch(matchID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "match not found")
		return
	}
	if m.Status != "ended" {
		jsonErr(w, http.StatusNotFound, "match not ended yet")
		return
	}
	jsonOK(w, map[string]string{"matchId": matchID, "note": "use WebSocket for ticks in local mode"})
}

// ── Rankings / Game Days ────────────────────────────────────────────────────

func (srv *server) getRankings(w http.ResponseWriter, _ *http.Request) {
	tanks := srv.store.scanTanksByScore(localUserID)
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
	jsonOK(w, result)
}

func (srv *server) getGameDay(w http.ResponseWriter, _ *http.Request, _ string) {
	// No game days in local mode.
	jsonErr(w, http.StatusNotFound, "no game days in local dev mode")
}

// ── Map handlers ────────────────────────────────────────────────────────────

func (srv *server) listMaps(w http.ResponseWriter, _ *http.Request) {
	maps := srv.store.listActiveMaps()
	if maps == nil {
		maps = []db.Map{}
	}
	jsonOK(w, maps)
}

func (srv *server) createMap(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug        string   `json:"slug"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Layout      [][]bool `json:"layout"`
	}
	if err := readJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Slug == "" || body.Name == "" || body.Layout == nil {
		jsonErr(w, http.StatusBadRequest, "slug, name, and layout are required")
		return
	}
	if len(body.Layout) < 5 || len(body.Layout[0]) < 5 {
		jsonErr(w, http.StatusBadRequest, "layout must be at least 5x5")
		return
	}
	if _, err := srv.store.getMapBySlug(body.Slug); err == nil {
		jsonErr(w, http.StatusConflict, "slug already in use")
		return
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
	srv.store.putMap(m)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(m)
}

func (srv *server) updateMap(w http.ResponseWriter, r *http.Request, mapID string) {
	existing, err := srv.store.getMapByID(mapID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "map not found")
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		IsActive    *bool  `json:"isActive"`
	}
	if err := readJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
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
	srv.store.updateMap(mapID, name, description, isActive)
	jsonOK(w, map[string]any{"mapId": mapID, "name": name, "description": description, "isActive": isActive})
}

// ── Helpers ────────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	err := json.NewDecoder(r.Body).Decode(v)
	if errors.Is(err, io.EOF) {
		return nil // empty body is fine
	}
	return err
}

func newUUID() string {
	var b [16]byte
	_, _ = crand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

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
