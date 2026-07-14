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
	// DisplayName is the durable source of truth for a user's chosen
	// in-game name (item 225). Unlike Cognito's given_name attribute, it
	// survives federated (Google/Facebook) re-logins: the IdP attribute
	// mapping in auth-stack.ts only ever touches given_name, re-syncing it
	// to the provider's real name on every sign-in, so given_name can't be
	// trusted as a durable custom-name store for federated accounts.
	DisplayName string `dynamodbav:"displayName,omitempty"  json:"displayName,omitempty"`
	// AvatarURL is the durable source of truth for a user's profile picture
	// (item 229), mirroring DisplayName above: auth-stack.ts's Google/Facebook
	// IdP attribute mapping re-syncs Cognito's "picture" attribute from the
	// provider's photo on every federated sign-in, silently overwriting an
	// uploaded avatar. AvatarURL isn't touched by that resync, so it's
	// preferred over the Cognito attribute everywhere an avatar is resolved.
	AvatarURL string `dynamodbav:"avatarUrl,omitempty" json:"avatarUrl,omitempty"`
	// LastLoginAt is a Unix timestamp set by cmd/post-auth-trigger, a Cognito
	// PostAuthentication Lambda trigger (item 241) — the only way to get a
	// "last sign-in" time, since Cognito's ListUsers/AdminGetUser expose no
	// such field natively. Fires on every successful sign-in regardless of
	// IdP (native or federated). Absent for users who haven't signed in
	// since this shipped — there is no historical data to backfill.
	LastLoginAt int64 `dynamodbav:"lastLoginAt,omitempty" json:"lastLoginAt,omitempty"`
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

// UpdateLastLogin sets LastLoginAt (item 241), upserting the UserSettings
// item if a user signs in before ever hitting a path that would otherwise
// create one (e.g. compiling or changing tier). tier defaults to Free via
// if_not_exists so a bare item created here never leaves Tier blank —
// GetUserSettings only applies that default when the item is missing
// entirely, not when it exists with an empty Tier field.
func (s *Store) UpdateLastLogin(ctx context.Context, userID string, unixSeconds int64) error {
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:        aws.String(s.userSettingsTable),
		Key:              userSettingsKey(userID),
		UpdateExpression: aws.String("SET lastLoginAt = :t, tier = if_not_exists(tier, :defaultTier)"),
		ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
			":t":           &dbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", unixSeconds)},
			":defaultTier": strAttr(TierFree),
		},
	})
	if err != nil {
		return fmt.Errorf("UpdateLastLogin: %w", err)
	}
	return nil
}
