package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tankmaze/backend/internal/db"
)

type localUser struct {
	Sub     string
	Email   string
	Name    string
	Picture string
	Enabled bool
	IsAdmin bool
}

// localFriendship mirrors db.Friendship's dual-item model (see
// internal/db/friendships.go) in memory: one entry per direction, keyed
// friendships[userId][friendId].
type localFriendship struct {
	Status      string
	RequestedBy string
}

type memStore struct {
	mu          sync.RWMutex
	tanks       map[string]db.Tank
	versions    map[string][]db.TankVersion // tankId → slice
	matches     map[string]db.Match
	maps        map[string]db.Map
	mapSlugs    map[string]string       // slug → mapId
	rankings    map[string][]db.Ranking // tankId → []Ranking
	users       map[string]localUser    // sub → user
	gamedays    map[string]db.GameDay
	friendships map[string]map[string]localFriendship // userId → friendId → relationship
	messages    map[string][]db.Message               // conversationId → messages, chronological
}

func newStore() *memStore {
	s := &memStore{
		tanks:       make(map[string]db.Tank),
		versions:    make(map[string][]db.TankVersion),
		matches:     make(map[string]db.Match),
		maps:        make(map[string]db.Map),
		mapSlugs:    make(map[string]string),
		rankings:    make(map[string][]db.Ranking),
		users:       make(map[string]localUser),
		gamedays:    make(map[string]db.GameDay),
		friendships: make(map[string]map[string]localFriendship),
		messages:    make(map[string][]db.Message),
	}
	s.users[localUserID] = localUser{
		Sub:     localUserID,
		Email:   "dev@localhost",
		Name:    "Local User",
		Enabled: true,
		IsAdmin: true,
	}
	return s
}

func (s *memStore) listTanksByUser(uid string) []db.Tank {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []db.Tank
	for _, t := range s.tanks {
		if t.UserID == uid {
			result = append(result, t)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt > result[j].CreatedAt
	})
	return result
}

func (s *memStore) putTank(t db.Tank) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tanks[t.TankID] = t
}

func (s *memStore) getTank(tankID string) (db.Tank, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tanks[tankID]
	if !ok {
		return db.Tank{}, db.ErrNotFound
	}
	return t, nil
}

func (s *memStore) listVersionsByTank(tankID string) []db.TankVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()
	vs := s.versions[tankID]
	out := make([]db.TankVersion, len(vs))
	copy(out, vs)
	return out
}

func (s *memStore) putVersion(v db.TankVersion) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.versions[v.TankID]
	for i, existing := range vs {
		if existing.Version == v.Version {
			vs[i] = v
			s.versions[v.TankID] = vs
			return
		}
	}
	s.versions[v.TankID] = append(vs, v)
}

func (s *memStore) getVersion(tankID, version string) (db.TankVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.versions[tankID] {
		if v.Version == version {
			return v, nil
		}
	}
	return db.TankVersion{}, db.ErrNotFound
}

func (s *memStore) updateVersionCompile(tankID, version string, u db.CompileUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.versions[tankID]
	for i, v := range vs {
		if v.Version == version {
			vs[i].CompileStatus = u.Status
			vs[i].WasmS3Key = u.WasmS3Key
			vs[i].WasmSHA256 = u.WasmSHA256
			vs[i].CompileError = u.CompileError
			s.versions[tankID] = vs
			return
		}
	}
}

func (s *memStore) updateVersionConfig(tankID, version string, cfg db.VersionConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.versions[tankID]
	for i, v := range vs {
		if v.Version == version {
			vs[i].Config = cfg
			s.versions[tankID] = vs
			return
		}
	}
}

func (s *memStore) addVersionRegistration(tankID, version, gameDayID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.versions[tankID]
	for i, v := range vs {
		if v.Version == version {
			for _, id := range vs[i].RegisteredForGameDays {
				if id == gameDayID {
					return
				}
			}
			vs[i].RegisteredForGameDays = append(vs[i].RegisteredForGameDays, gameDayID)
			s.versions[tankID] = vs
			return
		}
	}
}

func (s *memStore) removeVersionRegistration(tankID, version, gameDayID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.versions[tankID]
	for i, v := range vs {
		if v.Version == version {
			filtered := vs[i].RegisteredForGameDays[:0]
			for _, id := range v.RegisteredForGameDays {
				if id != gameDayID {
					filtered = append(filtered, id)
				}
			}
			vs[i].RegisteredForGameDays = filtered
			s.versions[tankID] = vs
			return
		}
	}
}

func (s *memStore) incrementTestMatchCount(tankID, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.versions[tankID]
	for i, v := range vs {
		if v.Version == version {
			vs[i].TestMatchCount++
			s.versions[tankID] = vs
			return
		}
	}
}

func (s *memStore) listRankingsByTank(tankID string) []db.Ranking {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rs := s.rankings[tankID]
	out := make([]db.Ranking, len(rs))
	copy(out, rs)
	return out
}

func (s *memStore) scoreTransfer(in db.ScoreTransferInput) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.tanks[in.SourceTankID]
	if !ok {
		return
	}
	dst, ok := s.tanks[in.TargetTankID]
	if !ok {
		return
	}
	src.GlobalScore = 0
	src.BestFinish = nil
	src.GameDaysCount = 0
	src.ScoreTransferredTo = in.TargetTankID
	s.tanks[in.SourceTankID] = src

	dst.GlobalScore = in.GlobalScore
	dst.BestFinish = in.BestFinish
	dst.GameDaysCount = in.GameDaysCount
	dst.LastActiveAt = in.LastActiveAt
	dst.ScoreTransferredFrom = in.SourceTankID
	s.tanks[in.TargetTankID] = dst

	s.rankings[in.TargetTankID] = append(s.rankings[in.TargetTankID], s.rankings[in.SourceTankID]...)
	delete(s.rankings, in.SourceTankID)
}

func (s *memStore) putMatch(m db.Match) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.matches[m.MatchID] = m
}

func (s *memStore) getMatch(matchID string) (db.Match, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.matches[matchID]
	if !ok {
		return db.Match{}, db.ErrNotFound
	}
	return m, nil
}

func (s *memStore) updateMatchStatus(matchID, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.matches[matchID]
	m.Status = status
	s.matches[matchID] = m
}

func (s *memStore) setMatchResult(matchID string, result db.MatchResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.matches[matchID]
	m.Status = "ended"
	m.Result = &result
	s.matches[matchID] = m
}

func (s *memStore) scanTanksByScore(excludeUserID string) []db.Tank {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []db.Tank
	for _, t := range s.tanks {
		if t.UserID == excludeUserID {
			result = append(result, t)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].GlobalScore > result[j].GlobalScore
	})
	return result
}

func (s *memStore) listActiveMaps() []db.Map {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []db.Map
	for _, m := range s.maps {
		if m.IsActive {
			result = append(result, m)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt < result[j].CreatedAt
	})
	return result
}

func (s *memStore) getMapByID(mapID string) (db.Map, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.maps[mapID]
	if !ok {
		return db.Map{}, db.ErrNotFound
	}
	return m, nil
}

func (s *memStore) getMapBySlug(slug string) (db.Map, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.mapSlugs[slug]
	if !ok {
		return db.Map{}, db.ErrNotFound
	}
	m, ok := s.maps[id]
	if !ok {
		return db.Map{}, db.ErrNotFound
	}
	return m, nil
}

func (s *memStore) putMap(m db.Map) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maps[m.MapID] = m
	s.mapSlugs[m.Slug] = m.MapID
}

func (s *memStore) updateMap(mapID, name, description string, isActive bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.maps[mapID]
	if !ok {
		return
	}
	m.Name = name
	m.Description = description
	m.IsActive = isActive
	s.maps[mapID] = m
}

// ── Tank / version deletion ─────────────────────────────────────────────────

func (s *memStore) deleteTank(tankID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tanks, tankID)
	delete(s.versions, tankID)
	delete(s.rankings, tankID)
}

func (s *memStore) listAllTanks() []db.Tank {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]db.Tank, 0, len(s.tanks))
	for _, t := range s.tanks {
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].GlobalScore != result[j].GlobalScore {
			return result[i].GlobalScore > result[j].GlobalScore
		}
		return result[i].TankID < result[j].TankID
	})
	return result
}

// ── User management ─────────────────────────────────────────────────────────

func (s *memStore) listUsers() []localUser {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]localUser, 0, len(s.users))
	for _, u := range s.users {
		result = append(result, u)
	}
	return result
}

func (s *memStore) getUser(sub string) (localUser, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[sub]
	return u, ok
}

func (s *memStore) updateUserEnabled(sub string, enabled bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[sub]
	if !ok {
		return false
	}
	u.Enabled = enabled
	s.users[sub] = u
	return true
}

func (s *memStore) toggleUserAdmin(sub string) (isAdmin bool, found bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[sub]
	if !ok {
		return false, false
	}
	u.IsAdmin = !u.IsAdmin
	s.users[sub] = u
	return u.IsAdmin, true
}

func (s *memStore) updateUserName(sub, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[sub]
	if !ok {
		return false
	}
	u.Name = name
	s.users[sub] = u
	return true
}

func (s *memStore) updateUserPicture(sub, url string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[sub]
	if !ok {
		return false
	}
	u.Picture = url
	s.users[sub] = u
	return true
}

func (s *memStore) updateAuthorName(tankID, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tanks[tankID]
	if !ok {
		return
	}
	t.AuthorName = name
	s.tanks[tankID] = t
}

// ── Game Day management ────────────────────────────────────────────────────

func (s *memStore) putGameDay(gd db.GameDay) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gamedays[gd.GameDayID] = gd
}

func (s *memStore) getGameDay(gameDayID string) (db.GameDay, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	gd, ok := s.gamedays[gameDayID]
	if !ok {
		return db.GameDay{}, db.ErrNotFound
	}
	return gd, nil
}

func (s *memStore) listGameDays() []db.GameDay {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]db.GameDay, 0, len(s.gamedays))
	for _, gd := range s.gamedays {
		result = append(result, gd)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt > result[j].CreatedAt
	})
	return result
}

func (s *memStore) deleteGameDay(gameDayID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.gamedays, gameDayID)
}

// isAITankID reports whether tankID belongs to a built-in AI tank.
// Production tanks use the "builtin-" prefix; localserver uses "__scout__" / "__bruiser__" / "__ranger__" / "__randy__".
func isAITankID(tankID string) bool {
	return strings.HasPrefix(tankID, "builtin-") ||
		tankID == "__scout__" || tankID == "__bruiser__" ||
		tankID == "__ranger__" || tankID == "__randy__"
}

func (s *memStore) addRosterEntry(gameDayID, tankID, version, tankName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gd, ok := s.gamedays[gameDayID]
	if !ok {
		return
	}
	// AI tanks (builtin-* in production, __scout__/__bruiser__/__ranger__/__randy__ in localserver)
	// may be added more than once; user tanks are deduplicated.
	if !isAITankID(tankID) {
		for _, t := range gd.RegisteredTanks {
			if t.TankID == tankID {
				return
			}
		}
	}
	gd.RegisteredTanks = append(gd.RegisteredTanks, db.MatchTank{TankID: tankID, Version: version, TankName: tankName})
	s.gamedays[gameDayID] = gd
}

func (s *memStore) removeRosterEntry(gameDayID, tankID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gd, ok := s.gamedays[gameDayID]
	if !ok {
		return
	}
	filtered := gd.RegisteredTanks[:0]
	for _, t := range gd.RegisteredTanks {
		if t.TankID != tankID {
			filtered = append(filtered, t)
		}
	}
	gd.RegisteredTanks = filtered
	s.gamedays[gameDayID] = gd
}

func (s *memStore) deleteUser(sub string) (found bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[sub]; !ok {
		return false
	}
	delete(s.users, sub)
	// cascade: remove all tanks owned by this user
	for tankID, t := range s.tanks {
		if t.UserID == sub {
			delete(s.tanks, tankID)
			delete(s.versions, tankID)
			delete(s.rankings, tankID)
		}
	}
	return true
}

// getFriendship, sendFriendRequest, acceptFriendRequest, removeFriendship,
// and listFriendships mirror internal/db/friendships.go's dual-item model
// (item 223) in memory. Local dev only ever has one real logged-in user, so
// this is mainly useful for exercising the API shape against an arbitrary
// second userId via curl, not a real two-account flow.
func (s *memStore) getFriendship(userID, friendID string) (localFriendship, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.friendships[userID][friendID]
	return f, ok
}

func (s *memStore) sendFriendRequest(fromID, toID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.friendships[fromID] == nil {
		s.friendships[fromID] = make(map[string]localFriendship)
	}
	if s.friendships[toID] == nil {
		s.friendships[toID] = make(map[string]localFriendship)
	}
	s.friendships[fromID][toID] = localFriendship{Status: "pending", RequestedBy: fromID}
	s.friendships[toID][fromID] = localFriendship{Status: "pending", RequestedBy: fromID}
}

func (s *memStore) acceptFriendRequest(userID, friendID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pair := range [2][2]string{{userID, friendID}, {friendID, userID}} {
		if s.friendships[pair[0]] != nil {
			f := s.friendships[pair[0]][pair[1]]
			f.Status = "accepted"
			s.friendships[pair[0]][pair[1]] = f
		}
	}
}

func (s *memStore) removeFriendship(userID, friendID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.friendships[userID], friendID)
	delete(s.friendships[friendID], userID)
}

func (s *memStore) listFriendships(userID string) map[string]localFriendship {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]localFriendship, len(s.friendships[userID]))
	for k, v := range s.friendships[userID] {
		out[k] = v
	}
	return out
}

// blockUser and unblockUser mirror internal/db/friendships.go's BlockUser/
// UnblockUser (item 226): blocking clears any existing relationship and
// writes a "blocked" pair with RequestedBy recording who placed it; only
// that user may unblock.
func (s *memStore) blockUser(blockerID, targetID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.friendships[blockerID], targetID)
	delete(s.friendships[targetID], blockerID)
	if s.friendships[blockerID] == nil {
		s.friendships[blockerID] = make(map[string]localFriendship)
	}
	if s.friendships[targetID] == nil {
		s.friendships[targetID] = make(map[string]localFriendship)
	}
	s.friendships[blockerID][targetID] = localFriendship{Status: "blocked", RequestedBy: blockerID}
	s.friendships[targetID][blockerID] = localFriendship{Status: "blocked", RequestedBy: blockerID}
}

// unblockUser returns (found, isBlocker). Callers should treat !found as 404
// and found && !isBlocker as 403.
func (s *memStore) unblockUser(callerID, targetID string) (found bool, isBlocker bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.friendships[callerID][targetID]
	if !ok || f.Status != "blocked" {
		return false, false
	}
	if f.RequestedBy != callerID {
		return true, false
	}
	delete(s.friendships[callerID], targetID)
	delete(s.friendships[targetID], callerID)
	return true, true
}

// sendMessage, listMessages, and getLatestMessage mirror
// internal/db/messages.go (item 223 Part 2) in memory. No TTL sweep here —
// local dev's in-memory store is already wiped on restart.
func (s *memStore) sendMessage(senderID, recipientID, body string) db.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	m := db.Message{
		MessageID:   newLocalMessageID(now),
		SenderID:    senderID,
		RecipientID: recipientID,
		Body:        body,
		SentAt:      now.Unix(),
	}
	cid := db.ConversationID(senderID, recipientID)
	s.messages[cid] = append(s.messages[cid], m)
	return m
}

func (s *memStore) listMessages(conversationID, sinceMessageID string, limit int) []db.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := s.messages[conversationID]
	var out []db.Message
	if sinceMessageID == "" {
		start := 0
		if len(all) > limit {
			start = len(all) - limit
		}
		out = append(out, all[start:]...)
		return out
	}
	for _, m := range all {
		if m.MessageID > sinceMessageID {
			out = append(out, m)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *memStore) getLatestMessage(conversationID string) (db.Message, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := s.messages[conversationID]
	if len(all) == 0 {
		return db.Message{}, false
	}
	return all[len(all)-1], true
}

// newLocalMessageID mirrors internal/db/messages.go's newMessageID: a
// zero-padded-millis prefix keeps messages sortable by string comparison
// even though the in-memory store doesn't actually need that (it appends in
// order) — kept for shape parity with the real backend's messageId format.
func newLocalMessageID(t time.Time) string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("%020d-%s", t.UnixMilli(), hex.EncodeToString(buf))
}
