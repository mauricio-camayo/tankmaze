package main

import (
	"testing"
	"time"

	"github.com/tankmaze/backend/internal/db"
)

func TestGameDayIsDone(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   bool
	}{
		{"upcoming — not done", "upcoming", false},
		{"running — not done", "running", false},
		{"complete — done", "complete", true},
		{"cancelled — done", "cancelled", true},
		{"empty status — not done", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := db.GameDay{Phases: db.GameDayPhases{Final: db.PhaseStatus{Status: c.status}}}
			if got := gameDayIsDone(g); got != c.want {
				t.Fatalf("gameDayIsDone(Final.Status=%q) = %v, want %v", c.status, got, c.want)
			}
		})
	}
}

func TestSeriesMaterializationState(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	fmtT := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }

	const seriesID = "series-1"

	upcoming := db.PhaseStatus{Status: "upcoming"}
	running := db.PhaseStatus{Status: "running"}
	complete := db.PhaseStatus{Status: "complete"}
	cancelled := db.PhaseStatus{Status: "cancelled"}

	t.Run("no existing occurrences for this series — materialize", func(t *testing.T) {
		series := db.GameDaySeries{SeriesID: seriesID, NextOccurrenceAt: fmtT(now.Add(24 * time.Hour))}
		_, already, pending := seriesMaterializationState(series, nil)
		if already || pending {
			t.Fatalf("got already=%v pending=%v, want false/false", already, pending)
		}
	})

	t.Run("an earlier occurrence for this series hasn't finished yet — wait (item 262)", func(t *testing.T) {
		series := db.GameDaySeries{SeriesID: seriesID, NextOccurrenceAt: fmtT(now.Add(48 * time.Hour))}
		existing := []db.GameDay{
			{SeriesID: seriesID, Schedule: db.GameDaySchedule{RoundRobin: fmtT(now.Add(24 * time.Hour))}, Phases: db.GameDayPhases{Final: upcoming}},
		}
		_, already, pending := seriesMaterializationState(series, existing)
		if already {
			t.Fatalf("got already=true, want false")
		}
		if !pending {
			t.Fatalf("got pending=false, want true — an earlier occurrence hasn't finished yet")
		}
	})

	t.Run("regression: an earlier occurrence's round-robin start time has passed but it's still running — must still wait", func(t *testing.T) {
		// This is the exact gap a live gameday (059dcbb7...) surfaced: gating
		// on Schedule.RoundRobin's scheduled timestamp instead of the actual
		// Final.Status let the next occurrence materialize while this one's
		// elimination rounds/final were still in progress — round-robin
		// starting is not the same as the occurrence being done.
		series := db.GameDaySeries{SeriesID: seriesID, NextOccurrenceAt: fmtT(now.Add(24 * time.Hour))}
		existing := []db.GameDay{
			{SeriesID: seriesID, Schedule: db.GameDaySchedule{RoundRobin: fmtT(now.Add(-24 * time.Hour))}, Phases: db.GameDayPhases{Final: running}},
		}
		_, already, pending := seriesMaterializationState(series, existing)
		if already {
			t.Fatalf("got already=true, want false")
		}
		if !pending {
			t.Fatalf("got pending=false — would have materialized the next occurrence while this one is still running")
		}
	})

	t.Run("the prior occurrence has completed — materialize the next one", func(t *testing.T) {
		series := db.GameDaySeries{SeriesID: seriesID, NextOccurrenceAt: fmtT(now.Add(24 * time.Hour))}
		existing := []db.GameDay{
			{SeriesID: seriesID, Schedule: db.GameDaySchedule{RoundRobin: fmtT(now.Add(-24 * time.Hour))}, Phases: db.GameDayPhases{Final: complete}},
		}
		_, already, pending := seriesMaterializationState(series, existing)
		if already || pending {
			t.Fatalf("got already=%v pending=%v, want false/false", already, pending)
		}
	})

	t.Run("the prior occurrence was cancelled (e.g. zero registered tanks) — materialize the next one", func(t *testing.T) {
		series := db.GameDaySeries{SeriesID: seriesID, NextOccurrenceAt: fmtT(now.Add(24 * time.Hour))}
		existing := []db.GameDay{
			{SeriesID: seriesID, Schedule: db.GameDaySchedule{RoundRobin: fmtT(now.Add(-24 * time.Hour))}, Phases: db.GameDayPhases{Final: cancelled}},
		}
		_, already, pending := seriesMaterializationState(series, existing)
		if already || pending {
			t.Fatalf("got already=%v pending=%v, want false/false", already, pending)
		}
	})

	t.Run("this exact slot was already materialized — self-healing dedup, advance only", func(t *testing.T) {
		nextAt := fmtT(now.Add(24 * time.Hour))
		series := db.GameDaySeries{SeriesID: seriesID, NextOccurrenceAt: nextAt}
		existing := []db.GameDay{
			{GameDayID: "gd-1", SeriesID: seriesID, Schedule: db.GameDaySchedule{RoundRobin: nextAt}, Phases: db.GameDayPhases{Final: upcoming}},
		}
		gd, already, pending := seriesMaterializationState(series, existing)
		if !already {
			t.Fatalf("got already=false, want true")
		}
		if pending {
			t.Fatalf("got pending=true, want false")
		}
		if gd.GameDayID != "gd-1" {
			t.Fatalf("got gd.GameDayID=%q, want gd-1", gd.GameDayID)
		}
	})

	t.Run("other series' occurrences never block this one", func(t *testing.T) {
		series := db.GameDaySeries{SeriesID: seriesID, NextOccurrenceAt: fmtT(now.Add(24 * time.Hour))}
		existing := []db.GameDay{
			{SeriesID: "other-series", Schedule: db.GameDaySchedule{RoundRobin: fmtT(now.Add(48 * time.Hour))}, Phases: db.GameDayPhases{Final: upcoming}},
		}
		_, already, pending := seriesMaterializationState(series, existing)
		if already || pending {
			t.Fatalf("got already=%v pending=%v, want false/false", already, pending)
		}
	})

	t.Run("regression: hourly ticks within LEAD_TIME_DAYS no longer fast-forward past the next occurrence", func(t *testing.T) {
		// Reproduces the originally reported symptom: a daily series ticked
		// hourly with LEAD_TIME_DAYS=7 used to materialize a new occurrence
		// every tick since NextOccurrenceAt stayed inside the 7-day cutoff.
		// Simulate tick 2 (one occurrence already materialized and still
		// upcoming): it must wait, not materialize occurrence #2 immediately.
		firstRR := now.Add(24 * time.Hour) // first occurrence still a day out
		series := db.GameDaySeries{SeriesID: seriesID, NextOccurrenceAt: fmtT(firstRR.AddDate(0, 0, 1))}
		existing := []db.GameDay{
			{SeriesID: seriesID, Schedule: db.GameDaySchedule{RoundRobin: fmtT(firstRR)}, Phases: db.GameDayPhases{Final: upcoming}},
		}
		_, already, pending := seriesMaterializationState(series, existing)
		if already {
			t.Fatalf("got already=true, want false")
		}
		if !pending {
			t.Fatalf("got pending=false — would have fast-forwarded and materialized a second occurrence before the first one finished")
		}
	})
}
