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
	// Item 265: with bracket depth correctly derived from round-1's real
	// entrant count (bracketDepth(2)=1 total round here — tankA/tankB are
	// the only two real bracket entrants; r2's single "playing" slot is
	// just the bye-continuation placeholder, not a second real round), the
	// tournament's only actual match doubles as the Final, so tankB — its
	// loser — is correctly the runner-up (k=2), not a tier-3 "semi-final"
	// loser as the old key-counting logic (which miscounted r2 as a real
	// extra round) previously computed.
	if tiers["tankB"].k != 2 {
		t.Errorf("tankB (r1 loser, sole match doubles as the Final): want k=2 (runner-up), got k=%d", tiers["tankB"].k)
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

// TestBracketTiers_DuplicateAutofillTankIDsStayIndependent is the regression
// test for item 248 ("Scout twins" bug): two autofill-registered instances of
// the same built-in AI, distinguished by tournament-scheduler's padWithBots
// suffix ("builtin-scout" vs "builtin-scout#2"), must each get their own,
// correct tier — not silently overwrite each other in the tiers map keyed by
// TankID. Scenario: "builtin-scout" wins r1 then loses the r2 final
// (runner-up, k=2); the *other* instance, "builtin-scout#2", loses r1
// (quarter-final tier, k=3). Before the fix both instances shared the bare
// "builtin-scout" key and one tier would clobber the other depending on map
// iteration order.
func TestBracketTiers_DuplicateAutofillTankIDsStayIndependent(t *testing.T) {
	bracket := map[string][]db.BracketSlot{
		"r1": {
			{TankID: "builtin-scout", Status: "won"},
			{TankID: "opponentX", Status: "lost"},
			{TankID: "builtin-scout#2", Status: "lost"},
			{TankID: "opponentY", Status: "won"},
		},
		"r2": {
			{TankID: "builtin-scout", Status: "lost"},
			{TankID: "opponentY", Status: "won"},
		},
	}

	tiers, championNull := bracketTiers(bracket)

	if championNull {
		t.Fatal("expected championNull=false but got true")
	}
	if got := tiers["builtin-scout"].k; got != 2 {
		t.Errorf("builtin-scout (lost the r2 final): want k=2 (runner-up), got k=%d", got)
	}
	if got := tiers["builtin-scout#2"].k; got != 3 {
		t.Errorf("builtin-scout#2 (lost r1): want k=3 (quarter-final), got k=%d", got)
	}
	if got := tiers["opponentY"].k; got != 1 {
		t.Errorf("opponentY (champion): want k=1, got k=%d", got)
	}
}

// TestBracketTiers_RoundBeforeFinalDoesNotCollideWithFinalists is the
// regression test for item 265, modeled directly on gameday
// 059dcbb7-6dca-4c0d-850d-32190b31998a's real 8-tank bracket: r1 (8 slots,
// quarter-final tier), r2 (4 real slots + 1 bye, semi-final tier — scoutID
// loses here), and a separate "final" key (both-lose, no champion). Before
// the fix, totalRounds was miscounted as 2 (only the numbered r1/r2 keys),
// so scoutID's r2 loss computed to the exact same tier (k=2) as the true
// both-lose finalists and got wrongly caught by the championNull skip in
// handle() — zero ranking credit despite outperforming every r1 loser.
func TestBracketTiers_RoundBeforeFinalDoesNotCollideWithFinalists(t *testing.T) {
	bracket := map[string][]db.BracketSlot{
		"r1": {
			{TankID: "finalistA", Status: "won"},
			{TankID: "r1LoserA", Status: "lost"},
			{TankID: "scoutID", Status: "won"},
			{TankID: "r1LoserB", Status: "lost"},
			{TankID: "finalistB", Status: "won"},
			{TankID: "r1LoserC", Status: "lost"},
			{TankID: "r1BothLoseA", Status: "both_lose"},
			{TankID: "r1BothLoseB", Status: "both_lose"},
		},
		"r2": {
			{TankID: "scoutID", Status: "lost"},
			{TankID: "finalistA", Status: "won"},
			{TankID: "finalistB", Status: "won"},
			{Status: "bye"},
		},
		"final": {
			{TankID: "finalistA", Status: "both_lose"},
			{TankID: "finalistB", Status: "both_lose"},
		},
	}

	tiers, championNull := bracketTiers(bracket)

	if !championNull {
		t.Fatal("both-lose Final: expected championNull=true")
	}
	if got := tiers["finalistA"].k; got != 2 {
		t.Errorf("finalistA (true Final participant): want k=2, got k=%d", got)
	}
	if got := tiers["finalistB"].k; got != 2 {
		t.Errorf("finalistB (true Final participant): want k=2, got k=%d", got)
	}
	if got := tiers["scoutID"].k; got != 3 {
		t.Errorf("scoutID (lost the round before the Final): want k=3 — must NOT collide with the true finalists' k=2, got k=%d", got)
	}
	for _, id := range []string{"r1LoserA", "r1LoserB", "r1LoserC", "r1BothLoseA", "r1BothLoseB"} {
		if got := tiers[id].k; got != 4 {
			t.Errorf("%s (r1 loser): want k=4, got k=%d", id, got)
		}
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
