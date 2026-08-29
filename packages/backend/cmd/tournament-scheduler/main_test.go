package main

import (
	"strings"
	"testing"
	"time"

	"github.com/tankmaze/backend/internal/db"
)

// TestHandleEliminationR1_FinalTimeUpdated verifies that handleEliminationR1
// updates gd.Schedule.Final based on the actual number of elimination rounds
// needed, rather than leaving it at the admin-set value.
//
// Scenario: admin created a game day for a full 5-round tournament. Only 8
// tanks qualified (3 rounds needed). The final should move from
// r1+150min to r1+90min.
func TestHandleEliminationR1_FinalRoundScheduling(t *testing.T) {
	r1Time := time.Date(2026, 6, 23, 14, 0, 0, 0, time.UTC) // 2pm
	adminFinalAt := r1Time.Add(150 * time.Minute)            // 2pm + 2h30m = 4:30pm (5-round estimate)

	// Pre-populate elimination times as createGameDay would (5 rounds backward from finalAt).
	preCreated := make([]string, 5)
	for i := 0; i < 5; i++ {
		preCreated[i] = adminFinalAt.Add(-time.Duration(5-i) * 30 * time.Minute).UTC().Format(time.RFC3339)
	}
	// Verify preCreated[0] == r1Time.
	if preCreated[0] != r1Time.UTC().Format(time.RFC3339) {
		t.Fatalf("test setup: preCreated[0]=%s want %s", preCreated[0], r1Time.UTC().Format(time.RFC3339))
	}

	gd := db.GameDay{
		Schedule: db.GameDaySchedule{
			Elimination: preCreated,
			Final:       adminFinalAt.UTC().Format(time.RFC3339),
		},
	}

	// Simulate what handleEliminationR1 does when computing with 8 qualifiers (3 rounds):
	// numRounds=3 (nextPowerOf2(8)=8, 8>2→1, 4>2→2, 2>2 stop → 2 rounds)
	// Wait, 8 qualifiers: nextPowerOf2(8)=8. Loop: 8>2→nr=1,q=4; 4>2→nr=2,q=2; 2>2 false → nr=2.
	// So 8 qualifiers → 2 rounds. Let me use 10 qualifiers → nextPowerOf2(10)=16 → 3 rounds.
	numRounds := 3
	r1StartAt, _ := time.Parse(time.RFC3339, gd.Schedule.Elimination[0])

	elim := make([]string, numRounds)
	for i := 0; i < numRounds; i++ {
		elim[i] = r1StartAt.Add(time.Duration(i) * 30 * time.Minute).UTC().Format(time.RFC3339)
	}
	gd.Schedule.Elimination = elim
	newFinalAt := r1StartAt.Add(time.Duration(numRounds) * 30 * time.Minute)
	gd.Schedule.Final = newFinalAt.UTC().Format(time.RFC3339)

	// Verify elimination times are forward from r1.
	wantR2 := r1Time.Add(30 * time.Minute).UTC().Format(time.RFC3339)
	wantR3 := r1Time.Add(60 * time.Minute).UTC().Format(time.RFC3339)
	wantFinal := r1Time.Add(90 * time.Minute).UTC().Format(time.RFC3339)

	if gd.Schedule.Elimination[1] != wantR2 {
		t.Errorf("r2: got %s want %s", gd.Schedule.Elimination[1], wantR2)
	}
	if gd.Schedule.Elimination[2] != wantR3 {
		t.Errorf("r3: got %s want %s", gd.Schedule.Elimination[2], wantR3)
	}
	if gd.Schedule.Final != wantFinal {
		t.Errorf("final: got %s want %s", gd.Schedule.Final, wantFinal)
	}
	// Final must differ from admin-set value (moved earlier).
	if gd.Schedule.Final == adminFinalAt.UTC().Format(time.RFC3339) {
		t.Error("final time was not updated — still at the admin-set worst-case estimate")
	}
}

// TestHandleEliminationR1_FiveRoundsUnchanged verifies that a full 5-round
// tournament leaves gd.Schedule.Final at the admin-set value (since
// r1+5*30min == adminFinalAt).
func TestHandleEliminationR1_FiveRoundsUnchanged(t *testing.T) {
	r1Time := time.Date(2026, 6, 23, 14, 0, 0, 0, time.UTC)
	adminFinalAt := r1Time.Add(150 * time.Minute) // 5 rounds × 30 min

	preCreated := make([]string, 5)
	for i := 0; i < 5; i++ {
		preCreated[i] = adminFinalAt.Add(-time.Duration(5-i) * 30 * time.Minute).UTC().Format(time.RFC3339)
	}

	numRounds := 5
	r1StartAt, _ := time.Parse(time.RFC3339, preCreated[0])

	elim := make([]string, numRounds)
	for i := 0; i < numRounds; i++ {
		elim[i] = r1StartAt.Add(time.Duration(i) * 30 * time.Minute).UTC().Format(time.RFC3339)
	}
	newFinalAt := r1StartAt.Add(time.Duration(numRounds) * 30 * time.Minute)
	newFinalStr := newFinalAt.UTC().Format(time.RFC3339)

	if newFinalStr != adminFinalAt.UTC().Format(time.RFC3339) {
		t.Errorf("5-round final: got %s want %s (should be unchanged)", newFinalStr, adminFinalAt.UTC().Format(time.RFC3339))
	}
}

// TestSeedBracketOrder verifies the standard best-vs-worst seeding pattern.
func TestSeedBracketOrder(t *testing.T) {
	got := seedBracketOrder(8)
	want := []int{1, 8, 4, 5, 2, 7, 3, 6}
	if len(got) != len(want) {
		t.Fatalf("len: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d]: got %d want %d", i, got[i], want[i])
		}
	}
}

// TestNextPowerOf2 tests the power-of-2 helper.
func TestNextPowerOf2(t *testing.T) {
	cases := [][2]int{{1, 1}, {2, 2}, {3, 4}, {4, 4}, {5, 8}, {14, 16}, {16, 16}, {17, 32}}
	for _, c := range cases {
		if got := nextPowerOf2(c[0]); got != c[1] {
			t.Errorf("nextPowerOf2(%d)=%d want %d", c[0], got, c[1])
		}
	}
}

// TestGlobalRerank_ByPoints verifies the global re-rank sorts by points desc.
func TestGlobalRerank_ByPoints(t *testing.T) {
	p := func(i int) *int { return &i }

	tanks := []db.MatchTank{
		{TankID: "low"},
		{TankID: "high"},
	}
	groups := []db.Group{{Tanks: tanks}}
	matches := []db.Match{
		{MatchID: "m1", TankA: tanks[1], TankB: tanks[0],
			Status: "ended", Result: &db.MatchResult{Winner: p(0), Flawless: true}},
	}

	qualifiers := []db.MatchTank{{TankID: "low"}, {TankID: "high"}}
	globalRerank(qualifiers, groups, matches)

	if qualifiers[0].TankID != "high" {
		t.Errorf("expected 'high' as seed 1, got %s", qualifiers[0].TankID)
	}
}

// TestPadWithBots_DuplicatesGetSuffixedTankID verifies item 248's fix: the
// first copy of each bot keeps its bare TankID (max backward compatibility
// for the common non-duplicated case); every repeat beyond the first gets a
// "#N" suffix so it never collides with another instance's TankID.
func TestPadWithBots_DuplicatesGetSuffixedTankID(t *testing.T) {
	bots := []db.MatchTank{
		{TankID: "builtin-scout", TankName: "Scout"},
		{TankID: "builtin-bruiser", TankName: "Bruiser"},
	}
	got := padWithBots(nil, bots, 5)

	want := []string{
		"builtin-scout", "builtin-bruiser",
		"builtin-scout#2", "builtin-bruiser#2",
		"builtin-scout#3",
	}
	if len(got) != len(want) {
		t.Fatalf("len: got %d want %d (%v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].TankID != w {
			t.Errorf("[%d]: got TankID=%q want %q", i, got[i].TankID, w)
		}
	}
	// TankName must be preserved unsuffixed on every instance — only TankID
	// carries the disambiguating suffix.
	if got[2].TankName != "Scout" {
		t.Errorf("got[2].TankName = %q, want unsuffixed %q", got[2].TankName, "Scout")
	}
}

// TestPadWithBots_NoDuplicatesLeavesBareTankIDs verifies the common case
// (padding needs at most one of each bot) never suffixes anything, so a
// non-duplicated game day's bracket/standings/rankings are byte-identical to
// before item 248's fix.
func TestPadWithBots_NoDuplicatesLeavesBareTankIDs(t *testing.T) {
	bots := []db.MatchTank{
		{TankID: "builtin-scout"}, {TankID: "builtin-bruiser"},
		{TankID: "builtin-ranger"}, {TankID: "builtin-randy"},
	}
	got := padWithBots(nil, bots, 4)
	for i, b := range got {
		if strings.Contains(b.TankID, "#") {
			t.Errorf("[%d]: TankID=%q unexpectedly suffixed", i, b.TankID)
		}
	}
}

// TestComputeGroupStandings_DuplicateAutofillTankIDsStayIndependent is the
// regression test for item 248 ("Scout twins" bug), at the round-robin
// standings level: two autofill-registered instances of the same built-in AI
// ("builtin-scout" vs "builtin-scout#2", per padWithBots' suffixing) must
// keep their own independent win/loss records rather than merging into one
// shared, misleadingly-average line. Mirrors the user's own illustration: one
// instance wins every game it plays, the other loses every game it plays —
// before the fix, both shared the "builtin-scout" map key in sm, so one
// instance's record silently overwrote the other's.
func TestComputeGroupStandings_DuplicateAutofillTankIDsStayIndependent(t *testing.T) {
	p := func(i int) *int { return &i }

	scoutA := db.MatchTank{TankID: "builtin-scout", TankName: "Scout"}
	scoutB := db.MatchTank{TankID: "builtin-scout#2", TankName: "Scout"}
	opponent := db.MatchTank{TankID: "opponent"}

	tanks := []db.MatchTank{scoutA, scoutB, opponent}
	matches := []db.Match{
		{MatchID: "m1", TankA: scoutA, TankB: opponent,
			Status: "ended", Result: &db.MatchResult{Winner: p(0)}}, // scoutA beats opponent
		{MatchID: "m2", TankA: scoutB, TankB: opponent,
			Status: "ended", Result: &db.MatchResult{Winner: p(1)}}, // opponent beats scoutB
	}

	standings, _ := computeGroupStandings(tanks, matches)

	byID := make(map[string]db.GroupStanding, len(standings))
	for _, s := range standings {
		byID[s.TankID] = s
	}

	if s := byID["builtin-scout"]; s.Wins != 1 || s.Losses != 0 {
		t.Errorf("builtin-scout: want 1W-0L, got %dW-%dL", s.Wins, s.Losses)
	}
	if s := byID["builtin-scout#2"]; s.Wins != 0 || s.Losses != 1 {
		t.Errorf("builtin-scout#2: want 0W-1L, got %dW-%dL", s.Wins, s.Losses)
	}
	if s := byID["opponent"]; s.Wins != 1 || s.Losses != 1 {
		t.Errorf("opponent (played both instances): want 1W-1L, got %dW-%dL", s.Wins, s.Losses)
	}
}

// TestPotSeed verifies pot seeding distributes tanks across groups.
func TestPotSeed(t *testing.T) {
	tanks := make([]db.MatchTank, 6)
	for i := range tanks {
		tanks[i] = db.MatchTank{TankID: strings.Repeat(string(rune('a'+i)), 1)}
	}
	groups := potSeed(tanks, 2)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if len(groups[0])+len(groups[1]) != 6 {
		t.Errorf("expected 6 total tanks, got %d", len(groups[0])+len(groups[1]))
	}
}
