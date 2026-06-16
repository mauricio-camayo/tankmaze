package db

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const (
	PhaseRoundRobin = "roundRobin"
	PhaseFinal      = "final"
	// Elimination phase keys follow the pattern "r1", "r2", … (e.g. "r" + strconv.Itoa(round)).
)

// ListGameDays returns all Game Day records sorted by createdAt descending.
func (s *Store) ListGameDays(ctx context.Context) ([]GameDay, error) {
	out, err := s.db.Scan(ctx, &dynamodb.ScanInput{
		TableName: &s.gamedaysTable,
	})
	if err != nil {
		return nil, fmt.Errorf("scan gamedays: %w", err)
	}
	gds := make([]GameDay, 0, len(out.Items))
	for _, item := range out.Items {
		var gd GameDay
		if err := attributevalue.UnmarshalMap(item, &gd); err != nil {
			return nil, fmt.Errorf("unmarshal gameday: %w", err)
		}
		gds = append(gds, gd)
	}
	sort.Slice(gds, func(i, j int) bool {
		return gds[i].CreatedAt > gds[j].CreatedAt
	})
	return gds, nil
}

// DeleteGameDay removes the Game Day record with the given gameDayId.
func (s *Store) DeleteGameDay(ctx context.Context, gameDayID string) error {
	_, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &s.gamedaysTable,
		Key:       gamedayKey(gameDayID),
	})
	if err != nil {
		return fmt.Errorf("delete gameday %s: %w", gameDayID, err)
	}
	return nil
}

// PutGameDay writes a Game Day record using optimistic locking on the Version
// field. It increments Version before writing and uses a ConditionExpression to
// ensure no concurrent writer has modified the record since it was read.
//
// For a brand-new record (Version == 0) the condition is attribute_not_exists,
// preventing accidental overwrites of a record that was concurrently created.
// For existing records the condition is version = :expected.
//
// Returns ErrConflict when the condition fails.
func (s *Store) PutGameDay(ctx context.Context, gd GameDay) error {
	expectedVersion := gd.Version
	gd.Version = expectedVersion + 1

	item, err := attributevalue.MarshalMap(gd)
	if err != nil {
		return fmt.Errorf("marshal gameday: %w", err)
	}

	input := &dynamodb.PutItemInput{
		TableName: &s.gamedaysTable,
		Item:      item,
	}

	if expectedVersion == 0 {
		// Version 0 covers two cases:
		//   1. Truly new record — attribute_not_exists(gameDayId)
		//   2. Record written before OCC was introduced (no version attribute) —
		//      attribute_not_exists(#v)
		// Both must be accepted so that pre-existing records can be upgraded in
		// place without a migration.
		cond := "attribute_not_exists(gameDayId) OR attribute_not_exists(#v) OR #v = :zero"
		input.ConditionExpression = aws.String(cond)
		input.ExpressionAttributeNames = map[string]string{"#v": "version"}
		input.ExpressionAttributeValues = map[string]dbtypes.AttributeValue{
			":zero": &dbtypes.AttributeValueMemberN{Value: "0"},
		}
	} else {
		// Existing record: version must match what we read.
		cond := "#v = :expected"
		input.ConditionExpression = aws.String(cond)
		input.ExpressionAttributeNames = map[string]string{"#v": "version"}
		input.ExpressionAttributeValues = map[string]dbtypes.AttributeValue{
			":expected": &dbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", expectedVersion)},
		}
	}

	_, putErr := s.db.PutItem(ctx, input)
	if putErr != nil {
		var ccf *dbtypes.ConditionalCheckFailedException
		if errors.As(putErr, &ccf) {
			return ErrConflict
		}
		return putErr
	}
	return nil
}

// GetGameDay returns the Game Day with the given gameDayId. Returns ErrNotFound
// if absent.
func (s *Store) GetGameDay(ctx context.Context, gameDayID string) (GameDay, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.gamedaysTable,
		Key:       gamedayKey(gameDayID),
	})
	if err != nil {
		return GameDay{}, fmt.Errorf("get gameday %s: %w", gameDayID, err)
	}
	if len(out.Item) == 0 {
		return GameDay{}, ErrNotFound
	}
	var gd GameDay
	if err := attributevalue.UnmarshalMap(out.Item, &gd); err != nil {
		return GameDay{}, fmt.Errorf("unmarshal gameday %s: %w", gameDayID, err)
	}
	return gd, nil
}

// UpdateGameDayPhase updates a single phase's status within a Game Day.
// phase should be PhaseRoundRobin, PhaseFinal, or an elimination round key
// such as "r1", "r2", etc.
//
// This operation uses a read-modify-write pattern. It is safe for the
// sequential, event-driven tournament-scheduler access pattern.
func (s *Store) UpdateGameDayPhase(ctx context.Context, gameDayID, phase string, status PhaseStatus) error {
	gd, err := s.GetGameDay(ctx, gameDayID)
	if err != nil {
		return err
	}
	switch phase {
	case PhaseRoundRobin:
		gd.Phases.RoundRobin = status
	case PhaseFinal:
		gd.Phases.Final = status
	default:
		if gd.Phases.Elimination == nil {
			gd.Phases.Elimination = make(map[string]PhaseStatus)
		}
		gd.Phases.Elimination[phase] = status
	}
	return s.PutGameDay(ctx, gd)
}

// UpdateGameDayGroups sets the round-robin group assignments after pot seeding.
func (s *Store) UpdateGameDayGroups(ctx context.Context, gameDayID string, groups []Group) error {
	gd, err := s.GetGameDay(ctx, gameDayID)
	if err != nil {
		return err
	}
	gd.Groups = groups
	return s.PutGameDay(ctx, gd)
}

// UpdateGameDayBracket replaces the elimination bracket state. bracket is
// keyed by round ("r1", "r2", …); each value is the ordered list of slots.
func (s *Store) UpdateGameDayBracket(ctx context.Context, gameDayID string, bracket map[string][]BracketSlot) error {
	gd, err := s.GetGameDay(ctx, gameDayID)
	if err != nil {
		return err
	}
	gd.Bracket = bracket
	return s.PutGameDay(ctx, gd)
}

// SetGameDayPlacementPoints records the final placement points for all tanks
// and is written once by ranking-updater when the Game Day completes.
func (s *Store) SetGameDayPlacementPoints(ctx context.Context, gameDayID string, points map[string]int) error {
	gd, err := s.GetGameDay(ctx, gameDayID)
	if err != nil {
		return err
	}
	gd.PlacementPoints = points
	return s.PutGameDay(ctx, gd)
}

// GameDayUpdate carries the mutable fields that PATCH /gamedays/{id} may change.
// Only non-zero fields are applied; zero values leave the existing value unchanged.
type GameDayUpdate struct {
	Name                string    // empty = no change
	RegistrationCloseAt string    // ISO 8601; empty = no change
	RoundRobinAt        string
	EliminationAt       []string  // nil = no change; non-nil replaces the whole slice
	FinalAt             string
	Autofill            *bool     // nil = no change
	ForcedMapIDs        *[]string // nil = no change; non-nil (even empty) replaces
	RandomMaps          *bool     // nil = no change
}

// ErrGameDayStarted is returned by UpdateGameDay when any phase has already
// progressed past "upcoming", making schedule edits unsafe.
var ErrGameDayStarted = fmt.Errorf("game day has already started")

// UpdateGameDay applies mutable field changes to a Game Day using a
// read-modify-write. Returns ErrGameDayStarted if RoundRobin.Status != "upcoming".
func (s *Store) UpdateGameDay(ctx context.Context, gameDayID string, u GameDayUpdate) error {
	gd, err := s.GetGameDay(ctx, gameDayID)
	if err != nil {
		return err
	}
	if gd.Phases.RoundRobin.Status != "upcoming" {
		return ErrGameDayStarted
	}
	if u.Name != "" {
		gd.Name = u.Name
	}
	if u.RegistrationCloseAt != "" {
		gd.Schedule.RegistrationClose = u.RegistrationCloseAt
	}
	if u.RoundRobinAt != "" {
		gd.Schedule.RoundRobin = u.RoundRobinAt
	}
	if u.EliminationAt != nil {
		gd.Schedule.Elimination = u.EliminationAt
	}
	if u.FinalAt != "" {
		gd.Schedule.Final = u.FinalAt
	}
	if u.Autofill != nil {
		gd.Autofill = *u.Autofill
	}
	if u.ForcedMapIDs != nil {
		gd.ForcedMapIDs = *u.ForcedMapIDs
	}
	if u.RandomMaps != nil {
		gd.RandomMaps = *u.RandomMaps
	}
	return s.PutGameDay(ctx, gd)
}

// AddRosterEntry appends a tank/version to the game day roster. For user tanks
// it is a no-op if the tank is already present. AI tanks (tankID starting with
// "builtin-") may appear more than once so that auto-fill can pad a bracket
// with multiple instances of the same bot. Returns ErrNotFound if the game day
// doesn't exist.
func (s *Store) AddRosterEntry(ctx context.Context, gameDayID, tankID, version, tankName string) error {
	isAI := strings.HasPrefix(tankID, "builtin-")
	gd, err := s.GetGameDay(ctx, gameDayID)
	if err != nil {
		return err
	}
	if !isAI {
		for _, t := range gd.RegisteredTanks {
			if t.TankID == tankID {
				return nil
			}
		}
	}
	gd.RegisteredTanks = append(gd.RegisteredTanks, MatchTank{TankID: tankID, Version: version, TankName: tankName})
	return s.PutGameDay(ctx, gd)
}

// RemoveRosterEntry removes a tank from the game day roster.
// Returns ErrNotFound if the game day doesn't exist.
func (s *Store) RemoveRosterEntry(ctx context.Context, gameDayID, tankID string) error {
	gd, err := s.GetGameDay(ctx, gameDayID)
	if err != nil {
		return err
	}
	filtered := gd.RegisteredTanks[:0]
	for _, t := range gd.RegisteredTanks {
		if t.TankID != tankID {
			filtered = append(filtered, t)
		}
	}
	gd.RegisteredTanks = filtered
	return s.PutGameDay(ctx, gd)
}
