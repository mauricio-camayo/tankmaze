package main

import (
	"testing"

	"github.com/tankmaze/backend/internal/db"
)

// TestBracketTiers_NormalFinal verifies that a regular two-player final
// (an actual match was played) is handled correctly via the numeric rounds loop.
func TestBracketTiers_NormalFinal(t *testing.T) {
	bracket := map[string][]db.BracketSlot{
		"r1": {
			{TankID: "tankA", Status: "won"},
			{TankID: "tankB", Status: "lost"},
			{TankID: "tankC", Status: "won"},
			{TankID: "tankD", Status: "lost"},
		},
		"r2": {
			{TankID: "tankA", Status: "won"},
			{TankID: "tankC", Status: "lost"},
		},
	}

	tiers, championNull := bracketTiers(bracket)

	if championNull {
		t.Fatal("expected championNull=false but got true")
	}
	if tiers["tankA"].k != 1 || tiers["tankA"].placement != 1 {
		t.Errorf("tankA: want k=1 placement=1, got k=%d placement=%d", tiers["tankA"].k, tiers["tankA"].placement)
	}
	if tiers["tankC"].k != 2 {
		t.Errorf("tankC: want k=2 (runner-up), got k=%d", tiers["tankC"].k)
	}
	if tiers["tankB"].k != 3 || tiers["tankC"].k != 2 {
		t.Errorf("unexpected tiers: %+v", tiers)
	}
}

// TestBracketTiers_ChampionByBye_PlayingStatus_RealBug is the regression test
// for P1-141: when handleFinal writes bracket["final"] with Status="won" for a
// bye-path champion and the last numeric round (bracket["r2"]) shows Status="playing",
// bracketTiers must recognise the "final" key and return championNull=false.
func TestBracketTiers_ChampionByBye_PlayingStatus_RealBug(t *testing.T) {
	// Simulate the real bug scenario:
	// - r1 completes normally; tankA beats tankB.
	// - tankC receives a bye in r2 (opponent slot is empty / Status="bye").
	// - tournament-scheduler writes bracket["final"] = [{tankA, "won"}, {tankC, "lost"}]
	//   because the final was a real match.
	// In the pure-bye case (only one finalist):
	// - bracket["r2"] has tankA with Status="playing" (no actual match ran).
	// - bracket["final"] has [{tankA, "won"}].
	bracket := map[string][]db.BracketSlot{
		"r1": {
			{TankID: "tankA", Status: "won"},
			{TankID: "tankB", Status: "lost"},
		},
		"r2": {
			// tankA is waiting — no match ran against a bye.
			{TankID: "tankA", Status: "playing"},
		},
		"final": {
			{TankID: "tankA", Status: "won"},
		},
	}

	tiers, championNull := bracketTiers(bracket)

	if championNull {
		t.Fatal("champion-by-bye: expected championNull=false but got true — bracket[\"final\"] was ignored")
	}
	if tiers["tankA"].k != 1 || tiers["tankA"].placement != 1 {
		t.Errorf("tankA (bye champion): want k=1 placement=1, got k=%d placement=%d",
			tiers["tankA"].k, tiers["tankA"].placement)
	}
	if tiers["tankB"].k != 3 {
		t.Errorf("tankB (r1 loser): want k=3, got k=%d", tiers["tankB"].k)
	}
}

// TestBracketTiers_UncontestedChampion tests the handleFinal len(finalists)==1 path:
// only one finalist exists (the other dropped out or was a bye), written to bracket["final"].
func TestBracketTiers_UncontestedChampion(t *testing.T) {
	bracket := map[string][]db.BracketSlot{
		"r1": {
			{TankID: "tankA", Status: "won"},
			{TankID: "tankB", Status: "lost"},
			{TankID: "tankC", Status: "won"},
			{TankID: "tankD", Status: "lost"},
		},
		"final": {
			// Only tankA reached the final — uncontested champion.
			{TankID: "tankA", Status: "won"},
		},
	}

	tiers, championNull := bracketTiers(bracket)

	if championNull {
		t.Fatal("uncontested champion: expected championNull=false")
	}
	if tiers["tankA"].k != 1 || tiers["tankA"].placement != 1 {
		t.Errorf("tankA: want k=1 placement=1, got %+v", tiers["tankA"])
	}
	// r1 losers should be at k=3 (totalRounds inferred from numeric keys = 1,
	// so finalRoundK(1,1)=2, but there's no r2 numeric round here).
	// The important invariant is championNull is false and tankA is champion.
}

// TestBracketTiers_FinalBothLose verifies that a both-lose final sets championNull=true
// and assigns tier-2 (k=2) to both finalists, even when written to bracket["final"].
func TestBracketTiers_FinalBothLose(t *testing.T) {
	bracket := map[string][]db.BracketSlot{
		"r1": {
			{TankID: "tankA", Status: "won"},
			{TankID: "tankB", Status: "lost"},
			{TankID: "tankC", Status: "won"},
			{TankID: "tankD", Status: "lost"},
		},
		"r2": {
			{TankID: "tankA", Status: "both_lose"},
			{TankID: "tankC", Status: "both_lose"},
		},
	}

	tiers, championNull := bracketTiers(bracket)

	if !championNull {
		t.Fatal("both-lose final: expected championNull=true")
	}
	if tiers["tankA"].k != 2 {
		t.Errorf("tankA (both-lose finalist): want k=2, got k=%d", tiers["tankA"].k)
	}
	if tiers["tankC"].k != 2 {
		t.Errorf("tankC (both-lose finalist): want k=2, got k=%d", tiers["tankC"].k)
	}
}

// TestBracketTiers_FinalBothLoseViaFinalKey verifies that both-lose written to
// bracket["final"] (rather than the last numeric round) also sets championNull=true.
func TestBracketTiers_FinalBothLoseViaFinalKey(t *testing.T) {
	bracket := map[string][]db.BracketSlot{
		"r1": {
			{TankID: "tankA", Status: "won"},
			{TankID: "tankB", Status: "lost"},
		},
		"final": {
			{TankID: "tankA", Status: "both_lose"},
			{TankID: "tankC", Status: "both_lose"},
		},
	}

	tiers, championNull := bracketTiers(bracket)

	if !championNull {
		t.Fatal("both-lose via bracket[\"final\"]: expected championNull=true")
	}
	if tiers["tankA"].k != 2 {
		t.Errorf("tankA: want k=2 (both_lose via final key), got k=%d", tiers["tankA"].k)
	}
	if tiers["tankC"].k != 2 {
		t.Errorf("tankC: want k=2 (both_lose via final key), got k=%d", tiers["tankC"].k)
	}
}

// TestBracketTiers_EmptyBracket verifies the empty-bracket fast path.
func TestBracketTiers_EmptyBracket(t *testing.T) {
	tiers, championNull := bracketTiers(nil)
	if tiers != nil {
		t.Errorf("expected nil tiers, got %v", tiers)
	}
	if !championNull {
		t.Fatal("empty bracket: expected championNull=true")
	}
}

// TestPlacementPoints verifies the floor(n/2^(k-1)) formula.
func TestPlacementPoints(t *testing.T) {
	cases := []struct{ n, k, want int }{
		{8, 1, 8},  // champion: n
		{8, 2, 4},  // runner-up: n/2
		{8, 3, 2},  // semi-final: n/4
		{8, 4, 1},  // quarter-final: n/8
		{32, 1, 32},
		{32, 2, 16},
		{32, 3, 8},
		{32, 4, 4},
		{32, 5, 2},
	}
	for _, c := range cases {
		got := placementPoints(c.n, c.k)
		if got != c.want {
			t.Errorf("placementPoints(%d,%d): want %d, got %d", c.n, c.k, c.want, got)
		}
	}
}
