package db

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
)

// marshalRoundtrip marshals v to a DynamoDB attribute map and unmarshals it
// back into a zero value of the same type, returning the result.
func marshalRoundtrip[T any](t *testing.T, v T) T {
	t.Helper()
	item, err := attributevalue.MarshalMap(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out T
	if err := attributevalue.UnmarshalMap(item, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestTankRoundtrip(t *testing.T) {
	placement := 2
	orig := Tank{
		TankID:        "tank-123",
		UserID:        "user-abc",
		Name:          "Destroyer",
		GlobalScore:   450,
		BestFinish:    &placement,
		GameDaysCount: 3,
		LastActiveAt:  1700000000,
		CreatedAt:     1699000000,
	}
	got := marshalRoundtrip(t, orig)
	if got.TankID != orig.TankID || got.GlobalScore != orig.GlobalScore ||
		got.GameDaysCount != orig.GameDaysCount || got.Name != orig.Name {
		t.Errorf("scalar fields mismatch: got %+v", got)
	}
	if got.BestFinish == nil || *got.BestFinish != placement {
		t.Errorf("BestFinish: got %v, want %d", got.BestFinish, placement)
	}
}

func TestTankNilBestFinish(t *testing.T) {
	orig := Tank{TankID: "t1", BestFinish: nil}
	got := marshalRoundtrip(t, orig)
	if got.BestFinish != nil {
		t.Errorf("expected nil BestFinish, got %v", got.BestFinish)
	}
}

func TestMatchResultNilWinner(t *testing.T) {
	orig := MatchResult{
		Winner:       nil,
		Reason:       "both_lose",
		DamageA:      20,
		DamageB:      20,
		TicksElapsed: 100,
	}
	item, err := attributevalue.MarshalMap(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got MatchResult
	if err := attributevalue.UnmarshalMap(item, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Winner != nil {
		t.Errorf("expected nil Winner, got %v", got.Winner)
	}
	if got.Reason != "both_lose" {
		t.Errorf("expected reason both_lose, got %s", got.Reason)
	}
}

func TestMatchResultWithWinner(t *testing.T) {
	winner := 0
	orig := MatchResult{
		Winner:   &winner,
		Reason:   "opponent_destroyed",
		Flawless: true,
	}
	got := marshalRoundtrip(t, orig)
	if got.Winner == nil || *got.Winner != winner {
		t.Errorf("expected winner %d, got %v", winner, got.Winner)
	}
	if !got.Flawless {
		t.Error("expected Flawless=true")
	}
}

func TestGameDayPhasesRoundtrip(t *testing.T) {
	orig := GameDay{
		GameDayID: "gd-1",
		Phases: GameDayPhases{
			RoundRobin: PhaseStatus{Status: "complete", StartedAt: 100, EndedAt: 200},
			Elimination: map[string]PhaseStatus{
				"r1": {Status: "complete", StartedAt: 300, EndedAt: 400},
				"r2": {Status: "running", StartedAt: 500},
			},
			Final: PhaseStatus{Status: "upcoming"},
		},
		RegisteredTanks: []MatchTank{
			{TankID: "t1", Version: "v1"},
			{TankID: "t2", Version: "v2"},
		},
		CreatedAt: 99,
	}
	got := marshalRoundtrip(t, orig)
	if got.GameDayID != orig.GameDayID {
		t.Errorf("GameDayID mismatch: %s vs %s", got.GameDayID, orig.GameDayID)
	}
	if got.Phases.RoundRobin.Status != "complete" {
		t.Errorf("RoundRobin status: %s", got.Phases.RoundRobin.Status)
	}
	if got.Phases.Elimination["r2"].Status != "running" {
		t.Errorf("r2 status: %s", got.Phases.Elimination["r2"].Status)
	}
	if len(got.RegisteredTanks) != 2 {
		t.Errorf("RegisteredTanks len: %d", len(got.RegisteredTanks))
	}
}

func TestMapLayoutRoundtrip(t *testing.T) {
	layout := [][]bool{
		{true, false, true},
		{false, true, false},
		{true, false, true},
	}
	orig := Map{
		MapID:     "map-1",
		Slug:      "test",
		Layout:    layout,
		IsBuiltIn: true,
		IsActive:  true,
	}
	got := marshalRoundtrip(t, orig)
	if len(got.Layout) != 3 || len(got.Layout[0]) != 3 {
		t.Fatalf("layout dimensions wrong: %v", got.Layout)
	}
	for r := range layout {
		for c := range layout[r] {
			if got.Layout[r][c] != layout[r][c] {
				t.Errorf("layout[%d][%d]: got %v, want %v", r, c, got.Layout[r][c], layout[r][c])
			}
		}
	}
}

func TestRankingRoundtrip(t *testing.T) {
	orig := Ranking{
		TankID:    "t1",
		GameDayID: "gd-1",
		Points:    4,
		Placement: 2,
		ExpiresAt: 1800000000,
		TTL:       1800000000,
	}
	got := marshalRoundtrip(t, orig)
	if got != orig {
		t.Errorf("roundtrip mismatch:\n got  %+v\n want %+v", got, orig)
	}
}

func TestGameDayVersionRoundtrip(t *testing.T) {
	orig := GameDay{
		GameDayID: "gd-v",
		Version:   7,
		Phases: GameDayPhases{
			RoundRobin: PhaseStatus{Status: "upcoming"},
			Final:      PhaseStatus{Status: "upcoming"},
		},
		CreatedAt: 1,
	}
	got := marshalRoundtrip(t, orig)
	if got.Version != orig.Version {
		t.Errorf("Version roundtrip: got %d, want %d", got.Version, orig.Version)
	}
}

func TestBracketRoundtrip(t *testing.T) {
	orig := GameDay{
		GameDayID: "gd-2",
		Bracket: map[string][]BracketSlot{
			"r1": {
				{TankID: "t1", Version: "v1", Status: "won"},
				{TankID: "t2", Version: "v1", Status: "lost"},
			},
			"r2": {
				{TankID: "t1", Version: "v1", Status: "playing"},
				{TankID: "t3", Version: "v2", Status: "bye"},
			},
		},
		CreatedAt: 1,
	}
	got := marshalRoundtrip(t, orig)
	if len(got.Bracket["r1"]) != 2 {
		t.Errorf("r1 slots: %d", len(got.Bracket["r1"]))
	}
	if got.Bracket["r2"][1].Status != "bye" {
		t.Errorf("r2[1] status: %s", got.Bracket["r2"][1].Status)
	}
}
