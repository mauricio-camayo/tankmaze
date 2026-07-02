package main

import (
	"testing"

	"github.com/tankmaze/backend/internal/db"
)

// TestNextTournamentPhase_LastRoundHasByeSkipsToFinal reconstructs the stuck
// state from game day da99184e-3ae8-4673-ad94-49c2d924fa61 (2026-07-02):
// elimination r3 running with one bye pair and one real pair, both matches
// in the real pair already ended. Before the fix, nextTournamentPhase
// returned "elimination_r4", which tournament-scheduler's handleElimination
// correctly no-ops on (activePairs <= 1), leaving r3 stuck at "running"
// until the separately-scheduled -final EventBridge rule rescued it ~79
// minutes later. It should instead go straight to "final".
func TestNextTournamentPhase_LastRoundHasByeSkipsToFinal(t *testing.T) {
	gd := db.GameDay{
		GameDayID: "da99184e-3ae8-4673-ad94-49c2d924fa61",
		Phases: db.GameDayPhases{
			RoundRobin: db.PhaseStatus{Status: "complete"},
			Elimination: map[string]db.PhaseStatus{
				"r1": {Status: "complete"},
				"r2": {Status: "complete"},
				"r3": {Status: "running", StartedAt: 1},
			},
			Final: db.PhaseStatus{Status: ""},
		},
		Bracket: map[string][]db.BracketSlot{
			"r3": {
				// pair(0,1): bye — slot 1 has no TankID.
				{TankID: "tank-a", Version: "v1", Status: "won"},
				{TankID: "", Status: "bye"},
				// pair(2,3): both real — the match ended, but slot .Status
				// is stale ("playing") since updateSlotsFromMatches only
				// runs during a phase-transition attempt, matching the
				// exact staleness described in the bug report.
				{TankID: "tank-b", Version: "v1", Status: "playing"},
				{TankID: "tank-c", Version: "v1", Status: "playing"},
			},
		},
	}

	got := nextTournamentPhase(gd)
	if got != "final" {
		t.Fatalf("nextTournamentPhase() = %q, want %q (r3 has only 1 active pair, should skip straight to final)", got, "final")
	}
}

// TestNextTournamentPhase_LastRoundStillNeedsNextRound is the control case:
// two active pairs in the current round still means another elimination
// round is needed, so the fix must not change this existing behavior.
func TestNextTournamentPhase_LastRoundStillNeedsNextRound(t *testing.T) {
	gd := db.GameDay{
		Phases: db.GameDayPhases{
			RoundRobin: db.PhaseStatus{Status: "complete"},
			Elimination: map[string]db.PhaseStatus{
				"r1": {Status: "running", StartedAt: 1},
			},
		},
		Bracket: map[string][]db.BracketSlot{
			"r1": {
				{TankID: "tank-a", Version: "v1", Status: "won"},
				{TankID: "tank-b", Version: "v1", Status: "lost"},
				{TankID: "tank-c", Version: "v1", Status: "won"},
				{TankID: "tank-d", Version: "v1", Status: "lost"},
			},
		},
	}

	got := nextTournamentPhase(gd)
	if got != "elimination_r2" {
		t.Fatalf("nextTournamentPhase() = %q, want %q (2 active pairs, another round is genuinely needed)", got, "elimination_r2")
	}
}
