package main

import (
	"sort"
	"strings"
	"sync"

	"github.com/tankmaze/backend/internal/db"
)

type localUser struct {
	Sub     string
	Email   string
	Name    string
	Enabled bool
	IsAdmin bool
}

type memStore struct {
	mu       sync.RWMutex
	tanks    map[string]db.Tank
	versions map[string][]db.TankVersion // tankId → slice
	matches  map[string]db.Match
	maps     map[string]db.Map
	mapSlugs map[string]string       // slug → mapId
	rankings map[string][]db.Ranking // tankId → []Ranking
	users    map[string]localUser    // sub → user
	gamedays map[string]db.GameDay
}

func newStore() *memStore {
	s := &memStore{
		tanks:    make(map[string]db.Tank),
		versions: make(map[string][]db.TankVersion),
		matches:  make(map[string]db.Match),
		maps:     make(map[string]db.Map),
		mapSlugs: make(map[string]string),
		rankings: make(map[string][]db.Ranking),
		users:    make(map[string]localUser),
		gamedays: make(map[string]db.GameDay),
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
// Production tanks use the "builtin-" prefix; localserver uses "__scout__" / "__bruiser__".
func isAITankID(tankID string) bool {
	return strings.HasPrefix(tankID, "builtin-") ||
		tankID == "__scout__" || tankID == "__bruiser__"
}

func (s *memStore) addRosterEntry(gameDayID, tankID, version, tankName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gd, ok := s.gamedays[gameDayID]
	if !ok {
		return
	}
	// AI tanks (builtin-* in production, __scout__/__bruiser__ in localserver)
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
