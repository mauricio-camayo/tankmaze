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
	"encoding/base64"
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
	store             *memStore
	port              string
	mu                sync.RWMutex
	wasmData          map[string][]byte // wasmKey → WASM bytes
	srcData           map[string][]byte // sourceKey → source bytes
	avatarData        map[string][]byte // tankId → uploaded avatar image bytes
	avatarContentType map[string]string // tankId → "image/png" | "image/jpeg"
	userAvatarData    []byte            // uploaded profile picture bytes (single local user)
	userAvatarType    string            // "image/png" | "image/jpeg"
	liveMatches       map[string]*liveMatch
	adConfig          adConfigState
	userSettings      userSettingsState
}

type userSettingsState struct {
	Tier                   string `json:"tier"`
	CompilationsThisWindow int    `json:"compilationsThisWindow"`
	WindowStart            string `json:"windowStart"`
}

type adConfigState struct {
	Enabled      bool   `json:"enabled"`
	PublisherID  string `json:"publisherId"`
	TopSlotID    string `json:"topSlotId"`
	RightSlotID  string `json:"rightSlotId"`
	BottomSlotID string `json:"bottomSlotId"`
}

func newServer() *server {
	return &server{
		store:             newStore(),
		wasmData:          make(map[string][]byte),
		srcData:           make(map[string][]byte),
		avatarData:        make(map[string][]byte),
		avatarContentType: make(map[string]string),
		liveMatches:       make(map[string]*liveMatch),
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
	srv.port = *port

	log.Println("Compiling AI tanks…")
	for _, name := range []string{"scout", "bruiser", "ranger", "randy"} {
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
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
	case method == "GET" && rawPath == "tanks/ai":
		srv.getAiTanks(w)
	case method == "GET" && n == 2 && parts[0] == "tanks":
		srv.getTank(w, r, parts[1])
	case method == "DELETE" && n == 2 && parts[0] == "tanks":
		srv.deleteTank(w, r, parts[1])
	case method == "PATCH" && n == 2 && parts[0] == "tanks":
		srv.updateTank(w, r, parts[1])
	case method == "PUT" && n == 3 && parts[0] == "tanks" && parts[2] == "avatar":
		srv.uploadTankAvatar(w, r, parts[1])
	case method == "GET" && n == 3 && parts[0] == "local-assets":
		srv.serveLocalAsset(w, parts[1], parts[2])
	case method == "POST" && n == 3 && parts[0] == "tanks" && parts[2] == "versions":
		srv.submitVersion(w, r, parts[1])
	case method == "GET" && n == 5 && parts[0] == "tanks" && parts[2] == "versions" && parts[4] == "status":
		srv.getVersionStatus(w, r, parts[1], parts[3])
	case method == "GET" && n == 5 && parts[0] == "tanks" && parts[2] == "versions" && parts[4] == "source":
		srv.getVersionSource(w, r, parts[1], parts[3])
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

	// Auth
	case method == "POST" && rawPath == "auth/forgot-password":
		srv.forgotPassword(w, r)

	// Friends
	case method == "GET" && rawPath == "friends":
		srv.listFriends(w)
	case method == "POST" && rawPath == "friends/requests":
		srv.sendFriendRequest(w, r)
	case method == "POST" && n == 4 && parts[0] == "friends" && parts[1] == "requests" && parts[3] == "accept":
		srv.respondFriendRequest(w, parts[2], true)
	case method == "POST" && n == 4 && parts[0] == "friends" && parts[1] == "requests" && parts[3] == "reject":
		srv.respondFriendRequest(w, parts[2], false)
	case method == "DELETE" && n == 2 && parts[0] == "friends":
		srv.removeFriend(w, parts[1])

	// Rankings / Game Days
	case method == "GET" && rawPath == "rankings":
		srv.getRankings(w, r)
	case method == "GET" && n == 2 && parts[0] == "users":
		srv.getPublicUserProfile(w, parts[1])
	case method == "GET" && rawPath == "gamedays":
		srv.listGameDays(w)
	case method == "POST" && rawPath == "gamedays":
		srv.createGameDay(w, r)
	case method == "DELETE" && n == 2 && parts[0] == "gamedays":
		srv.deleteGameDay(w, r, parts[1])
	case method == "GET" && n == 2 && parts[0] == "gamedays":
		srv.getGameDay(w, r, parts[1])
	case method == "PATCH" && n == 2 && parts[0] == "gamedays":
		srv.patchGameDay(w, r, parts[1])
	case method == "POST" && n == 3 && parts[0] == "gamedays" && parts[2] == "roster":
		srv.addRosterEntry(w, r, parts[1])
	case method == "DELETE" && n == 4 && parts[0] == "gamedays" && parts[2] == "roster":
		srv.removeRosterEntry(w, parts[1], parts[3])

	// Maps
	case method == "GET" && rawPath == "maps":
		srv.listMaps(w, r)
	case method == "POST" && rawPath == "maps":
		srv.createMap(w, r)
	case method == "PATCH" && n == 2 && parts[0] == "maps":
		srv.updateMap(w, r, parts[1])

	// User settings
	case method == "GET" && rawPath == "me/settings":
		srv.getMySettings(w)
	case method == "PATCH" && rawPath == "me/settings":
		srv.patchMySettings(w, r)

	// User profile
	case method == "PATCH" && rawPath == "me/profile":
		srv.patchMyProfile(w, r)
	case method == "PUT" && rawPath == "me/profile/picture":
		srv.uploadProfilePicture(w, r)
	case method == "GET" && n == 2 && parts[0] == "local-user-assets":
		srv.serveLocalUserAsset(w, parts[1])

	// Ad config
	case method == "GET" && rawPath == "config/ads":
		srv.getAdConfig(w)
	case method == "GET" && rawPath == "admin/config/ads":
		srv.getAdConfig(w)
	case method == "PATCH" && rawPath == "admin/config/ads":
		srv.patchAdConfig(w, r)

	// Admin
	case method == "GET" && rawPath == "admin/users":
		srv.adminListUsers(w)
	case method == "PATCH" && n == 3 && parts[0] == "admin" && parts[1] == "users":
		srv.adminUpdateUser(w, r, parts[2])
	case method == "PATCH" && n == 4 && parts[0] == "admin" && parts[1] == "users" && parts[3] == "role":
		srv.adminToggleUserRole(w, r, parts[2])
	case method == "DELETE" && n == 3 && parts[0] == "admin" && parts[1] == "users":
		srv.adminDeleteUser(w, r, parts[2])
	case method == "GET" && rawPath == "admin/tanks":
		srv.adminListTanks(w, r)
	case method == "PATCH" && n == 3 && parts[0] == "admin" && parts[1] == "tanks":
		srv.adminUpdateTank(w, r, parts[2])
	case method == "DELETE" && n == 3 && parts[0] == "admin" && parts[1] == "tanks":
		srv.adminDeleteTank(w, r, parts[2])
	case method == "POST" && n == 6 && parts[0] == "admin" && parts[1] == "tanks" && parts[3] == "versions" && parts[5] == "reset-compile":
		srv.adminResetCompile(w, r, parts[2], parts[4])

	default:
		jsonErr(w, http.StatusNotFound, "not found")
	}
}

// ── Tank handlers ──────────────────────────────────────────────────────────

func (srv *server) getAiTanks(w http.ResponseWriter) {
	type aiTankResponse struct {
		db.Tank
		Versions []db.TankVersion `json:"versions"`
	}
	results := make([]aiTankResponse, 0, 2)
	for _, id := range []string{"__scout__", "__bruiser__", "__ranger__", "__randy__"} {
		tank, err := srv.store.getTank(id)
		if err != nil {
			continue
		}
		versions := srv.store.listVersionsByTank(id)
		if versions == nil {
			versions = []db.TankVersion{}
		}
		results = append(results, aiTankResponse{Tank: tank, Versions: versions})
	}
	jsonOK(w, results)
}

func (srv *server) listTanks(w http.ResponseWriter, r *http.Request) {
	uid := localUserID
	if viewUserID := r.URL.Query().Get("userId"); viewUserID != "" {
		uid = viewUserID
	}
	tanks := srv.store.listTanksByUser(uid)
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

	// Enforce tank limit.
	srv.mu.RLock()
	tier := srv.userSettings.Tier
	if tier == "" {
		tier = db.TierFree
	}
	srv.mu.RUnlock()
	tankLimit, _ := db.TierLimits(tier)
	existing := srv.store.listTanksByUser(localUserID)
	if len(existing) >= tankLimit {
		jsonErr(w, http.StatusForbidden, fmt.Sprintf("tank limit reached (%d/%d for %s tier)", len(existing), tankLimit, tier))
		return
	}

	tankID := newUUID()
	now := time.Now().Unix()
	var forkAvatarURL string
	if forkFrom != "" {
		if srcTank, err := srv.store.getTank(forkFrom); err == nil {
			forkAvatarURL = srcTank.AvatarURL
		}
	}
	tank := db.Tank{
		TankID:            tankID,
		UserID:            localUserID,
		Name:              body.Name,
		LastActiveAt:      now,
		CreatedAt:         now,
		ForkedFromTankID:  forkFrom,
		ForkedFromVersion: forkVersion,
		AvatarURL:         forkAvatarURL,
	}
	srv.store.putTank(tank)

	// If forking: copy (or convert) source, then compile into initial version.
	if forkFrom != "" && forkVersion != "" {
		srcVer, err := srv.store.getVersion(forkFrom, forkVersion)
		if err == nil && srcVer.CompileStatus == "ready" {
			newSrcKey := fmt.Sprintf("%s/v0.1/source.go", tankID)

			// Determine the source to write.  AI tanks (userId == "__ai__" or
			// tankId starts with "builtin-") carry full package main files that
			// must be converted to body-only dot-import style before the user
			// edits them.
			srcTank, _ := srv.store.getTank(forkFrom)
			var srcBytes []byte
			if srcTank.UserID == aiUserID || strings.HasPrefix(forkFrom, "builtin-") {
				if raw := srv.srcData[srcVer.SourceS3Key]; len(raw) > 0 {
					srcBytes = []byte(convertAISource(string(raw)))
				}
			} else {
				srv.mu.RLock()
				srcBytes = srv.srcData[srcVer.SourceS3Key]
				srv.mu.RUnlock()
			}

			if len(srcBytes) > 0 {
				srv.mu.Lock()
				srv.srcData[newSrcKey] = srcBytes
				srv.mu.Unlock()
			}

			ver := db.TankVersion{
				TankID:        tankID,
				Version:       "v0.1",
				VersionType:   "minor",
				Config:        srcVer.Config,
				SourceS3Key:   newSrcKey,
				CompileStatus: "no_source",
				CreatedAt:     now,
			}
			srv.store.putVersion(ver)
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
	type getTankResp struct {
		db.Tank
		Versions []db.TankVersion `json:"versions"`
	}
	jsonOK(w, getTankResp{Tank: tank, Versions: versions})
}

func (srv *server) updateTank(w http.ResponseWriter, r *http.Request, tankID string) {
	t, err := srv.store.getTank(tankID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "tank not found")
		return
	}
	var body struct {
		Name      string  `json:"name"`
		AvatarURL *string `json:"avatarUrl,omitempty"`
	}
	if err := readJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" && body.AvatarURL == nil {
		jsonErr(w, http.StatusBadRequest, "name or avatarUrl is required")
		return
	}
	if body.Name != "" {
		if len(body.Name) > maxNameLen {
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("name must be %d chars or fewer", maxNameLen))
			return
		}
		t.Name = body.Name
	}
	if body.AvatarURL != nil {
		t.AvatarURL = *body.AvatarURL
	}
	srv.store.putTank(t)
	jsonOK(w, map[string]string{"name": t.Name})
}

const maxAvatarBytes = 512 * 1024

// uploadTankAvatar mirrors tank-api's PUT /tanks/{id}/avatar (item 158), but
// stores the image bytes in memory and serves them back via
// GET /local-assets/{tankId}/avatar.{ext} instead of S3.
func (srv *server) uploadTankAvatar(w http.ResponseWriter, r *http.Request, tankID string) {
	t, err := srv.store.getTank(tankID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "tank not found")
		return
	}
	var body struct {
		Data        string `json:"data"`
		ContentType string `json:"contentType"`
	}
	if err := readJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var ext string
	switch body.ContentType {
	case "image/png":
		ext = "png"
	case "image/jpeg":
		ext = "jpg"
	default:
		jsonErr(w, http.StatusBadRequest, "contentType must be image/png or image/jpeg")
		return
	}
	imgBytes, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "data must be valid base64")
		return
	}
	if len(imgBytes) == 0 {
		jsonErr(w, http.StatusBadRequest, "data is required")
		return
	}
	if len(imgBytes) > maxAvatarBytes {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("avatar must be %d bytes or fewer", maxAvatarBytes))
		return
	}

	srv.mu.Lock()
	srv.avatarData[tankID] = imgBytes
	srv.avatarContentType[tankID] = body.ContentType
	srv.mu.Unlock()

	url := fmt.Sprintf("http://localhost:%s/local-assets/%s/avatar.%s", srv.port, tankID, ext)
	t.AvatarURL = url
	srv.store.putTank(t)
	jsonOK(w, map[string]string{"avatarUrl": url})
}

func (srv *server) serveLocalAsset(w http.ResponseWriter, tankID, filename string) {
	srv.mu.RLock()
	data, ok := srv.avatarData[tankID]
	contentType := srv.avatarContentType[tankID]
	srv.mu.RUnlock()
	if !ok || !strings.HasPrefix(filename, "avatar.") {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Write(data)
}

func (srv *server) deleteTank(w http.ResponseWriter, _ *http.Request, tankID string) {
	if _, err := srv.store.getTank(tankID); errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "tank not found")
		return
	}
	versions := srv.store.listVersionsByTank(tankID)
	for _, v := range versions {
		if len(v.RegisteredForGameDays) > 0 {
			jsonErr(w, http.StatusConflict, "tank is registered for a game day and cannot be deleted")
			return
		}
	}
	srv.store.deleteTank(tankID)
	w.WriteHeader(http.StatusNoContent)
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

	// Enforce compilation quota before starting the build.
	srv.mu.Lock()
	cTier := srv.userSettings.Tier
	if cTier == "" {
		cTier = db.TierFree
	}
	// Lazy window reset.
	if srv.userSettings.WindowStart != "" {
		if t, err2 := time.Parse(time.RFC3339, srv.userSettings.WindowStart); err2 == nil {
			if time.Since(t) >= 30*24*time.Hour {
				srv.userSettings.CompilationsThisWindow = 0
				srv.userSettings.WindowStart = ""
			}
		}
	}
	_, compileLimit := db.TierLimits(cTier)
	if srv.userSettings.CompilationsThisWindow >= compileLimit {
		srv.mu.Unlock()
		jsonErr(w, http.StatusTooManyRequests, fmt.Sprintf("compilation limit reached (%d/%d for %s tier)", srv.userSettings.CompilationsThisWindow, compileLimit, cTier))
		return
	}
	if srv.userSettings.WindowStart == "" {
		srv.userSettings.WindowStart = time.Now().UTC().Format(time.RFC3339)
	}
	srv.userSettings.CompilationsThisWindow++
	srv.mu.Unlock()

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

func (srv *server) getVersionSource(w http.ResponseWriter, _ *http.Request, tankID, version string) {
	ver, err := srv.store.getVersion(tankID, version)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "version not found")
		return
	}
	srv.mu.RLock()
	srcBytes := srv.srcData[ver.SourceS3Key]
	srv.mu.RUnlock()
	if len(srcBytes) == 0 {
		jsonErr(w, http.StatusNotFound, "source not available")
		return
	}
	jsonOK(w, map[string]string{"source": string(srcBytes)})
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
	if ver.Disqualified {
		jsonErr(w, http.StatusUnprocessableEntity, "this version is disqualified and cannot register for Game Days")
		return
	}
	tank, err := srv.store.getTank(tankID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "tank not found")
		return
	}
	var body struct {
		GameDayID string `json:"gameDayId"`
	}
	if err := readJSON(r, &body); err != nil || body.GameDayID == "" {
		jsonErr(w, http.StatusBadRequest, "gameDayId is required")
		return
	}
	gd, err := srv.store.getGameDay(body.GameDayID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "game day not found")
		return
	}
	if rc, err := time.Parse(time.RFC3339, gd.Schedule.RegistrationClose); err != nil || !time.Now().Before(rc) {
		jsonErr(w, http.StatusConflict, "game day registration is closed")
		return
	}
	if gd.Phases.RoundRobin.Status != "upcoming" {
		jsonErr(w, http.StatusConflict, "game day registration is closed")
		return
	}
	srv.store.addVersionRegistration(tankID, version, body.GameDayID)
	srv.store.addRosterEntry(body.GameDayID, tankID, version, tank.Name)
	jsonOK(w, map[string]string{"gameDayId": body.GameDayID})
}

func (srv *server) deregisterVersion(w http.ResponseWriter, r *http.Request, tankID, version string) {
	var body struct {
		GameDayID string `json:"gameDayId"`
	}
	if err := readJSON(r, &body); err != nil || body.GameDayID == "" {
		jsonErr(w, http.StatusBadRequest, "gameDayId is required")
		return
	}
	srv.store.removeVersionRegistration(tankID, version, body.GameDayID)
	srv.store.removeRosterEntry(body.GameDayID, tankID)
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
		case "ranger":
			oppTankID = "__ranger__"
		case "randy":
			oppTankID = "__randy__"
		default:
			jsonErr(w, http.StatusBadRequest, "unknown AI: use scout, bruiser, ranger, or randy")
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

// ── Auth ─────────────────────────────────────────────────────────────────────

// forgotPassword is a local-dev stand-in for item 217's real backend, which
// self-invokes forgot-password-worker asynchronously and never sends a real
// email — there is no Cognito/SES here, so this just logs what would happen
// and always responds 202, matching the enumeration-safe contract's shape
// for frontend integration testing.
func (srv *server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Email) == "" {
		jsonErr(w, http.StatusBadRequest, "email required")
		return
	}
	email := strings.TrimSpace(body.Email)
	found := false
	for _, u := range srv.store.listUsers() {
		if u.Email == email {
			found = true
			break
		}
	}
	log.Printf("[local] forgot-password requested for %s (account found: %v) — no real email sent in local dev", email, found)
	w.WriteHeader(http.StatusAccepted)
}

// ── Rankings / Game Days ────────────────────────────────────────────────────

// aiTankAvatarURL mirrors tank-api's item 218 fix — the Leaderboard shouldn't
// show a built-in AI tank's real (seeder) Cognito owner's picture.
const aiTankAvatarURL = "/avatar.png"

func (srv *server) getRankings(w http.ResponseWriter, _ *http.Request) {
	tanks := srv.store.scanTanksByScore(localUserID)
	type entry struct {
		Rank          int    `json:"rank"`
		TankID        string `json:"tankId"`
		Name          string `json:"name"`
		UserID        string `json:"userId"`
		AuthorPicture string `json:"authorPicture,omitempty"`
		AvatarURL     string `json:"avatarUrl,omitempty"`
		GlobalScore   int    `json:"globalScore"`
		BestFinish    *int   `json:"bestFinish"`
		GameDaysCount int    `json:"gameDaysCount"`
		LastActiveAt  int64  `json:"lastActiveAt"`
	}
	pictures := make(map[string]string)
	for _, u := range srv.store.listUsers() {
		pictures[u.Sub] = u.Picture
	}
	result := make([]entry, len(tanks))
	for i, t := range tanks {
		// Note: this shape has no separate "author name" field (pre-existing
		// tank-api/localserver rankings shape mismatch, flagged in item 209/213) —
		// only the author's picture can be overridden here for AI tanks.
		picture := pictures[t.UserID]
		if isAITankID(t.TankID) {
			picture = aiTankAvatarURL
		}
		result[i] = entry{
			Rank:          i + 1,
			TankID:        t.TankID,
			Name:          t.Name,
			UserID:        t.UserID,
			AuthorPicture: picture,
			AvatarURL:     t.AvatarURL,
			GlobalScore:   t.GlobalScore,
			BestFinish:    t.BestFinish,
			GameDaysCount: t.GameDaysCount,
			LastActiveAt:  t.LastActiveAt,
		}
	}
	jsonOK(w, result)
}

// getPublicUserProfile mirrors tank-api's GET /users/{sub} (item 210). Local
// dev only has one user, so any other sub 404s like a real unknown user
// would.
func (srv *server) getPublicUserProfile(w http.ResponseWriter, sub string) {
	if sub != localUserID {
		jsonErr(w, http.StatusNotFound, "user not found")
		return
	}
	srv.mu.RLock()
	u := srv.store.users[localUserID]
	srv.mu.RUnlock()

	tanks := srv.store.listTanksByUser(localUserID)
	type publicTank struct {
		TankID        string `json:"tankId"`
		Name          string `json:"name"`
		AvatarURL     string `json:"avatarUrl,omitempty"`
		GlobalScore   int    `json:"globalScore"`
		BestFinish    *int   `json:"bestFinish"`
		GameDaysCount int    `json:"gameDaysCount"`
		LastActiveAt  int64  `json:"lastActiveAt"`
	}
	publicTanks := make([]publicTank, len(tanks))
	for i, t := range tanks {
		publicTanks[i] = publicTank{
			TankID: t.TankID, Name: t.Name, AvatarURL: t.AvatarURL,
			GlobalScore: t.GlobalScore, BestFinish: t.BestFinish,
			GameDaysCount: t.GameDaysCount, LastActiveAt: t.LastActiveAt,
		}
	}
	jsonOK(w, map[string]interface{}{
		"sub": sub, "name": u.Name, "picture": u.Picture, "tanks": publicTanks,
	})
}

// ── Friends ──────────────────────────────────────────────────────────────────

type friendEntry struct {
	UserID  string `json:"userId"`
	Name    string `json:"name"`
	Picture string `json:"picture,omitempty"`
}

// resolveUserDisplay falls back to the raw sub when it isn't a known local
// user — local dev only ever seeds localUserID, so any other id used to
// exercise the friends API via curl has no name/picture on file.
func (srv *server) resolveUserDisplay(sub string) (name, picture string) {
	if u, ok := srv.store.getUser(sub); ok {
		return u.Name, u.Picture
	}
	return sub, ""
}

func (srv *server) listFriends(w http.ResponseWriter) {
	rows := srv.store.listFriendships(localUserID)
	friends := []friendEntry{}
	incoming := []friendEntry{}
	outgoing := []friendEntry{}
	for friendID, f := range rows {
		name, picture := srv.resolveUserDisplay(friendID)
		entry := friendEntry{UserID: friendID, Name: name, Picture: picture}
		switch {
		case f.Status == "accepted":
			friends = append(friends, entry)
		case f.RequestedBy == localUserID:
			outgoing = append(outgoing, entry)
		default:
			incoming = append(incoming, entry)
		}
	}
	jsonOK(w, map[string]interface{}{"friends": friends, "incoming": incoming, "outgoing": outgoing})
}

func (srv *server) sendFriendRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ToUserID string `json:"toUserId"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.ToUserID) == "" {
		jsonErr(w, http.StatusBadRequest, "toUserId required")
		return
	}
	toUserID := strings.TrimSpace(body.ToUserID)
	if toUserID == localUserID {
		jsonErr(w, http.StatusBadRequest, "cannot friend yourself")
		return
	}
	if existing, ok := srv.store.getFriendship(localUserID, toUserID); ok {
		if existing.Status == "accepted" {
			jsonErr(w, http.StatusConflict, "already friends")
		} else {
			jsonErr(w, http.StatusConflict, "friend request already pending")
		}
		return
	}
	srv.store.sendFriendRequest(localUserID, toUserID)
	jsonOK(w, map[string]string{"status": "pending"})
}

func (srv *server) respondFriendRequest(w http.ResponseWriter, fromUserID string, accept bool) {
	existing, ok := srv.store.getFriendship(localUserID, fromUserID)
	if !ok {
		jsonErr(w, http.StatusNotFound, "no pending request")
		return
	}
	if existing.Status != "pending" || existing.RequestedBy == localUserID {
		jsonErr(w, http.StatusConflict, "no pending incoming request from this user")
		return
	}
	if accept {
		srv.store.acceptFriendRequest(localUserID, fromUserID)
	} else {
		srv.store.removeFriendship(localUserID, fromUserID)
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

func (srv *server) removeFriend(w http.ResponseWriter, friendID string) {
	srv.store.removeFriendship(localUserID, friendID)
	jsonOK(w, map[string]string{"status": "ok"})
}

func (srv *server) listGameDays(w http.ResponseWriter) {
	gds := srv.store.listGameDays()
	if gds == nil {
		gds = []db.GameDay{}
	}
	jsonOK(w, gds)
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

func (srv *server) createGameDay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name                string   `json:"name"`
		RegistrationCloseAt string   `json:"registrationCloseAt"`
		RoundRobinAt        string   `json:"roundRobinAt"`
		FinalAt             string   `json:"finalAt"`
		Autofill            bool     `json:"autofill"`
		ForcedMapIDs        []string `json:"forcedMapIds"`
		RandomMaps          bool     `json:"randomMaps"`
	}
	if err := readJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.RegistrationCloseAt == "" || body.RoundRobinAt == "" || body.FinalAt == "" {
		jsonErr(w, http.StatusBadRequest, "registrationCloseAt, roundRobinAt, finalAt are required")
		return
	}

	parseISO := func(s string) (time.Time, bool) {
		t, err := time.Parse(time.RFC3339, s)
		return t, err == nil
	}
	regClose, ok1 := parseISO(body.RegistrationCloseAt)
	rrAt, ok2 := parseISO(body.RoundRobinAt)
	finalAt, ok3 := parseISO(body.FinalAt)
	if !ok1 || !ok2 || !ok3 {
		jsonErr(w, http.StatusBadRequest, "timestamps must be ISO 8601")
		return
	}
	if !regClose.Before(rrAt) {
		jsonErr(w, http.StatusBadRequest, "registration must close before round robin")
		return
	}
	if !rrAt.Before(finalAt) {
		jsonErr(w, http.StatusBadRequest, "round robin must start before final")
		return
	}
	const maxElimRounds = 5
	elimination := make([]string, maxElimRounds)
	for i := 0; i < maxElimRounds; i++ {
		t := finalAt.Add(-time.Duration(maxElimRounds-i) * 30 * time.Minute)
		if t.Before(rrAt) {
			t = rrAt
		}
		elimination[i] = t.UTC().Format(time.RFC3339)
	}

	gd := db.GameDay{
		GameDayID: newUUID(),
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
		CreatedAt:    time.Now().Unix(),
		Autofill:     body.Autofill,
		ForcedMapIDs: body.ForcedMapIDs,
		RandomMaps:   body.RandomMaps,
	}
	srv.store.putGameDay(gd)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(gd)
}

func (srv *server) deleteGameDay(w http.ResponseWriter, r *http.Request, gameDayID string) {
	gd, err := srv.store.getGameDay(gameDayID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "game day not found")
		return
	}
	force := r.URL.Query().Get("force") == "true"
	if !force && gd.Phases.RoundRobin.Status != "upcoming" {
		jsonErr(w, http.StatusConflict, "game day has already started")
		return
	}
	srv.store.deleteGameDay(gameDayID)
	for _, t := range gd.RegisteredTanks {
		srv.store.removeVersionRegistration(t.TankID, t.Version, gameDayID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (srv *server) getGameDay(w http.ResponseWriter, _ *http.Request, gameDayID string) {
	gd, err := srv.store.getGameDay(gameDayID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "game day not found")
		return
	}
	jsonOK(w, gd)
}

func (srv *server) patchGameDay(w http.ResponseWriter, r *http.Request, gameDayID string) {
	gd, err := srv.store.getGameDay(gameDayID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "game day not found")
		return
	}

	force := r.URL.Query().Get("force") == "true"

	var body struct {
		Name                string            `json:"name,omitempty"`
		RegistrationCloseAt string            `json:"registrationCloseAt,omitempty"`
		RoundRobinAt        string            `json:"roundRobinAt,omitempty"`
		FinalAt             string            `json:"finalAt,omitempty"`
		Autofill            *bool             `json:"autofill"`
		ForcedMapIDs        *[]string         `json:"forcedMapIds"`
		RandomMaps          *bool             `json:"randomMaps"`
		PhaseOverride       map[string]string `json:"phaseOverride,omitempty"`
	}
	if err := readJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// ?force=true: apply phase status overrides without schedule guards.
	if force && len(body.PhaseOverride) > 0 {
		validStatuses := map[string]bool{"upcoming": true, "running": true, "complete": true, "cancelled": true}
		for phase, status := range body.PhaseOverride {
			if !validStatuses[status] {
				jsonErr(w, http.StatusBadRequest, "phaseOverride status must be one of: upcoming, running, complete, cancelled")
				return
			}
			ps := db.PhaseStatus{Status: status}
			switch phase {
			case "roundRobin":
				gd.Phases.RoundRobin = ps
			case "final":
				gd.Phases.Final = ps
			default:
				if gd.Phases.Elimination == nil {
					gd.Phases.Elimination = make(map[string]db.PhaseStatus)
				}
				gd.Phases.Elimination[phase] = ps
			}
		}
		srv.store.putGameDay(gd)
		jsonOK(w, gd)
		return
	}

	if !force && gd.Phases.RoundRobin.Status != "upcoming" {
		jsonErr(w, http.StatusConflict, "game day has already started")
		return
	}
	if !force {
		if finalAt, parseErr := time.Parse(time.RFC3339, gd.Schedule.Final); parseErr == nil && finalAt.Before(time.Now()) {
			jsonErr(w, http.StatusConflict, "game day has already concluded")
			return
		}
	}

	// Validate ordering on the merged schedule.
	parseISO := func(s string) (time.Time, bool) {
		t, err := time.Parse(time.RFC3339, s)
		return t, err == nil
	}
	mergedRegClose := gd.Schedule.RegistrationClose
	mergedRRAt := gd.Schedule.RoundRobin
	mergedFinalAt := gd.Schedule.Final
	if body.RegistrationCloseAt != "" {
		mergedRegClose = body.RegistrationCloseAt
	}
	if body.RoundRobinAt != "" {
		mergedRRAt = body.RoundRobinAt
	}
	if body.FinalAt != "" {
		mergedFinalAt = body.FinalAt
	}
	if rc, ok1 := parseISO(mergedRegClose); ok1 {
		if rr, ok2 := parseISO(mergedRRAt); ok2 && !rc.Before(rr) {
			jsonErr(w, http.StatusBadRequest, "registration must close before round robin")
			return
		}
	}
	if rr, ok1 := parseISO(mergedRRAt); ok1 {
		if fn, ok2 := parseISO(mergedFinalAt); ok2 && !rr.Before(fn) {
			jsonErr(w, http.StatusBadRequest, "round robin must start before final")
			return
		}
	}

	if body.RegistrationCloseAt != "" {
		gd.Schedule.RegistrationClose = body.RegistrationCloseAt
	}
	if body.RoundRobinAt != "" {
		gd.Schedule.RoundRobin = body.RoundRobinAt
	}
	if body.FinalAt != "" {
		gd.Schedule.Final = body.FinalAt
		fn, _ := parseISO(body.FinalAt)
		rr, _ := parseISO(gd.Schedule.RoundRobin)
		const maxElim = 5
		elim := make([]string, maxElim)
		for i := 0; i < maxElim; i++ {
			t := fn.Add(-time.Duration(maxElim-i) * 30 * time.Minute)
			if t.Before(rr) {
				t = rr
			}
			elim[i] = t.UTC().Format(time.RFC3339)
		}
		gd.Schedule.Elimination = elim
	}
	if body.Autofill != nil {
		gd.Autofill = *body.Autofill
	}
	if body.ForcedMapIDs != nil {
		gd.ForcedMapIDs = *body.ForcedMapIDs
	}
	if body.RandomMaps != nil {
		gd.RandomMaps = *body.RandomMaps
	}
	// Recompute full display name using base name and merged schedule.
	patchBaseName := strings.TrimSpace(body.Name)
	if patchBaseName == "" {
		patchBaseName = gameDayBaseName(gd.Name)
	}
	rrAt, _ := parseISO(gd.Schedule.RoundRobin)
	finalAt, _ := parseISO(gd.Schedule.Final)
	gd.Name = gameDayDisplayName(patchBaseName, rrAt, finalAt)
	srv.store.putGameDay(gd)
	jsonOK(w, gd)
}

func (srv *server) addRosterEntry(w http.ResponseWriter, r *http.Request, gameDayID string) {
	gd, err := srv.store.getGameDay(gameDayID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "game day not found")
		return
	}
	if gd.Phases.RoundRobin.Status != "upcoming" {
		jsonErr(w, http.StatusConflict, "game day has already started")
		return
	}
	var body struct {
		TankID  string `json:"tankId"`
		Version string `json:"version"`
	}
	if err := readJSON(r, &body); err != nil || body.TankID == "" || body.Version == "" {
		jsonErr(w, http.StatusBadRequest, "tankId and version are required")
		return
	}
	if _, _, isMajor, ok := parseVersion(body.Version); !ok || !isMajor {
		jsonErr(w, http.StatusUnprocessableEntity, "version must be a major version (e.g. v1)")
		return
	}
	// AI tanks (builtin-* in production, __scout__/__bruiser__/__ranger__/__randy__ in localserver)
	// may be added more than once so the bracket can be padded with multiple
	// instances of the same bot.
	if !isAITankID(body.TankID) {
		for _, t := range gd.RegisteredTanks {
			if t.TankID == body.TankID {
				jsonErr(w, http.StatusConflict, "tank is already registered for this game day")
				return
			}
		}
	}
	tankName := ""
	if t, err := srv.store.getTank(body.TankID); err == nil {
		tankName = t.Name
	}
	srv.store.addRosterEntry(gameDayID, body.TankID, body.Version, tankName)
	// Mirror the registration on the TankVersion record so TankDetail shows the
	// Withdraw button. Skip for AI/built-in tanks — they have no version record.
	if !isAITankID(body.TankID) {
		srv.store.addVersionRegistration(body.TankID, body.Version, gameDayID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (srv *server) removeRosterEntry(w http.ResponseWriter, gameDayID, tankID string) {
	gd, err := srv.store.getGameDay(gameDayID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "game day not found")
		return
	}
	if gd.Phases.RoundRobin.Status != "upcoming" {
		jsonErr(w, http.StatusConflict, "game day has already started")
		return
	}
	// Capture the version before removing so we can update the TankVersion record.
	var removedVersion string
	for _, t := range gd.RegisteredTanks {
		if t.TankID == tankID {
			removedVersion = t.Version
			break
		}
	}
	srv.store.removeRosterEntry(gameDayID, tankID)
	// Mirror the deregistration on the TankVersion record. Skip for AI/built-in tanks.
	if removedVersion != "" && !isAITankID(tankID) {
		srv.store.removeVersionRegistration(tankID, removedVersion, gameDayID)
	}
	w.WriteHeader(http.StatusNoContent)
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

// ── Admin handlers ─────────────────────────────────────────────────────────

func (srv *server) adminListUsers(w http.ResponseWriter) {
	type userResp struct {
		Sub     string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		IsAdmin bool   `json:"isAdmin"`
		Tier    string `json:"tier"`
	}
	srv.mu.RLock()
	tier := srv.userSettings.Tier
	srv.mu.RUnlock()
	if tier == "" {
		tier = db.TierFree
	}
	users := srv.store.listUsers()
	resp := make([]userResp, 0, len(users))
	for _, u := range users {
		// localserver only tracks one global userSettings record (single local user) —
		// every listed user shows that same tier in local dev.
		resp = append(resp, userResp{Sub: u.Sub, Email: u.Email, Name: u.Name, Enabled: u.Enabled, IsAdmin: u.IsAdmin, Tier: tier})
	}
	jsonOK(w, map[string]any{"users": resp})
}

func (srv *server) adminUpdateUser(w http.ResponseWriter, r *http.Request, sub string) {
	var body struct {
		Disabled bool `json:"disabled"`
	}
	if err := readJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	srv.store.updateUserEnabled(sub, !body.Disabled)
	jsonOK(w, map[string]string{"status": "ok"})
}

func (srv *server) adminToggleUserRole(w http.ResponseWriter, r *http.Request, sub string) {
	if sub == localUserID {
		jsonErr(w, http.StatusBadRequest, "cannot modify your own admin role")
		return
	}
	isAdmin, found := srv.store.toggleUserAdmin(sub)
	if !found {
		jsonErr(w, http.StatusNotFound, "user not found")
		return
	}
	jsonOK(w, map[string]bool{"isAdmin": isAdmin})
}

func (srv *server) adminDeleteUser(w http.ResponseWriter, r *http.Request, sub string) {
	if sub == localUserID {
		jsonErr(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}
	if !srv.store.deleteUser(sub) {
		jsonErr(w, http.StatusNotFound, "user not found")
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

func (srv *server) adminListTanks(w http.ResponseWriter, r *http.Request) {
	const pageSize = 50
	cursor := r.URL.Query().Get("nextToken")
	all := srv.store.listAllTanks()

	// Find start index after cursor.
	start := 0
	if cursor != "" {
		for i, t := range all {
			if t.TankID == cursor {
				start = i + 1
				break
			}
		}
	}

	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	page := all[start:end]

	resp := map[string]any{"tanks": page}
	if end < len(all) {
		resp["nextToken"] = all[end-1].TankID
	}
	jsonOK(w, resp)
}

func (srv *server) adminUpdateTank(w http.ResponseWriter, r *http.Request, tankID string) {
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil || body.Name == "" {
		jsonErr(w, http.StatusBadRequest, "name is required")
		return
	}
	t, err := srv.store.getTank(tankID)
	if err != nil {
		jsonErr(w, http.StatusNotFound, "tank not found")
		return
	}
	t.Name = body.Name
	srv.store.putTank(t)
	jsonOK(w, map[string]string{"status": "ok"})
}

func (srv *server) adminDeleteTank(w http.ResponseWriter, r *http.Request, tankID string) {
	if _, err := srv.store.getTank(tankID); err != nil {
		jsonErr(w, http.StatusNotFound, "tank not found")
		return
	}
	srv.store.deleteTank(tankID)
	jsonOK(w, map[string]string{"status": "deleted"})
}

func (srv *server) adminResetCompile(w http.ResponseWriter, r *http.Request, tankID, version string) {
	if _, err := srv.store.getVersion(tankID, version); err != nil {
		jsonErr(w, http.StatusNotFound, "version not found")
		return
	}
	srv.store.updateVersionCompile(tankID, version, db.CompileUpdate{
		Status: "failed", CompileError: "reset by admin",
	})
	jsonOK(w, map[string]string{"status": "reset"})
}

// ── Ad config handlers ─────────────────────────────────────────────────────

func (srv *server) getAdConfig(w http.ResponseWriter) {
	srv.mu.RLock()
	cfg := srv.adConfig
	srv.mu.RUnlock()
	jsonOK(w, cfg)
}

func (srv *server) patchAdConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled      *bool  `json:"enabled"`
		PublisherID  string `json:"publisherId"`
		TopSlotID    string `json:"topSlotId"`
		RightSlotID  string `json:"rightSlotId"`
		BottomSlotID string `json:"bottomSlotId"`
	}
	if err := readJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	srv.mu.Lock()
	if body.Enabled != nil {
		srv.adConfig.Enabled = *body.Enabled
	}
	srv.adConfig.PublisherID = body.PublisherID
	srv.adConfig.TopSlotID = body.TopSlotID
	srv.adConfig.RightSlotID = body.RightSlotID
	srv.adConfig.BottomSlotID = body.BottomSlotID
	srv.mu.Unlock()
	jsonOK(w, map[string]string{"status": "ok"})
}

// ── User settings handlers ─────────────────────────────────────────────────

func (srv *server) getMySettings(w http.ResponseWriter) {
	srv.mu.RLock()
	us := srv.userSettings
	srv.mu.RUnlock()
	tier := us.Tier
	if tier == "" {
		tier = db.TierFree
	}
	tankLimit, compileLimit := db.TierLimits(tier)
	jsonOK(w, map[string]interface{}{
		"tier":                   tier,
		"compilationsThisWindow": us.CompilationsThisWindow,
		"windowStart":            us.WindowStart,
		"tankLimit":              tankLimit,
		"compilationLimit":       compileLimit,
	})
}

func (srv *server) patchMySettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tier string `json:"tier"`
	}
	if err := readJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch body.Tier {
	case db.TierFree, db.TierBuilder, db.TierPro:
	default:
		jsonErr(w, http.StatusBadRequest, "tier must be free, builder, or pro")
		return
	}
	srv.mu.Lock()
	srv.userSettings.Tier = body.Tier
	srv.mu.Unlock()
	jsonOK(w, map[string]string{"tier": body.Tier})
}

// ── User profile handlers ──────────────────────────────────────────────────

func (srv *server) patchMyProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		jsonErr(w, http.StatusBadRequest, "name is required")
		return
	}
	srv.store.updateUserName(localUserID, name)
	for _, t := range srv.store.listTanksByUser(localUserID) {
		srv.store.updateAuthorName(t.TankID, name)
	}
	jsonOK(w, map[string]string{"name": name})
}

// uploadProfilePicture mirrors tank-api's PUT /me/profile/picture (item 198),
// storing the decoded bytes in memory (single local user) and serving them
// back via GET /local-user-assets/avatar.{ext} instead of S3/Cognito.
func (srv *server) uploadProfilePicture(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Data        string `json:"data"`
		ContentType string `json:"contentType"`
	}
	if err := readJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var ext string
	switch body.ContentType {
	case "image/png":
		ext = "png"
	case "image/jpeg":
		ext = "jpg"
	default:
		jsonErr(w, http.StatusBadRequest, "contentType must be image/png or image/jpeg")
		return
	}
	imgBytes, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "data must be valid base64")
		return
	}
	if len(imgBytes) == 0 {
		jsonErr(w, http.StatusBadRequest, "data is required")
		return
	}
	if len(imgBytes) > maxAvatarBytes {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("avatar must be %d bytes or fewer", maxAvatarBytes))
		return
	}

	srv.mu.Lock()
	srv.userAvatarData = imgBytes
	srv.userAvatarType = body.ContentType
	srv.mu.Unlock()

	url := fmt.Sprintf("http://localhost:%s/local-user-assets/avatar.%s", srv.port, ext)
	srv.store.updateUserPicture(localUserID, url)
	jsonOK(w, map[string]string{"picture": url})
}

func (srv *server) serveLocalUserAsset(w http.ResponseWriter, filename string) {
	srv.mu.RLock()
	data := srv.userAvatarData
	contentType := srv.userAvatarType
	srv.mu.RUnlock()
	if data == nil || !strings.HasPrefix(filename, "avatar.") {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Write(data)
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

// convertAISource transforms a full package main AI source file into the
// package tank dot-import style expected by compileWasm. It replaces
// "package main" with "package tank", replaces the named import block with
// a single dot-import of the SDK, strips //go:wasmimport / //go:noescape
// directives, func main, func encode, and the WASM host-import stubs, then
// renames func tick → func Tick.
func convertAISource(src string) string {
	lines := strings.Split(src, "\n")
	var out []string
	// skipUntilBrace tracks removal of a multi-line function or var body.
	// depth counts open braces; we skip until we reach depth 0 after the opener.
	skipDepth := 0
	inImport := false
	skipVarLine := false

	// funcsToRemove are function signatures whose bodies we want to strip.
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

		// Remove //go: directives (wasmimport, noescape, etc.).
		if strings.HasPrefix(trimmed, "//go:") {
			continue
		}

		// Skip body of a function/var we're removing.
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

		// Remove var Config = ... and var cfgJSON = ... (top-level; may be multi-line).
		if strings.HasPrefix(trimmed, "var Config ") || strings.HasPrefix(trimmed, "var cfgJSON ") {
			skipVarLine = true
		}
		if skipVarLine {
			// Count braces to handle multi-line func literals.
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

		// Remove named functions (main, encode, WASM stubs).
		isRemovedFunc := false
		for _, sig := range funcsToRemove {
			if strings.HasPrefix(trimmed, sig) {
				isRemovedFunc = true
				break
			}
		}
		if isRemovedFunc {
			// Count opening brace on this line; may open on same line or next.
			for _, ch := range line {
				if ch == '{' {
					skipDepth++
				} else if ch == '}' {
					skipDepth--
				}
			}
			// If no brace on this line (e.g. bare function stub with no body), skip line only.
			continue
		}

		// Rename func tick → func Tick.
		line = strings.Replace(line, "func tick(", "func Tick(", 1)

		// Remove named SDK qualifier.
		line = strings.ReplaceAll(line, "tankmaze.", "")

		// If this line opens a surviving var block (var (...)), strip any
		// immediately-preceding comment line from out so that stripPreamble
		// can find the \n\nvar  token.  Without this, a comment between the
		// blank line and var ( prevents the double-newline from landing
		// directly before "var ", causing stripPreamble to miss the block and
		// fall through to func Tick, silently dropping the declarations.
		if trimmed == "var (" {
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

	// Collapse runs of more than two consecutive blank lines.
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

	// Trim leading blank lines.
	for len(result) > 0 && strings.TrimSpace(result[0]) == "" {
		result = result[1:]
	}

	return strings.Join(result, "\n")
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
