package db

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Recurrence frequencies (item 238).
const (
	FreqWeekly     = "weekly"
	FreqMonthly    = "monthly"
	FreqEveryNDays = "every_n_days"
)

const (
	SeriesStatusActive    = "active"
	SeriesStatusCancelled = "cancelled"
	SeriesStatusFinished  = "finished" // MaxOccurrences reached
)

// GameDaySeries is a recurring Game Day template (item 238). Rather than
// pre-creating every future occurrence, a rolling job (cmd/series-materializer)
// keeps only the next occurrence materialized as a real GameDay record ahead
// of time (see NextOccurrenceAt), creating the following one once that fires.
type GameDaySeries struct {
	SeriesID     string `dynamodbav:"seriesId"               json:"seriesId"`
	Name         string `dynamodbav:"name,omitempty"         json:"name,omitempty"`
	Frequency    string `dynamodbav:"frequency"              json:"frequency"`              // weekly | monthly | every_n_days
	ByMonthDay   int    `dynamodbav:"byMonthDay,omitempty"   json:"byMonthDay,omitempty"`   // monthly only, 1-31
	IntervalDays int    `dynamodbav:"intervalDays,omitempty" json:"intervalDays,omitempty"` // every_n_days only

	// RegistrationLeadSeconds / FinalLeadSeconds are captured from the first
	// occurrence's gaps and reapplied to every subsequent one: registration
	// closes this many seconds before roundRobin, and final starts this many
	// seconds after roundRobin.
	RegistrationLeadSeconds int64 `dynamodbav:"registrationLeadSeconds" json:"registrationLeadSeconds"`
	FinalLeadSeconds        int64 `dynamodbav:"finalLeadSeconds"        json:"finalLeadSeconds"`

	Autofill     bool     `dynamodbav:"autofill,omitempty"     json:"autofill,omitempty"`
	ForcedMapIDs []string `dynamodbav:"forcedMapIds,omitempty" json:"forcedMapIds,omitempty"`
	RandomMaps   bool     `dynamodbav:"randomMaps,omitempty"   json:"randomMaps,omitempty"`

	// MaxOccurrences is 0 for indefinite repetition, or a fixed repeat count.
	MaxOccurrences     int `dynamodbav:"maxOccurrences,omitempty"     json:"maxOccurrences,omitempty"`
	OccurrencesCreated int `dynamodbav:"occurrencesCreated"           json:"occurrencesCreated"`

	// NextOccurrenceAt is the roundRobin timestamp (ISO 8601 UTC) of the next
	// occurrence to materialize. Advanced by cmd/series-materializer after
	// each materialization using AdvanceGameDaySeries's optimistic lock.
	NextOccurrenceAt string `dynamodbav:"nextOccurrenceAt" json:"nextOccurrenceAt"`

	Status    string `dynamodbav:"status"    json:"status"` // active | cancelled | finished
	CreatedAt int64  `dynamodbav:"createdAt" json:"createdAt"`
}

func seriesKey(seriesID string) map[string]dbtypes.AttributeValue {
	return map[string]dbtypes.AttributeValue{"seriesId": strAttr(seriesID)}
}

// PutGameDaySeries creates a brand-new series record.
func (s *Store) PutGameDaySeries(ctx context.Context, series GameDaySeries) error {
	item, err := attributevalue.MarshalMap(series)
	if err != nil {
		return fmt.Errorf("marshal gameday series: %w", err)
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.gamedaySeriesTable),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("put gameday series: %w", err)
	}
	return nil
}

// GetGameDaySeries returns a series by ID, or ErrNotFound.
func (s *Store) GetGameDaySeries(ctx context.Context, seriesID string) (GameDaySeries, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.gamedaySeriesTable),
		Key:       seriesKey(seriesID),
	})
	if err != nil {
		return GameDaySeries{}, fmt.Errorf("get gameday series: %w", err)
	}
	if len(out.Item) == 0 {
		return GameDaySeries{}, ErrNotFound
	}
	var series GameDaySeries
	if err := attributevalue.UnmarshalMap(out.Item, &series); err != nil {
		return GameDaySeries{}, fmt.Errorf("unmarshal gameday series: %w", err)
	}
	return series, nil
}

// ListGameDaySeries returns all series, newest first.
func (s *Store) ListGameDaySeries(ctx context.Context) ([]GameDaySeries, error) {
	out, err := s.db.Scan(ctx, &dynamodb.ScanInput{
		TableName: aws.String(s.gamedaySeriesTable),
	})
	if err != nil {
		return nil, fmt.Errorf("scan gameday series: %w", err)
	}
	all := make([]GameDaySeries, 0, len(out.Items))
	for _, item := range out.Items {
		var series GameDaySeries
		if err := attributevalue.UnmarshalMap(item, &series); err != nil {
			return nil, fmt.Errorf("unmarshal gameday series: %w", err)
		}
		all = append(all, series)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt > all[j].CreatedAt })
	return all, nil
}

// ListActiveGameDaySeriesDue returns active series whose NextOccurrenceAt is
// at or before the given cutoff — used by cmd/series-materializer to decide
// what needs the next occurrence materialized.
func (s *Store) ListActiveGameDaySeriesDue(ctx context.Context, cutoff time.Time) ([]GameDaySeries, error) {
	all, err := s.ListGameDaySeries(ctx)
	if err != nil {
		return nil, err
	}
	due := make([]GameDaySeries, 0)
	for _, series := range all {
		if series.Status != SeriesStatusActive {
			continue
		}
		next, err := time.Parse(time.RFC3339, series.NextOccurrenceAt)
		if err != nil {
			continue
		}
		if !next.After(cutoff) {
			due = append(due, series)
		}
	}
	return due, nil
}

// CancelGameDaySeries stops future materialization. Already-materialized
// occurrences (past or upcoming GameDay records) are untouched — cancelling
// a series never retroactively affects them (item 238, point 6), matching
// how DELETE /gamedays/{id} already treats a single occurrence independently
// of any series it may belong to.
func (s *Store) CancelGameDaySeries(ctx context.Context, seriesID string) error {
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:        aws.String(s.gamedaySeriesTable),
		Key:              seriesKey(seriesID),
		UpdateExpression: aws.String("SET #st = :cancelled"),
		ExpressionAttributeNames: map[string]string{
			"#st": "status",
		},
		ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
			":cancelled": strAttr(SeriesStatusCancelled),
		},
	})
	if err != nil {
		return fmt.Errorf("cancel gameday series: %w", err)
	}
	return nil
}

// AdvanceGameDaySeries records that the occurrence at prevNextAt has been
// materialized and moves the series on to nextAt (or marks it finished if
// maxOccurrences has now been reached). The ConditionExpression on
// nextOccurrenceAt is an optimistic lock: if another concurrent
// series-materializer run already advanced this series, this call fails
// with ErrConflict instead of double-materializing the same occurrence.
func (s *Store) AdvanceGameDaySeries(ctx context.Context, seriesID, prevNextAt, nextAt string, finished bool) error {
	status := SeriesStatusActive
	if finished {
		status = SeriesStatusFinished
	}
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.gamedaySeriesTable),
		Key:       seriesKey(seriesID),
		UpdateExpression: aws.String(
			"SET nextOccurrenceAt = :next, #st = :status ADD occurrencesCreated :one",
		),
		ConditionExpression: aws.String("nextOccurrenceAt = :prev"),
		ExpressionAttributeNames: map[string]string{
			"#st": "status",
		},
		ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
			":next":   strAttr(nextAt),
			":prev":   strAttr(prevNextAt),
			":status": strAttr(status),
			":one":    &dbtypes.AttributeValueMemberN{Value: "1"},
		},
	})
	if err != nil {
		var ccf *dbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrConflict
		}
		return fmt.Errorf("advance gameday series: %w", err)
	}
	return nil
}

// NextOccurrenceTime computes the next occurrence's roundRobin time from the
// current one, per the series' recurrence rule. Monthly clamps to the last
// day of the target month when byMonthDay exceeds it (e.g. day 31 in a
// 30-day month lands on the 30th, not rolling into the following month).
func NextOccurrenceTime(series GameDaySeries, from time.Time) time.Time {
	switch series.Frequency {
	case FreqWeekly:
		return from.AddDate(0, 0, 7)
	case FreqMonthly:
		y, m, _ := from.Date()
		targetMonth := time.Date(y, m+1, 1, 0, 0, 0, 0, from.Location())
		lastDay := targetMonth.AddDate(0, 1, -1).Day()
		day := series.ByMonthDay
		if day <= 0 || day > lastDay {
			day = lastDay
		}
		return time.Date(y, m+1, day, from.Hour(), from.Minute(), from.Second(), 0, from.Location())
	case FreqEveryNDays:
		n := series.IntervalDays
		if n <= 0 {
			n = 1
		}
		return from.AddDate(0, 0, n)
	default:
		return from.AddDate(0, 0, 7)
	}
}
