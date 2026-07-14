// Package scheduling holds the Game Day materialization logic shared between
// cmd/tank-api's POST /gamedays handler and cmd/series-materializer (item
// 238): creating a GameDay record and its per-phase one-time EventBridge
// Scheduler rules is identical work whether triggered by an admin's request
// or by a recurring series' rolling job.
package scheduling

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	schedulersvc "github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"

	"github.com/tankmaze/backend/internal/db"
)

// newUUID matches the same small helper duplicated across cmd/tank-api,
// cmd/localserver, and cmd/tournament-scheduler.
func newUUID() string {
	var b [16]byte
	_, _ = crand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Materializer creates GameDay records and their EventBridge schedules.
type Materializer struct {
	Store                  *db.Store
	SchedulerSvc           *schedulersvc.Client
	SchedulerRoleArn       string
	TournamentSchedulerArn string
	SchedulerDLQArn        string
}

// Params describes one occurrence to materialize.
type Params struct {
	Name                string
	RegistrationCloseAt time.Time
	RoundRobinAt        time.Time
	FinalAt             time.Time
	Autofill            bool
	ForcedMapIDs        []string
	RandomMaps          bool
	// SeriesID is empty for a standalone (non-recurring) Game Day.
	SeriesID string
}

// Materialize creates the GameDay record and its registration_close,
// round_robin, elimination_r1..r5, and final one-time schedules. On schedule
// creation failure it best-effort rolls back the GameDay record it just
// wrote, mirroring the original createGameDay handler's behavior.
func (m *Materializer) Materialize(ctx context.Context, p Params) (db.GameDay, error) {
	const maxElimRounds = 5
	elimTimes := make([]time.Time, maxElimRounds)
	for i := 0; i < maxElimRounds; i++ {
		t := p.FinalAt.Add(-time.Duration(maxElimRounds-i) * 30 * time.Minute)
		if t.Before(p.RoundRobinAt) {
			t = p.RoundRobinAt
		}
		elimTimes[i] = t
	}
	elimination := make([]string, maxElimRounds)
	for i, t := range elimTimes {
		elimination[i] = t.UTC().Format(time.RFC3339)
	}

	gameDayID := newUUID()
	gd := db.GameDay{
		GameDayID: gameDayID,
		Name:      displayName(p.Name, p.RoundRobinAt, p.FinalAt),
		Schedule: db.GameDaySchedule{
			RegistrationClose: p.RegistrationCloseAt.UTC().Format(time.RFC3339),
			RoundRobin:        p.RoundRobinAt.UTC().Format(time.RFC3339),
			Elimination:       elimination,
			Final:             p.FinalAt.UTC().Format(time.RFC3339),
		},
		Phases: db.GameDayPhases{
			RoundRobin: db.PhaseStatus{Status: "upcoming"},
			Final:      db.PhaseStatus{Status: "upcoming"},
		},
		CreatedAt:    time.Now().Unix(),
		Autofill:     p.Autofill,
		ForcedMapIDs: p.ForcedMapIDs,
		RandomMaps:   p.RandomMaps,
		SeriesID:     p.SeriesID,
	}

	if err := m.Store.PutGameDay(ctx, gd); err != nil {
		return db.GameDay{}, fmt.Errorf("put gameday: %w", err)
	}

	phases := []struct {
		name  string
		phase string
		at    time.Time
	}{
		{gameDayID + "-reg-close", "registration_close", p.RegistrationCloseAt},
		{gameDayID + "-rr", "round_robin", p.RoundRobinAt},
		{gameDayID + "-elim-r1", "elimination_r1", elimTimes[0]},
		{gameDayID + "-elim-r2", "elimination_r2", elimTimes[1]},
		{gameDayID + "-elim-r3", "elimination_r3", elimTimes[2]},
		{gameDayID + "-elim-r4", "elimination_r4", elimTimes[3]},
		{gameDayID + "-elim-r5", "elimination_r5", elimTimes[4]},
		{gameDayID + "-final", "final", p.FinalAt},
	}

	for _, ph := range phases {
		expr := "at(" + ph.at.UTC().Format("2006-01-02T15:04:05") + ")"
		payload, _ := json.Marshal(map[string]string{"gameDayId": gameDayID, "phase": ph.phase})
		target := &schedulertypes.Target{
			Arn:     aws.String(m.TournamentSchedulerArn),
			RoleArn: aws.String(m.SchedulerRoleArn),
			Input:   aws.String(string(payload)),
		}
		if m.SchedulerDLQArn != "" {
			target.DeadLetterConfig = &schedulertypes.DeadLetterConfig{Arn: aws.String(m.SchedulerDLQArn)}
		}
		_, err := m.SchedulerSvc.CreateSchedule(ctx, &schedulersvc.CreateScheduleInput{
			Name:                       aws.String(ph.name),
			GroupName:                  aws.String("tankmaze-gamedays"),
			ScheduleExpression:         aws.String(expr),
			ScheduleExpressionTimezone: aws.String("UTC"),
			FlexibleTimeWindow: &schedulertypes.FlexibleTimeWindow{
				Mode: schedulertypes.FlexibleTimeWindowModeOff,
			},
			Target:                target,
			ActionAfterCompletion: schedulertypes.ActionAfterCompletionDelete,
		})
		if err != nil {
			log.Printf("create schedule %s: %v", ph.name, err)
			if delErr := m.Store.DeleteGameDay(ctx, gameDayID); delErr != nil {
				log.Printf("rollback delete gameday %s: %v", gameDayID, delErr)
			}
			return db.GameDay{}, fmt.Errorf("create schedule %s: %w", ph.name, err)
		}
	}

	return gd, nil
}

// displayName matches cmd/tank-api's and cmd/localserver's own
// gameDayDisplayName helper exactly (both duplicate this small function
// rather than import it, following this repo's established convention —
// see e.g. identityProviderName) — kept in sync by hand if either changes.
func displayName(baseName string, rrAt, finalAt time.Time) string {
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
