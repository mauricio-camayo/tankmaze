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
	"log"
	"os"
	"strconv"
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
