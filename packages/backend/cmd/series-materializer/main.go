// Package main implements the series-materializer Lambda (item 238) — the
// rolling job behind recurring Game Day series. Rather than pre-creating
// every future occurrence of a series up front, this runs on a periodic
// EventBridge Scheduler rate rule (see LEAD_TIME_DAYS below) and keeps only
// the next occurrence materialized as a real GameDay record ahead of time,
// creating the following one once that fires.
//
// A series' first occurrence is instead materialized synchronously by
// cmd/tank-api's createGameDaySeries handler, so an admin sees it in the
// Game Day list immediately rather than waiting for this job's next tick.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	schedulersvc "github.com/aws/aws-sdk-go-v2/service/scheduler"

	"github.com/tankmaze/backend/internal/db"
	"github.com/tankmaze/backend/internal/scheduling"
)

type handler struct {
	store        *db.Store
	materializer *scheduling.Materializer
	leadTime     time.Duration
}

var h *handler

func main() {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}
	leadDays := 7
	if v := os.Getenv("LEAD_TIME_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			leadDays = n
		}
	}
	store := db.New(dynamodb.NewFromConfig(cfg))
	h = &handler{
		store: store,
		materializer: &scheduling.Materializer{
			Store:                  store,
			SchedulerSvc:           schedulersvc.NewFromConfig(cfg),
			SchedulerRoleArn:       os.Getenv("SCHEDULER_INVOKE_ROLE_ARN"),
			TournamentSchedulerArn: os.Getenv("TOURNAMENT_SCHEDULER_FUNCTION"),
			SchedulerDLQArn:        os.Getenv("SCHEDULER_DLQ_ARN"),
		},
		leadTime: time.Duration(leadDays) * 24 * time.Hour,
	}
	lambda.Start(h.handle)
}

// retryOnConflict calls fn up to maxConflictRetries+1 times, retrying whenever
// db.ErrConflict is returned (optimistic-locking failure on GameDay writes).
// Duplicates cmd/tank-api's identical helper, following this repo's
// established convention of small per-binary duplicates over a shared
// cross-main-package import (see e.g. newUUID in internal/scheduling).
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

// handle runs on every scheduler tick — no meaningful input, no output.
func (h *handler) handle(ctx context.Context, _ map[string]interface{}) error {
	cutoff := time.Now().Add(h.leadTime)
	due, err := h.store.ListActiveGameDaySeriesDue(ctx, cutoff)
	if err != nil {
		return err
	}
	for _, series := range due {
		h.materializeNext(ctx, series)
	}
	return nil
}

func (h *handler) materializeNext(ctx context.Context, series db.GameDaySeries) {
	rrAt, err := time.Parse(time.RFC3339, series.NextOccurrenceAt)
	if err != nil {
		log.Printf("series %s: invalid nextOccurrenceAt %q: %v", series.SeriesID, series.NextOccurrenceAt, err)
		return
	}

	// Self-healing check: if a prior tick already materialized this exact
	// occurrence but then failed to advance the series (e.g. a transient
	// DynamoDB error right after Materialize succeeded), NextOccurrenceAt
	// would still equal this occurrence's time on the next tick. Without
	// this check that would materialize a second, duplicate GameDay for the
	// same slot every tick until it happened to succeed.
	existing, err := h.store.ListGameDays(ctx)
	if err != nil {
		log.Printf("series %s: list gamedays for dedup check: %v", series.SeriesID, err)
		return
	}
	var gd db.GameDay
	alreadyMaterialized := false
	for _, g := range existing {
		if g.SeriesID == series.SeriesID && g.Schedule.RoundRobin == series.NextOccurrenceAt {
			gd = g
			alreadyMaterialized = true
			break
		}
	}

	if !alreadyMaterialized {
		regClose := rrAt.Add(-time.Duration(series.RegistrationLeadSeconds) * time.Second)
		finalAt := rrAt.Add(time.Duration(series.FinalLeadSeconds) * time.Second)
		gd, err = h.materializer.Materialize(ctx, scheduling.Params{
			Name:                series.Name,
			RegistrationCloseAt: regClose,
			RoundRobinAt:        rrAt,
			FinalAt:             finalAt,
			Autofill:            series.Autofill,
			ForcedMapIDs:        series.ForcedMapIDs,
			RandomMaps:          series.RandomMaps,
			SeriesID:            series.SeriesID,
		})
		if err != nil {
			log.Printf("series %s: materialize occurrence at %s: %v — will retry next tick", series.SeriesID, series.NextOccurrenceAt, err)
			return
		}
		log.Printf("series %s: materialized occurrence %s at %s", series.SeriesID, gd.GameDayID, series.NextOccurrenceAt)
	} else {
		log.Printf("series %s: occurrence at %s already materialized as %s (prior tick's advance must have failed) — advancing only", series.SeriesID, series.NextOccurrenceAt, gd.GameDayID)
	}

	h.carryForwardUserTanks(ctx, series, gd, existing, rrAt)

	nextAt := db.NextOccurrenceTime(series, rrAt)
	finished := series.MaxOccurrences > 0 && series.OccurrencesCreated+1 >= series.MaxOccurrences
	if err := h.store.AdvanceGameDaySeries(ctx, series.SeriesID, series.NextOccurrenceAt, nextAt.Format(time.RFC3339), finished); err != nil {
		// The occurrence above was already created — this only affects when
		// the *next* one gets materialized. ErrConflict means another
		// invocation already advanced it (shouldn't happen with a single
		// rate rule, but the optimistic lock makes it safe either way).
		log.Printf("series %s: advance past %s: %v", series.SeriesID, series.NextOccurrenceAt, err)
	}
}

// carryForwardUserTanks (item 256) auto-registers each real, user-owned tank
// — never a builtin-* AI tank — that played the series' immediately
// preceding occurrence into the newly materialized occurrence gd, at that
// tank's CURRENT (highest major) version. The point is that a recurring
// weekly series should keep testing whatever a tank owner has most recently
// published, not silently re-run whichever version happened to be frozen on
// the prior occurrence's registration.
//
// Built-in AI tanks (builtin-scout/bruiser/ranger/randy) are deliberately
// excluded here: every occurrence, including this one, already gets padded
// with them unconditionally by handleRegistrationClose's autofill logic
// (cmd/tournament-scheduler/main.go), sourced from fixed deploy-time config
// rather than any prior occurrence's roster. Carrying them forward here too
// would risk colliding with that padding — see PRIORITIES.md item 256.
//
// Safe to call more than once for the same gd (e.g. on the self-healing
// retry path in materializeNext): both db.AddRosterEntry and
// db.AddVersionRegistration no-op when the tank/registration is already
// present.
func (h *handler) carryForwardUserTanks(ctx context.Context, series db.GameDaySeries, gd db.GameDay, existing []db.GameDay, rrAt time.Time) {
	var prev db.GameDay
	var prevAt time.Time
	found := false
	for _, g := range existing {
		if g.SeriesID != series.SeriesID || g.GameDayID == gd.GameDayID {
			continue
		}
		t, err := time.Parse(time.RFC3339, g.Schedule.RoundRobin)
		if err != nil || !t.Before(rrAt) {
			continue
		}
		if !found || t.After(prevAt) {
			prev, prevAt, found = g, t, true
		}
	}
	if !found {
		return // this is the series' first occurrence — nothing to carry forward
	}

	for _, mt := range prev.RegisteredTanks {
		tankID := db.RealTankID(mt.TankID)
		if strings.HasPrefix(tankID, "builtin-") {
			continue
		}
		versions, err := h.store.ListVersionsByTank(ctx, tankID)
		if err != nil {
			log.Printf("series %s: carry-forward: list versions for tank %s: %v", series.SeriesID, tankID, err)
			continue
		}
		latest, ok := db.LatestMajorVersion(versions)
		if !ok {
			log.Printf("series %s: carry-forward: tank %s has no major version — skipping", series.SeriesID, tankID)
			continue
		}
		tankName := mt.TankName
		if t, err := h.store.GetTank(ctx, tankID); err == nil {
			tankName = t.Name
		}
		// AddRosterEntry does a read-modify-write on the GameDay record
		// (optimistic-locked via db.PutGameDay), so a concurrent write —
		// e.g. an admin editing this same freshly-materialized occurrence's
		// roster around the same time — can return db.ErrConflict. Retry it
		// the same way cmd/tank-api's addRosterEntry handler does, or a
		// tank silently drops out of the carry-forward instead of just
		// being retried (item 257).
		if err := retryOnConflict(func() error {
			return h.store.AddRosterEntry(ctx, gd.GameDayID, tankID, latest, tankName)
		}); err != nil {
			log.Printf("series %s: carry-forward: add roster entry tank %s version %s: %v", series.SeriesID, tankID, latest, err)
			continue
		}
		if err := h.store.AddVersionRegistration(ctx, tankID, latest, gd.GameDayID); err != nil {
			log.Printf("series %s: carry-forward: add version registration tank %s version %s: %v", series.SeriesID, tankID, latest, err)
			continue
		}
		log.Printf("series %s: carried forward tank %s at %s into occurrence %s", series.SeriesID, tankID, latest, gd.GameDayID)
	}
}
