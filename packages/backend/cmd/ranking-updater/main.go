package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/tankmaze/backend/internal/db"
)

const rankingTTL = 365 * 24 * time.Hour

type event struct {
	GameDayID string `json:"gameDayId"`
}

type handler struct {
	store *db.Store
}

// tankTier holds the computed tier (k) and ordinal placement for one tank.
// Tier k: 1=champion, 2=runner-up / final both-lose, 3=semi-final loser, …
// Points formula: k=1→n; k≥2→floor(n/2^(k−1)).
type tankTier struct {
	k         int
	placement int // ordinal: 1, 2, 3, 5, 9, …
}

func main() {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		panic(fmt.Sprintf("load aws config: %v", err))
	}
	h := &handler{store: db.New(dynamodb.NewFromConfig(cfg))}
	lambda.Start(h.handle)
}

func (h *handler) handle(ctx context.Context, evt event) error {
	gd, err := h.store.GetGameDay(ctx, evt.GameDayID)
	if err != nil {
		return fmt.Errorf("get game day: %w", err)
	}
	n := len(gd.RegisteredTanks)
	if n == 0 {
		return nil
	}

	tiers, championNull := bracketTiers(gd.Bracket)

	now := time.Now()
	expiresAt := now.Add(rankingTTL).Unix()
	// pointsMap stays keyed by the (possibly autofill-suffixed, item 248) tank
	// identity used within this event's own bookkeeping — it becomes
	// gd.PlacementPoints below, which the frontend needs per-instance to show
	// each duplicated built-in AI's own real placement (item 249). The
	// permanent db.Ranking rows below are different: they must use the real
	// tank ID (db.RealTankID) since a synthetic "builtin-scout#2" is never a
	// real Tank record — see db.RealTankID's doc comment for the full
	// rationale, including why two instances of the same built-in AI still
	// share one permanent ranking history (last-write-wins, same as before
	// this fix), same as they always have.
	pointsMap := make(map[string]int, n)

	// Write ranking records for all bracket participants.
	for tankID, tier := range tiers {
		if championNull && tier.k <= 2 {
			// Both finalists in a both-lose final get no ranking entry.
			continue
		}
		pts := placementPoints(n, tier.k)
		if err := h.store.PutRanking(ctx, db.Ranking{
			TankID:    db.RealTankID(tankID),
			GameDayID: evt.GameDayID,
			Points:    pts,
			Placement: tier.placement,
			ExpiresAt: expiresAt,
			TTL:       expiresAt,
		}); err != nil {
			return fmt.Errorf("put ranking %s: %w", tankID, err)
		}
		pointsMap[tankID] = pts
	}

	// Tanks that participated in round-robin but did not qualify to the bracket
	// receive 0 points and share the last placement tier.
	lastPlacement := n
	for _, mt := range gd.RegisteredTanks {
		if _, inBracket := tiers[mt.TankID]; inBracket {
			continue
		}
		if err := h.store.PutRanking(ctx, db.Ranking{
			TankID:    db.RealTankID(mt.TankID),
			GameDayID: evt.GameDayID,
			Points:    0,
			Placement: lastPlacement,
			ExpiresAt: expiresAt,
			TTL:       expiresAt,
		}); err != nil {
			return fmt.Errorf("put ranking rr-only %s: %w", mt.TankID, err)
		}
		pointsMap[mt.TankID] = 0
	}

	if err := h.store.SetGameDayPlacementPoints(ctx, evt.GameDayID, pointsMap); err != nil {
		return fmt.Errorf("set placement points: %w", err)
	}

	// Dedupe to real tank IDs — pointsMap may hold more than one
	// autofill-suffixed key (item 248) for the same real built-in AI.
	realIDs := make(map[string]struct{}, len(pointsMap))
	for tankID := range pointsMap {
		realIDs[db.RealTankID(tankID)] = struct{}{}
	}
	for tankID := range realIDs {
		if err := h.recomputeTankStats(ctx, tankID, now); err != nil {
			return fmt.Errorf("recompute stats %s: %w", tankID, err)
		}
	}
	return nil
}

// bracketTiers assigns a tier and ordinal placement to every real tank that
// appears in the elimination bracket. It returns championNull=true when no tank
// holds the champion (won) slot in the final round — either because the final
// ended both-lose or because no real tanks reached the final.
func bracketTiers(bracket map[string][]db.BracketSlot) (map[string]tankTier, bool) {
	if len(bracket) == 0 {
		return nil, true
	}

	rounds := make([]int, 0, len(bracket))
	for key := range bracket {
		r, _ := strconv.Atoi(strings.TrimPrefix(key, "r"))
		rounds = append(rounds, r)
	}
	sort.Ints(rounds)
	totalRounds := rounds[len(rounds)-1]

	tiers := make(map[string]tankTier)
	championNull := true

	for _, r := range rounds {
		key := "r" + strconv.Itoa(r)
		for _, slot := range bracket[key] {
			if slot.TankID == "" {
				continue // virtual bye
			}
			switch slot.Status {
			case "won":
				if r != totalRounds {
					// Tank continues to the next round; record it there instead.
					continue
				}
				tiers[slot.TankID] = tankTier{k: 1, placement: 1}
				championNull = false
			case "lost":
				k := finalRoundK(r, totalRounds)
				tiers[slot.TankID] = tankTier{k: k, placement: tierPlacement(k)}
			case "both_lose":
				k := finalRoundK(r, totalRounds)
				tiers[slot.TankID] = tankTier{k: k, placement: tierPlacement(k)}
				// A both-lose in the final means no champion.
				if r == totalRounds {
					championNull = true
				}
			}
			// "bye" and "playing" slots need no action.
		}
	}

	// The "final" key is not parsed by the Atoi loop above (Atoi("final")=0 maps
	// to bracket["r0"] which never exists). Process it explicitly so the actual
	// championship result — whether a real match or a bye — overrides whatever
	// the last numeric round may have provisionally assigned.
	for _, slot := range bracket["final"] {
		if slot.TankID == "" {
			continue // bye slot
		}
		switch slot.Status {
		case "won":
			tiers[slot.TankID] = tankTier{k: 1, placement: 1}
			championNull = false
		case "lost":
			tiers[slot.TankID] = tankTier{k: 2, placement: 2}
		case "both_lose":
			tiers[slot.TankID] = tankTier{k: 2, placement: 2}
			championNull = true
		}
	}

	return tiers, championNull
}

// finalRoundK maps the round in which a tank was eliminated to its tier k.
// r=totalRounds → k=2 (runner-up / final both-lose)
// r=totalRounds-1 → k=3 (semi-final), etc.
func finalRoundK(r, totalRounds int) int {
	return totalRounds - r + 2
}

// tierPlacement converts tier k to the lowest ordinal placement in that tier.
// k=1→1, k=2→2, k=3→3, k=4→5, k=5→9, k=j→2^(j-2)+1 for j≥3.
func tierPlacement(k int) int {
	if k <= 2 {
		return k
	}
	return (1 << (k - 2)) + 1
}

// placementPoints computes the points earned by a tank at tier k out of n total
// participants. Champion (k=1) earns n; every other tier earns floor(n/2^(k−1)).
func placementPoints(n, k int) int {
	if k == 1 {
		return n
	}
	return n / (1 << (k - 1))
}

// recomputeTankStats fetches all ranking records for tankID and writes the
// recomputed globalScore, bestFinish, gameDaysCount, and lastActiveAt back to
// the tanks table.
func (h *handler) recomputeTankStats(ctx context.Context, tankID string, now time.Time) error {
	rankings, err := h.store.ListRankingsByTank(ctx, tankID)
	if err != nil {
		return fmt.Errorf("list rankings for %s: %w", tankID, err)
	}
	totalScore := 0
	var bestFinish *int
	for _, r := range rankings {
		totalScore += r.Points
		if r.Placement > 0 && (bestFinish == nil || r.Placement < *bestFinish) {
			bf := r.Placement
			bestFinish = &bf
		}
	}
	return h.store.UpdateTankStats(ctx, tankID, db.TankStats{
		GlobalScore:   totalScore,
		BestFinish:    bestFinish,
		GameDaysCount: len(rankings),
		LastActiveAt:  now.Unix(),
	})
}
