package db

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const windowDays = 30

// Tier names
const (
	TierFree    = "free"
	TierBuilder = "builder"
	TierPro     = "pro"
)

// TierLimits returns tank and compilation limits for a given tier.
func TierLimits(tier string) (tanks int, compilations int) {
	switch tier {
	case TierBuilder:
		return 5, 50
	case TierPro:
		return 15, 200
	default:
		return 2, 10
	}
}

// UserSettings holds per-user subscription and quota state.
type UserSettings struct {
	UserID                 string `dynamodbav:"userId"                 json:"userId,omitempty"`
	Tier                   string `dynamodbav:"tier"                   json:"tier"`
	CompilationsThisWindow int    `dynamodbav:"compilationsThisWindow" json:"compilationsThisWindow"`
	WindowStart            string `dynamodbav:"windowStart"            json:"windowStart"`
}

func userSettingsKey(userID string) map[string]dbtypes.AttributeValue {
	return map[string]dbtypes.AttributeValue{"userId": strAttr(userID)}
}

// GetUserSettings returns the user's settings, defaulting to Free tier if not found.
func (s *Store) GetUserSettings(ctx context.Context, userID string) (UserSettings, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.userSettingsTable),
		Key:       userSettingsKey(userID),
	})
	if err != nil {
		return UserSettings{}, fmt.Errorf("GetUserSettings: %w", err)
	}
	if len(out.Item) == 0 {
		return UserSettings{UserID: userID, Tier: TierFree}, nil
	}
	var us UserSettings
	if err := attributevalue.UnmarshalMap(out.Item, &us); err != nil {
		return UserSettings{}, fmt.Errorf("GetUserSettings unmarshal: %w", err)
	}
	return us, nil
}

// PutUserSettings writes the full user settings record.
func (s *Store) PutUserSettings(ctx context.Context, us UserSettings) error {
	item, err := attributevalue.MarshalMap(us)
	if err != nil {
		return fmt.Errorf("PutUserSettings marshal: %w", err)
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.userSettingsTable),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("PutUserSettings: %w", err)
	}
	return nil
}

// ResetWindowIfExpired resets the compilation window if >30 days have elapsed.
// Returns the (possibly updated) settings and whether a reset occurred.
func ResetWindowIfExpired(us UserSettings) (UserSettings, bool) {
	if us.WindowStart == "" {
		return us, false
	}
	start, err := time.Parse(time.RFC3339, us.WindowStart)
	if err != nil {
		return us, false
	}
	if time.Since(start) >= windowDays*24*time.Hour {
		us.CompilationsThisWindow = 0
		us.WindowStart = ""
		return us, true
	}
	return us, false
}

// IncrementCompilations atomically increments the compilation counter for userID.
// It initialises WindowStart on the first compile of a new window.
// Callers must check the limit before calling this.
func (s *Store) IncrementCompilations(ctx context.Context, userID string, currentWindowStart string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	var updateExpr string
	var exprAttr map[string]dbtypes.AttributeValue
	var exprNames map[string]string

	if currentWindowStart == "" {
		// First compile: set windowStart and increment counter.
		updateExpr = "SET windowStart = :ws ADD compilationsThisWindow :one"
		exprAttr = map[string]dbtypes.AttributeValue{
			":ws":  strAttr(now),
			":one": &dbtypes.AttributeValueMemberN{Value: "1"},
		}
	} else {
		updateExpr = "ADD compilationsThisWindow :one"
		exprAttr = map[string]dbtypes.AttributeValue{
			":one": &dbtypes.AttributeValueMemberN{Value: "1"},
		}
	}

	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(s.userSettingsTable),
		Key:                       userSettingsKey(userID),
		UpdateExpression:          aws.String(updateExpr),
		ExpressionAttributeValues: exprAttr,
		ExpressionAttributeNames:  exprNames,
	})
	if err != nil {
		return fmt.Errorf("IncrementCompilations: %w", err)
	}
	return nil
}

