// Package main implements the TankMaze tournament-scheduler Lambda.
//
// Triggered by EventBridge Scheduler rules — one rule per Game Day phase.
// Event payload: { "gameDayId": "<uuid>", "phase": "<phase>" }
//
// Phases and actions:
//
//	registration_close – snapshot & lock registered tanks; compute pot seedings
//	round_robin        – assign groups by pot seeding; create + invoke all RR matches
//	elimination_r1     – qualify from RR; seed bracket best-vs-worst; create R1 matches
//	elimination_r2…    – advance bracket from previous round; create next-round matches
//	final              – run championship match (sync); invoke ranking-updater
//
// If a phase fires before the previous phase is complete (all its matches ended),
// the handler returns nil early — the next cron tick will retry.
package main

import (
	crand "crypto/rand"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	lambdasvc "github.com/aws/aws-sdk-go-v2/service/lambda"
	ltypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	schedulersvc "github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"

	"github.com/tankmaze/backend/internal/db"
)

const maxGroupSize = 8

// schedulerEvent is the EventBridge Scheduler payload.
type schedulerEvent struct {
	GameDayID string `json:"gameDayId"`
	Phase     string `json:"phase"` // registration_close | round_robin | elimination_r{N} | final
}

type handler struct {
	store               *db.Store
	lambdaSvc           *lambdasvc.Client
	matchRunnerFunc     string
	rankingUpdaterFunc  string
	scoutTankID         string
	scoutVersion        string
	bruiserTankID       string
	bruiserVersion      string
	schedulerSvc        *schedulersvc.Client
	schedulerRoleArn    string
	schedulerDLQArn     string
	selfArn             string
}

var h *handler

func main() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}
	h = &handler{
		store:              db.New(dynamodb.NewFromConfig(cfg)),
		lambdaSvc:          lambdasvc.NewFromConfig(cfg),
		matchRunnerFunc:    os.Getenv("MATCH_RUNNER_FUNCTION"),
		rankingUpdaterFunc: os.Getenv("RANKING_UPDATER_FUNCTION"),
		scoutTankID:        os.Getenv("SCOUT_TANK_ID"),
		scoutVersion:       os.Getenv("SCOUT_VERSION"),
		bruiserTankID:      os.Getenv("BRUISER_TANK_ID"),
		bruiserVersion:     os.Getenv("BRUISER_VERSION"),
		schedulerSvc:       schedulersvc.NewFromConfig(cfg),
		schedulerRoleArn:   os.Getenv("SCHEDULER_INVOKE_ROLE_ARN"),
		schedulerDLQArn:    os.Getenv("SCHEDULER_DLQ_ARN"),
		selfArn:            os.Getenv("TOURNAMENT_SCHEDULER_FUNCTION"),
	}
	lambda.Start(h.handle)
}

func (h *handler) handle(ctx context.Context, evt schedulerEvent) error {
	if evt.GameDayID == "" || evt.Phase == "" {
		return fmt.Errorf("gameDayId and phase are required")
	}
	log.Printf("tournament-scheduler: gameDay=%s phase=%s", evt.GameDayID, evt.Phase)

	gd, err := h.store.GetGameDay(ctx, evt.GameDayID)
	if err != nil {
		return fmt.Errorf("get gameday: %w", err)
	}

	switch evt.Phase {
	case "registration_close":
		return h.handleRegistrationClose(ctx, gd)
	case "round_robin":
		return h.handleRoundRobin(ctx, gd)
	case "final":
		return h.handleFinal(ctx, gd)
	default:
		if strings.HasPrefix(evt.Phase, "elimination_r") {
			n, parseErr := strconv.Atoi(strings.TrimPrefix(evt.Phase, "elimination_r"))
			if parseErr != nil || n < 1 {
				return fmt.Errorf("invalid elimination phase: %s", evt.Phase)
			}
			if n == 1 {
				return h.handleEliminationR1(ctx, gd)
			}
			return h.handleElimination(ctx, gd, n)
		}
		return fmt.Errorf("unknown phase: %s", evt.Phase)
	}
}

// ---- Phase: registration_close ----------------------------------------------

// handleRegistrationClose scans all versions registered for this game day,
// merges any admin-added roster entries, sorts by global rank, and stores the
// locked list in gameDay.RegisteredTanks.
func (h *handler) handleRegistrationClose(ctx context.Context, gd db.GameDay) error {
	// Idempotency guard: if a later phase has already started (or the tournament
	// was cancelled), registration_close is a no-op.
	switch gd.Phases.RoundRobin.Status {
	case "running", "complete", "cancelled":
		log.Printf("registration_close: round_robin already %s for game day %s — skipping", gd.Phases.RoundRobin.Status, gd.GameDayID)
		return nil
	}

	versions, err := h.store.ScanVersionsByGameDay(ctx, gd.GameDayID)
	if err != nil {
		return fmt.Errorf("scan registered versions: %w", err)
	}

	// Fetch tank records for ranking data. Start with self-registered versions
	// from the tank-versions table, then merge admin-added roster entries that
	// were written directly to gd.RegisteredTanks via AddRosterEntry.
	type ranked struct {
		tank    db.Tank
		version db.TankVersion
	}
	seen := make(map[string]struct{}, len(versions)+len(gd.RegisteredTanks))
	entries := make([]ranked, 0, len(versions)+len(gd.RegisteredTanks))

	for _, ver := range versions {
		if _, dup := seen[ver.TankID]; dup {
			continue
		}
		seen[ver.TankID] = struct{}{}
		tank, err := h.store.GetTank(ctx, ver.TankID)
		if err != nil {
			log.Printf("get tank %s: %v — skipping", ver.TankID, err)
			continue
		}
		entries = append(entries, ranked{tank: tank, version: ver})
	}

	// Merge admin-added roster entries not already covered by the version scan.
	for _, mt := range gd.RegisteredTanks {
		if _, dup := seen[mt.TankID]; dup {
			continue
		}
		seen[mt.TankID] = struct{}{}
		tank, err := h.store.GetTank(ctx, mt.TankID)
		if err != nil {
			log.Printf("get admin-roster tank %s: %v — skipping", mt.TankID, err)
			continue
		}
		// Synthesise a minimal TankVersion so the ranking sort has a WinRate field.
		ver := db.TankVersion{TankID: mt.TankID, Version: mt.Version}
		entries = append(entries, ranked{tank: tank, version: ver})
	}

	log.Printf("registration_close: game day %s — %d self-registered version(s), %d admin-roster entry(ies), %d merged total",
		gd.GameDayID, len(versions), len(gd.RegisteredTanks), len(entries))

	// Sort by global rank: globalScore desc, bestFinish asc (nil = worst),
	// gameDaysCount desc, winRate desc, random for remaining ties.
	rand.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i].tank, entries[j].tank
		if a.GlobalScore != b.GlobalScore {
			return a.GlobalScore > b.GlobalScore
		}
		// Tanks with no finish rank below those with a finish.
		if (a.BestFinish == nil) != (b.BestFinish == nil) {
			return b.BestFinish == nil // a has a finish, b doesn't → a ranks higher
		}
		if a.BestFinish != nil && *a.BestFinish != *b.BestFinish {
			return *a.BestFinish < *b.BestFinish // lower placement number = better
		}
		if a.GameDaysCount != b.GameDaysCount {
			return a.GameDaysCount > b.GameDaysCount
		}
		return entries[i].version.WinRate > entries[j].version.WinRate
	})

	tanks := make([]db.MatchTank, len(entries))
	for i, e := range entries {
		tanks[i] = db.MatchTank{TankID: e.tank.TankID, Version: e.version.Version, TankName: e.tank.Name}
	}

	if gd.Autofill && h.scoutTankID != "" && h.bruiserTankID != "" {
		target := nextPowerOf2(len(tanks))
		if target < 8 {
			target = 8
		}
		scoutName, bruiserName := "Scout", "Bruiser"
		if t, err := h.store.GetTank(ctx, h.scoutTankID); err == nil {
			scoutName = t.Name
		}
		if t, err := h.store.GetTank(ctx, h.bruiserTankID); err == nil {
			bruiserName = t.Name
		}
		bots := []db.MatchTank{
			{TankID: h.scoutTankID, Version: h.scoutVersion, TankName: scoutName},
			{TankID: h.bruiserTankID, Version: h.bruiserVersion, TankName: bruiserName},
		}
		for i := 0; len(tanks) < target; i++ {
			tanks = append(tanks, bots[i%len(bots)])
		}
		log.Printf("autofill: padded to %d tanks for game day %s", len(tanks), gd.GameDayID)
	}

	// Cancel the tournament only after autofill — bots count toward the minimum.
	if len(tanks) == 0 {
		log.Printf("no tanks registered for game day %s (self-registered: %d, admin-roster: %d) — cancelling",
			gd.GameDayID, len(versions), len(gd.RegisteredTanks))
		gd.Phases.RoundRobin.Status = "cancelled"
		gd.Phases.Final.Status = "cancelled"
		for k, p := range gd.Phases.Elimination {
			p.Status = "cancelled"
			gd.Phases.Elimination[k] = p
		}
		return h.store.PutGameDay(ctx, gd)
	}

	gd.RegisteredTanks = tanks
	return h.store.PutGameDay(ctx, gd)
}

// ---- Phase: round_robin -----------------------------------------------------

// handleRoundRobin assigns groups via pot seeding, creates all RR matches, and
// invokes match-runner for each one asynchronously.
func (h *handler) handleRoundRobin(ctx context.Context, gd db.GameDay) error {
	if gd.Phases.RoundRobin.Status == "running" || gd.Phases.RoundRobin.Status == "complete" {
		log.Printf("round_robin already %s for game day %s", gd.Phases.RoundRobin.Status, gd.GameDayID)
		return nil
	}
	if gd.Phases.RoundRobin.Status == "cancelled" {
		log.Printf("round_robin cancelled for game day %s — tournament was cancelled at registration_close", gd.GameDayID)
		return nil
	}
	if len(gd.RegisteredTanks) == 0 {
		return fmt.Errorf("no registered tanks for game day %s (registration_close must run first)", gd.GameDayID)
	}

	numGroups := max1((len(gd.RegisteredTanks) + maxGroupSize - 1) / maxGroupSize)
	grouped := potSeed(gd.RegisteredTanks, numGroups)

	groups := make([]db.Group, len(grouped))
	for i, tanks := range grouped {
		standings := make([]db.GroupStanding, len(tanks))
		for j, t := range tanks {
			standings[j] = db.GroupStanding{TankID: t.TankID, Version: t.Version, TankName: t.TankName}
		}
		groups[i] = db.Group{
			GroupID:   newUUID(),
			Tanks:     tanks,
			Standings: standings,
		}
	}

	now := time.Now().Unix()
	gd.Groups = groups
	gd.Phases.RoundRobin = db.PhaseStatus{Status: "running", StartedAt: now}
	if err := h.store.PutGameDay(ctx, gd); err != nil {
		return fmt.Errorf("save groups: %w", err)
	}

	// Create and invoke all round-robin matches.
	for _, grp := range groups {
		if err := h.createGroupMatches(ctx, gd, grp.Tanks); err != nil {
			return fmt.Errorf("create group matches: %w", err)
		}
	}
	return nil
}

// createGroupMatches generates all pairings within a group and invokes match-runner.
func (h *handler) createGroupMatches(ctx context.Context, gd db.GameDay, tanks []db.MatchTank) error {
	for i := 0; i < len(tanks); i++ {
		for j := i + 1; j < len(tanks); j++ {
			matchID, err := h.createRankedMatch(ctx, gd, tanks[i], tanks[j])
			if err != nil {
				return err
			}
			h.invokeMatchRunner(ctx, matchID, false)
		}
	}
	return nil
}

// ---- Phase: elimination_r1 --------------------------------------------------

// handleEliminationR1 checks that all RR matches are done, computes group
// standings, qualifies tanks, globally re-ranks them, seeds the bracket, and
// creates round-1 elimination matches.
func (h *handler) handleEliminationR1(ctx context.Context, gd db.GameDay) error {
	if gd.Phases.RoundRobin.Status != "running" {
		log.Printf("round_robin not yet running for %s — skipping elimination_r1", gd.GameDayID)
		return nil
	}
	if elim, ok := gd.Phases.Elimination["r1"]; ok && (elim.Status == "running" || elim.Status == "complete") {
		log.Printf("elimination_r1 already %s for %s", elim.Status, gd.GameDayID)
		return nil
	}

	matches, err := h.store.ScanMatchesByGameDay(ctx, gd.GameDayID)
	if err != nil {
		return fmt.Errorf("scan matches: %w", err)
	}
	if !allMatchesEnded(matches) {
		log.Printf("not all RR matches ended for %s — skipping", gd.GameDayID)
		return nil
	}

	// Compute standings and qualify tanks.
	qualifiers := qualifyTanks(gd.Groups, matches, len(gd.RegisteredTanks))

	// Update group standings in the game day record.
	for i, grp := range gd.Groups {
		gd.Groups[i].Standings = computeGroupStandings(grp.Tanks, matches)
	}

	// Globally re-rank qualifiers.
	globalRerank(qualifiers, gd.Groups, matches)

	// Store derived elimination schedule times based on actual qualifier count.
	if finalAt, parseErr := time.Parse(time.RFC3339, gd.Schedule.Final); parseErr == nil {
		numRounds := 0
		for q := nextPowerOf2(len(qualifiers)); q > 2; q >>= 1 {
			numRounds++
		}
		if numRounds == 0 {
			numRounds = 1
		}
		elim := make([]string, numRounds)
		for i := 0; i < numRounds; i++ {
			elim[i] = finalAt.Add(-time.Duration(numRounds-i) * 30 * time.Minute).UTC().Format(time.RFC3339)
		}
		gd.Schedule.Elimination = elim

		// Push the final forward if the last elimination round + 30 min buffer
		// would overlap it. Admin-set finalAt is a floor, never a ceiling.
		if len(elim) > 0 {
			if lastElimAt, lerr := time.Parse(time.RFC3339, elim[len(elim)-1]); lerr == nil {
				candidate := lastElimAt.Add(30 * time.Minute)
				if candidate.After(finalAt) {
					log.Printf("auto-advancing finalAt %s → %s for game day %s",
						finalAt.Format(time.RFC3339), candidate.UTC().Format(time.RFC3339), gd.GameDayID)
					gd.Schedule.Final = candidate.UTC().Format(time.RFC3339)
					h.rescheduleFinal(ctx, gd)
				}
			}
		}
	}

	// Seed the bracket.
	n := len(qualifiers)
	if n == 0 {
		log.Printf("no qualifiers for %s", gd.GameDayID)
		return nil
	}
	p := nextPowerOf2(n)
	order := seedBracketOrder(p)
	slots := make([]db.BracketSlot, p)
	for pos, seed := range order {
		if seed <= n {
			slots[pos] = db.BracketSlot{
				TankID:   qualifiers[seed-1].TankID,
				Version:  qualifiers[seed-1].Version,
				Status:   "playing",
				TankName: qualifiers[seed-1].TankName,
			}
		} else {
			slots[pos] = db.BracketSlot{Status: "bye"}
		}
	}

	// Auto-advance tanks whose opponent is an empty bye slot.
	for i := 0; i+1 < len(slots); i += 2 {
		a, b := &slots[i], &slots[i+1]
		if a.TankID != "" && b.TankID == "" {
			a.Status = "won"
		} else if b.TankID != "" && a.TankID == "" {
			b.Status = "won"
		}
	}

	now := time.Now().Unix()
	gd.Phases.RoundRobin.Status = "complete"
	gd.Phases.RoundRobin.EndedAt = now
	if gd.Phases.Elimination == nil {
		gd.Phases.Elimination = make(map[string]db.PhaseStatus)
	}
	gd.Phases.Elimination["r1"] = db.PhaseStatus{Status: "running", StartedAt: now}
	if gd.Bracket == nil {
		gd.Bracket = make(map[string][]db.BracketSlot)
	}
	gd.Bracket["r1"] = slots
	if err := h.store.PutGameDay(ctx, gd); err != nil {
		return fmt.Errorf("save r1 bracket: %w", err)
	}

	return h.createElimMatches(ctx, gd, "r1", slots)
}

// ---- Phase: elimination_r{N} (N ≥ 2) ---------------------------------------

// handleElimination advances the bracket from the previous elimination round
// and creates matches for the current round.
func (h *handler) handleElimination(ctx context.Context, gd db.GameDay, round int) error {
	prevKey := fmt.Sprintf("r%d", round-1)
	curKey := fmt.Sprintf("r%d", round)

	prevPhase, ok := gd.Phases.Elimination[prevKey]
	if !ok || prevPhase.Status != "running" {
		log.Printf("%s not yet running for %s — skipping %s", prevKey, gd.GameDayID, curKey)
		return nil
	}
	if cur, ok := gd.Phases.Elimination[curKey]; ok && (cur.Status == "running" || cur.Status == "complete") {
		log.Printf("%s already %s for %s", curKey, cur.Status, gd.GameDayID)
		return nil
	}

	prevSlots, ok := gd.Bracket[prevKey]
	if !ok {
		return fmt.Errorf("bracket %s not found for %s", prevKey, gd.GameDayID)
	}

	// Check if only 1 match is left in the prev round — if so, this elimination
	// round isn't needed (the final trigger handles the championship).
	if activePairs(prevSlots) <= 1 {
		log.Printf("%s has ≤1 active pair — skipping %s (let final handle it)", prevKey, curKey)
		return nil
	}

	matches, err := h.store.ScanMatchesByGameDay(ctx, gd.GameDayID)
	if err != nil {
		return fmt.Errorf("scan matches: %w", err)
	}

	updatedPrev, allDone := updateSlotsFromMatches(prevSlots, matches)
	if !allDone {
		log.Printf("not all %s matches ended for %s — skipping", prevKey, gd.GameDayID)
		return nil
	}

	nextSlots := advanceBracketRound(updatedPrev)

	now := time.Now().Unix()
	gd.Phases.Elimination[prevKey] = db.PhaseStatus{
		Status:    "complete",
		StartedAt: prevPhase.StartedAt,
		EndedAt:   now,
	}
	gd.Phases.Elimination[curKey] = db.PhaseStatus{Status: "running", StartedAt: now}
	gd.Bracket[prevKey] = updatedPrev
	gd.Bracket[curKey] = nextSlots
	if err := h.store.PutGameDay(ctx, gd); err != nil {
		return fmt.Errorf("save %s bracket: %w", curKey, err)
	}

	return h.createElimMatches(ctx, gd, curKey, nextSlots)
}

// ---- Phase: final -----------------------------------------------------------

// handleFinal runs the championship: if the finalists are determined it invokes
// match-runner synchronously, then invokes ranking-updater. A bye-champion skips
// the match and goes directly to ranking-updater.
func (h *handler) handleFinal(ctx context.Context, gd db.GameDay) error {
	// Find the last running elimination round to get finalists.
	lastRoundKey, lastPhase, lastSlots := lastEliminationRound(gd)
	if lastRoundKey == "" {
		log.Printf("no elimination round in progress for %s — skipping final", gd.GameDayID)
		return nil
	}
	if lastPhase.Status == "complete" {
		log.Printf("final already processed for %s", gd.GameDayID)
		return nil
	}

	matches, err := h.store.ScanMatchesByGameDay(ctx, gd.GameDayID)
	if err != nil {
		return fmt.Errorf("scan matches: %w", err)
	}

	updated, allDone := updateSlotsFromMatches(lastSlots, matches)
	if !allDone {
		log.Printf("not all %s matches ended for %s — skipping final", lastRoundKey, gd.GameDayID)
		return nil
	}

	finalists := advanceBracketRound(updated)
	// finalists has exactly 2 slots for the championship match.
	if len(finalists) < 2 {
		log.Printf("unexpected finalist count %d for %s", len(finalists), gd.GameDayID)
		return nil
	}
	a, b := finalists[0], finalists[1]

	now := time.Now().Unix()
	gd.Phases.Elimination[lastRoundKey] = db.PhaseStatus{
		Status:    "complete",
		StartedAt: lastPhase.StartedAt,
		EndedAt:   now,
	}
	gd.Bracket[lastRoundKey] = updated
	gd.Phases.Final = db.PhaseStatus{Status: "running", StartedAt: now}

	// Determine the championship outcome without a match if possible.
	switch {
	case a.TankID == "" && b.TankID == "":
		// No survivors at all — no champion.
		log.Printf("no champion for game day %s (all both-lose)", gd.GameDayID)
		gd.Phases.Final.Status = "complete"
		gd.Phases.Final.EndedAt = now
		if err := h.store.PutGameDay(ctx, gd); err != nil {
			return err
		}
		return h.invokeRankingUpdater(ctx, gd.GameDayID)

	case a.TankID == "":
		// b is champion by bye.
		log.Printf("champion by bye: %s/%s", b.TankID, b.Version)
		gd.Phases.Final.Status = "complete"
		gd.Phases.Final.EndedAt = now
		if err := h.store.PutGameDay(ctx, gd); err != nil {
			return err
		}
		return h.invokeRankingUpdater(ctx, gd.GameDayID)

	case b.TankID == "":
		// a is champion by bye.
		log.Printf("champion by bye: %s/%s", a.TankID, a.Version)
		gd.Phases.Final.Status = "complete"
		gd.Phases.Final.EndedAt = now
		if err := h.store.PutGameDay(ctx, gd); err != nil {
			return err
		}
		return h.invokeRankingUpdater(ctx, gd.GameDayID)
	}

	// Both finalists are real — run the championship match synchronously so we
	// can invoke ranking-updater after it completes.
	if err := h.store.PutGameDay(ctx, gd); err != nil {
		return err
	}

	matchID, err := h.createRankedMatch(ctx, gd,
		db.MatchTank{TankID: a.TankID, Version: a.Version},
		db.MatchTank{TankID: b.TankID, Version: b.Version})
	if err != nil {
		return fmt.Errorf("create final match: %w", err)
	}

	log.Printf("invoking final match %s synchronously", matchID)
	h.invokeMatchRunner(ctx, matchID, true /* synchronous */)

	// Mark final complete and invoke ranking-updater.
	gd, err = h.store.GetGameDay(ctx, gd.GameDayID)
	if err != nil {
		return err
	}
	gd.Phases.Final.Status = "complete"
	gd.Phases.Final.EndedAt = time.Now().Unix()
	if err := h.store.PutGameDay(ctx, gd); err != nil {
		return err
	}
	return h.invokeRankingUpdater(ctx, gd.GameDayID)
}

// ---- Bracket helpers --------------------------------------------------------

// seedBracketOrder returns the ordered list of seed numbers for a bracket of
// size p (must be a power of 2) using standard best-vs-worst seeding.
// seedBracketOrder(8) → [1, 8, 4, 5, 2, 7, 3, 6]
func seedBracketOrder(p int) []int {
	if p == 1 {
		return []int{1}
	}
	half := seedBracketOrder(p / 2)
	seeds := make([]int, p)
	for i, s := range half {
		seeds[2*i] = s
		seeds[2*i+1] = p + 1 - s
	}
	return seeds
}

// advanceBracketRound builds the next round's slots from the current round's
// results. Each pair (2i, 2i+1) produces one slot for the next round.
func advanceBracketRound(slots []db.BracketSlot) []db.BracketSlot {
	next := make([]db.BracketSlot, len(slots)/2)
	for i := 0; i+1 < len(slots); i += 2 {
		next[i/2] = winnerSlot(slots[i], slots[i+1])
	}
	return next
}

// winnerSlot returns the slot that should advance from a bracket pair.
func winnerSlot(a, b db.BracketSlot) db.BracketSlot {
	aReal := a.TankID != ""
	bReal := b.TankID != ""
	adv := func(s db.BracketSlot) db.BracketSlot {
		return db.BracketSlot{TankID: s.TankID, Version: s.Version, Status: "playing", TankName: s.TankName}
	}
	switch {
	case !aReal && !bReal:
		return db.BracketSlot{Status: "bye"}
	case !aReal:
		return adv(b)
	case !bReal:
		return adv(a)
	case a.Status == "won":
		return adv(a)
	case b.Status == "won":
		return adv(b)
	case a.Status == "both_lose" && b.Status == "both_lose":
		return db.BracketSlot{Status: "bye"}
	case a.Status == "both_lose":
		return adv(b)
	case b.Status == "both_lose":
		return adv(a)
	}
	return db.BracketSlot{Status: "bye"}
}

// updateSlotsFromMatches fills in won/lost/both_lose statuses on bracket slots
// by looking up match results. Returns the updated slots and whether all pairs
// with real-tank opponents have been resolved.
func updateSlotsFromMatches(slots []db.BracketSlot, matches []db.Match) ([]db.BracketSlot, bool) {
	updated := make([]db.BracketSlot, len(slots))
	copy(updated, slots)
	allDone := true

	for i := 0; i+1 < len(slots); i += 2 {
		a := &updated[i]
		b := &updated[i+1]

		// Already resolved.
		if a.Status != "playing" && b.Status != "playing" {
			continue
		}

		// Auto-advance when one side is empty.
		if a.TankID == "" {
			if b.Status == "playing" {
				b.Status = "won"
			}
			continue
		}
		if b.TankID == "" {
			if a.Status == "playing" {
				a.Status = "won"
			}
			continue
		}

		// Both real — look up the match result.
		m := findMatchForPair(matches, a.TankID, b.TankID)
		if m == nil || m.Status != "ended" || m.Result == nil {
			allDone = false
			continue
		}
		if m.Result.Reason == "both_lose" {
			a.Status = "both_lose"
			b.Status = "both_lose"
			continue
		}
		if m.Result.Winner == nil {
			allDone = false
			continue
		}
		// Determine which slot corresponds to TankA / TankB in the match.
		if *m.Result.Winner == 0 { // TankA won
			if m.TankA.TankID == a.TankID {
				a.Status = "won"
				b.Status = "lost"
			} else {
				b.Status = "won"
				a.Status = "lost"
			}
		} else { // TankB won
			if m.TankB.TankID == a.TankID {
				a.Status = "won"
				b.Status = "lost"
			} else {
				b.Status = "won"
				a.Status = "lost"
			}
		}
	}
	return updated, allDone
}

// createElimMatches creates match records and invokes match-runner for each
// pair of real tanks in the given bracket round's slots.
func (h *handler) createElimMatches(ctx context.Context, gd db.GameDay, roundKey string, slots []db.BracketSlot) error {
	for i := 0; i+1 < len(slots); i += 2 {
		a, b := slots[i], slots[i+1]
		if a.TankID == "" || b.TankID == "" {
			continue // bye pair — no match needed
		}
		if a.Status != "playing" || b.Status != "playing" {
			continue // already auto-resolved
		}
		matchID, err := h.createRankedMatch(ctx, gd,
			db.MatchTank{TankID: a.TankID, Version: a.Version},
			db.MatchTank{TankID: b.TankID, Version: b.Version})
		if err != nil {
			return fmt.Errorf("create %s match %d/%d: %w", roundKey, i, i+1, err)
		}
		h.invokeMatchRunner(ctx, matchID, false)
	}
	return nil
}

// activePairs returns the number of pairs in slots where both sides are real tanks.
func activePairs(slots []db.BracketSlot) int {
	n := 0
	for i := 0; i+1 < len(slots); i += 2 {
		if slots[i].TankID != "" && slots[i+1].TankID != "" {
			n++
		}
	}
	return n
}

// lastEliminationRound returns the key, phase, and slots for the last started
// elimination round. Returns empty key if none found.
func lastEliminationRound(gd db.GameDay) (string, db.PhaseStatus, []db.BracketSlot) {
	last := 0
	for key := range gd.Phases.Elimination {
		if strings.HasPrefix(key, "r") {
			if n, err := strconv.Atoi(key[1:]); err == nil && n > last {
				last = n
			}
		}
	}
	if last == 0 {
		return "", db.PhaseStatus{}, nil
	}
	key := fmt.Sprintf("r%d", last)
	return key, gd.Phases.Elimination[key], gd.Bracket[key]
}

// ---- Round-robin helpers ----------------------------------------------------

// computeGroupStandings derives wins, losses, and points for each tank in a
// group from the set of all game day matches.
func computeGroupStandings(tanks []db.MatchTank, matches []db.Match) []db.GroupStanding {
	// points: win=1, flawless win=2, both_lose=0, loss=0
	type stats struct{ wins, losses, points int }
	sm := make(map[string]*stats, len(tanks))
	for _, t := range tanks {
		sm[t.TankID] = &stats{}
	}

	for i := range matches {
		m := &matches[i]
		if m.Result == nil {
			continue
		}
		aInGroup := sm[m.TankA.TankID] != nil
		bInGroup := sm[m.TankB.TankID] != nil
		if !aInGroup || !bInGroup {
			continue
		}
		switch {
		case m.Result.Reason == "both_lose":
			sm[m.TankA.TankID].losses++
			sm[m.TankB.TankID].losses++
		case m.Result.Winner != nil && *m.Result.Winner == 0:
			sm[m.TankA.TankID].wins++
			pts := 1
			if m.Result.Flawless {
				pts = 2
			}
			sm[m.TankA.TankID].points += pts
			sm[m.TankB.TankID].losses++
		case m.Result.Winner != nil && *m.Result.Winner == 1:
			sm[m.TankB.TankID].wins++
			pts := 1
			if m.Result.Flawless {
				pts = 2
			}
			sm[m.TankB.TankID].points += pts
			sm[m.TankA.TankID].losses++
		}
	}

	standings := make([]db.GroupStanding, len(tanks))
	for i, t := range tanks {
		s := sm[t.TankID]
		standings[i] = db.GroupStanding{
			TankID:   t.TankID,
			Version:  t.Version,
			TankName: t.TankName,
			Wins:     s.wins,
			Losses:   s.losses,
			Points:   s.points,
		}
	}
	return standings
}

// qualifyTanks determines which tanks advance to the elimination bracket from
// the round-robin groups. Returns the qualified tanks in their original group order.
func qualifyTanks(groups []db.Group, matches []db.Match, totalTanks int) []db.MatchTank {
	var qualified []db.MatchTank
	for _, grp := range groups {
		standings := computeGroupStandings(grp.Tanks, matches)
		// Compute aggregate stats for tiebreaking.
		type augmented struct {
			db.GroupStanding
			dmg   int
			moves int
		}
		aug := make([]augmented, len(standings))
		for i, s := range standings {
			aug[i] = augmented{GroupStanding: s}
		}
		for idx := range matches {
			m := &matches[idx]
			if m.Result == nil {
				continue
			}
			for i := range aug {
				switch aug[i].TankID {
				case m.TankA.TankID:
					aug[i].dmg += m.Result.DamageA
					aug[i].moves += m.Result.MovesA
				case m.TankB.TankID:
					aug[i].dmg += m.Result.DamageB
					aug[i].moves += m.Result.MovesB
				}
			}
		}

		rand.Shuffle(len(aug), func(i, j int) { aug[i], aug[j] = aug[j], aug[i] })
		sort.SliceStable(aug, func(i, j int) bool {
			if aug[i].Points != aug[j].Points {
				return aug[i].Points > aug[j].Points
			}
			if aug[i].dmg != aug[j].dmg {
				return aug[i].dmg > aug[j].dmg
			}
			return aug[i].moves > aug[j].moves
		})

		n := len(grp.Tanks)
		advN := n // all advance if totalTanks ≤ 64
		if totalTanks > 64 {
			advN = (n * 2) / 3
		}
		for _, a := range aug[:advN] {
			qualified = append(qualified, db.MatchTank{TankID: a.TankID, Version: a.Version, TankName: a.TankName})
		}
	}
	return qualified
}

// globalRerank sorts qualifiers by their round-robin performance for elimination
// seeding: points desc, damage dealt desc, moves desc, then random.
func globalRerank(qualifiers []db.MatchTank, groups []db.Group, matches []db.Match) {
	// Build a points+damage+moves map across all groups.
	type totals struct{ pts, dmg, moves int }
	tm := make(map[string]totals, len(qualifiers))
	for _, grp := range groups {
		standings := computeGroupStandings(grp.Tanks, matches)
		for _, s := range standings {
			tm[s.TankID] = totals{pts: s.Points}
		}
	}
	for i := range matches {
		m := &matches[i]
		if m.Result == nil {
			continue
		}
		if t, ok := tm[m.TankA.TankID]; ok {
			t.dmg += m.Result.DamageA
			t.moves += m.Result.MovesA
			tm[m.TankA.TankID] = t
		}
		if t, ok := tm[m.TankB.TankID]; ok {
			t.dmg += m.Result.DamageB
			t.moves += m.Result.MovesB
			tm[m.TankB.TankID] = t
		}
	}

	rand.Shuffle(len(qualifiers), func(i, j int) { qualifiers[i], qualifiers[j] = qualifiers[j], qualifiers[i] })
	sort.SliceStable(qualifiers, func(i, j int) bool {
		a, b := tm[qualifiers[i].TankID], tm[qualifiers[j].TankID]
		if a.pts != b.pts {
			return a.pts > b.pts
		}
		if a.dmg != b.dmg {
			return a.dmg > b.dmg
		}
		return a.moves > b.moves
	})
}

// ---- Seeding / grouping helpers ---------------------------------------------

// potSeed distributes tanks into numGroups groups using pot seeding.
// Tanks are expected to be pre-sorted by global rank.
// Each pot of numGroups tanks is shuffled before assignment, ensuring every
// group receives one tank from each rank tier.
func potSeed(tanks []db.MatchTank, numGroups int) [][]db.MatchTank {
	groups := make([][]db.MatchTank, numGroups)
	for start := 0; start < len(tanks); start += numGroups {
		end := start + numGroups
		if end > len(tanks) {
			end = len(tanks)
		}
		pot := make([]db.MatchTank, end-start)
		copy(pot, tanks[start:end])
		rand.Shuffle(len(pot), func(i, j int) { pot[i], pot[j] = pot[j], pot[i] })
		for i, t := range pot {
			groups[i] = append(groups[i], t)
		}
	}
	return groups
}

// nextPowerOf2 returns the smallest power of 2 that is ≥ n.
func nextPowerOf2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// max1 returns the maximum of x and 1.
func max1(x int) int {
	if x < 1 {
		return 1
	}
	return x
}

// ---- Match / invocation helpers ---------------------------------------------

// createRankedMatch writes a ranked match record and returns the new matchId.
func (h *handler) createRankedMatch(ctx context.Context, gd db.GameDay, a, b db.MatchTank) (string, error) {
	matchID := newUUID()
	match := db.Match{
		MatchID:   matchID,
		MatchType: "ranked",
		GameDayID: gd.GameDayID,
		Status:    "scheduled",
		TankA:     a,
		TankB:     b,
		CreatedAt: time.Now().Unix(),
	}
	if len(gd.ForcedMapIDs) > 0 {
		match.MapID = gd.ForcedMapIDs[rand.Intn(len(gd.ForcedMapIDs))]
	} else {
		match.MazeSeed = strconv.FormatInt(rand.Int63(), 10)
	}
	if err := h.store.PutMatch(ctx, match); err != nil {
		return "", fmt.Errorf("put match: %w", err)
	}
	return matchID, nil
}

// invokeMatchRunner invokes the match-runner Lambda for the given matchId.
// Pass sync=true for the final match to block until it completes.
func (h *handler) invokeMatchRunner(ctx context.Context, matchID string, sync bool) {
	payload, _ := json.Marshal(map[string]string{"matchId": matchID})
	invType := ltypes.InvocationTypeEvent
	if sync {
		invType = ltypes.InvocationTypeRequestResponse
	}
	if _, err := h.lambdaSvc.Invoke(ctx, &lambdasvc.InvokeInput{
		FunctionName:   aws.String(h.matchRunnerFunc),
		InvocationType: invType,
		Payload:        payload,
	}); err != nil {
		log.Printf("invoke match-runner for %s: %v", matchID, err)
	}
}

// rescheduleFinal updates the EventBridge Scheduler rule for the final phase
// to fire at the new time stored in gd.Schedule.Final. No-ops gracefully when
// the scheduler client or role ARN env vars are missing (e.g. in tests).
func (h *handler) rescheduleFinal(ctx context.Context, gd db.GameDay) {
	if h.schedulerSvc == nil || h.schedulerRoleArn == "" || h.selfArn == "" {
		log.Printf("rescheduleFinal: scheduler not configured — skipping EventBridge update")
		return
	}
	finalAt, err := time.Parse(time.RFC3339, gd.Schedule.Final)
	if err != nil {
		log.Printf("rescheduleFinal: parse finalAt: %v", err)
		return
	}
	scheduleName := gd.GameDayID + "-final"
	payload, _ := json.Marshal(map[string]string{"gameDayId": gd.GameDayID, "phase": "final"})
	atExpr := "at(" + finalAt.UTC().Format("2006-01-02T15:04:05") + ")"
	target := &schedulertypes.Target{
		Arn:     aws.String(h.selfArn),
		RoleArn: aws.String(h.schedulerRoleArn),
		Input:   aws.String(string(payload)),
	}
	if h.schedulerDLQArn != "" {
		target.DeadLetterConfig = &schedulertypes.DeadLetterConfig{Arn: aws.String(h.schedulerDLQArn)}
	}
	if _, err := h.schedulerSvc.UpdateSchedule(ctx, &schedulersvc.UpdateScheduleInput{
		Name:                       aws.String(scheduleName),
		GroupName:                  aws.String("tankmaze-gamedays"),
		ScheduleExpression:         aws.String(atExpr),
		ScheduleExpressionTimezone: aws.String("UTC"),
		FlexibleTimeWindow: &schedulertypes.FlexibleTimeWindow{
			Mode: schedulertypes.FlexibleTimeWindowModeOff,
		},
		Target:                target,
		ActionAfterCompletion: schedulertypes.ActionAfterCompletionDelete,
	}); err != nil {
		log.Printf("rescheduleFinal %s: %v", scheduleName, err)
	}
}

// invokeRankingUpdater invokes the ranking-updater Lambda asynchronously.
func (h *handler) invokeRankingUpdater(ctx context.Context, gameDayID string) error {
	payload, _ := json.Marshal(map[string]string{"gameDayId": gameDayID})
	if _, err := h.lambdaSvc.Invoke(ctx, &lambdasvc.InvokeInput{
		FunctionName:   aws.String(h.rankingUpdaterFunc),
		InvocationType: ltypes.InvocationTypeEvent,
		Payload:        payload,
	}); err != nil {
		log.Printf("invoke ranking-updater for %s: %v", gameDayID, err)
	}
	return nil
}

// ---- Misc helpers -----------------------------------------------------------

// findMatchForPair searches matches for one with the given tank IDs on either side.
func findMatchForPair(matches []db.Match, tankA, tankB string) *db.Match {
	for i := range matches {
		m := &matches[i]
		if (m.TankA.TankID == tankA && m.TankB.TankID == tankB) ||
			(m.TankA.TankID == tankB && m.TankB.TankID == tankA) {
			return m
		}
	}
	return nil
}

// allMatchesEnded returns true if every match in the slice has Status="ended".
func allMatchesEnded(matches []db.Match) bool {
	for i := range matches {
		if matches[i].Status != "ended" {
			return false
		}
	}
	return true
}

// newUUID generates a random UUID v4.
func newUUID() string {
	var b [16]byte
	_, _ = crand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

