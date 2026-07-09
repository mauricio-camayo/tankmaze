package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Friendship status values. FriendshipBlocked (item 226) reuses this same
// table/dual-item model rather than a separate tankmaze-blocks table — the
// RequestedBy field is repurposed to mean "who blocked" for a blocked-status
// row, tracked so only that user can unblock.
const (
	FriendshipPending  = "pending"
	FriendshipAccepted = "accepted"
	FriendshipBlocked  = "blocked"
)

// ErrNotBlocker is returned by UnblockUser when the caller isn't the user who
// placed the block (item 226: "only the blocker can unblock").
var ErrNotBlocker = errors.New("only the user who placed the block can unblock")

// Friendship is stored as a pair of items — one keyed (userId=A, friendId=B)
// and one keyed (userId=B, friendId=A) — so a lookup from either side of the
// relationship is a single GetItem/Query against that user's own partition,
// with no GSI or canonicalized pair-key needed (item 223).
type Friendship struct {
	UserID      string `dynamodbav:"userId"      json:"userId"`
	FriendID    string `dynamodbav:"friendId"    json:"friendId"`
	Status      string `dynamodbav:"status"      json:"status"`
	RequestedBy string `dynamodbav:"requestedBy" json:"requestedBy"`
	CreatedAt   int64  `dynamodbav:"createdAt"   json:"createdAt"`
}

func friendshipKey(userID, friendID string) map[string]dbtypes.AttributeValue {
	return map[string]dbtypes.AttributeValue{
		"userId":   strAttr(userID),
		"friendId": strAttr(friendID),
	}
}

// GetFriendship returns the relationship between userID and friendID from
// userID's own side, or ErrNotFound if none exists (no request sent or
// received, in either direction).
func (s *Store) GetFriendship(ctx context.Context, userID, friendID string) (Friendship, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.friendshipsTable),
		Key:       friendshipKey(userID, friendID),
	})
	if err != nil {
		return Friendship{}, fmt.Errorf("GetFriendship: %w", err)
	}
	if len(out.Item) == 0 {
		return Friendship{}, ErrNotFound
	}
	var f Friendship
	if err := attributevalue.UnmarshalMap(out.Item, &f); err != nil {
		return Friendship{}, fmt.Errorf("GetFriendship unmarshal: %w", err)
	}
	return f, nil
}

// SendFriendRequest writes both sides of a pending friendship. Callers must
// check GetFriendship first to reject duplicate/self requests — this method
// does not check for an existing relationship.
func (s *Store) SendFriendRequest(ctx context.Context, fromUserID, toUserID string) error {
	now := time.Now().Unix()
	for _, item := range []Friendship{
		{UserID: fromUserID, FriendID: toUserID, Status: FriendshipPending, RequestedBy: fromUserID, CreatedAt: now},
		{UserID: toUserID, FriendID: fromUserID, Status: FriendshipPending, RequestedBy: fromUserID, CreatedAt: now},
	} {
		av, err := attributevalue.MarshalMap(item)
		if err != nil {
			return fmt.Errorf("SendFriendRequest marshal: %w", err)
		}
		if _, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(s.friendshipsTable),
			Item:      av,
		}); err != nil {
			return fmt.Errorf("SendFriendRequest put: %w", err)
		}
	}
	return nil
}

// AcceptFriendRequest flips both sides of a pending friendship to accepted.
func (s *Store) AcceptFriendRequest(ctx context.Context, userID, friendID string) error {
	for _, pair := range [][2]string{{userID, friendID}, {friendID, userID}} {
		_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:        aws.String(s.friendshipsTable),
			Key:              friendshipKey(pair[0], pair[1]),
			UpdateExpression: aws.String("SET #status = :accepted"),
			ExpressionAttributeNames: map[string]string{
				"#status": "status",
			},
			ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
				":accepted": strAttr(FriendshipAccepted),
			},
		})
		if err != nil {
			return fmt.Errorf("AcceptFriendRequest: %w", err)
		}
	}
	return nil
}

// RemoveFriendship deletes both sides of a relationship — used for rejecting
// a pending request, cancelling an outgoing one, and unfriending an accepted
// friend; all three are "delete the pairing" from the data model's view.
func (s *Store) RemoveFriendship(ctx context.Context, userID, friendID string) error {
	for _, pair := range [][2]string{{userID, friendID}, {friendID, userID}} {
		_, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName: aws.String(s.friendshipsTable),
			Key:       friendshipKey(pair[0], pair[1]),
		})
		if err != nil {
			return fmt.Errorf("RemoveFriendship: %w", err)
		}
	}
	return nil
}

// BlockUser removes any existing friendship between blockerID and targetID
// (equivalent to unfriend, per item 226) and writes a blocked-status pair in
// its place. RequestedBy is repurposed to record who placed the block, so
// UnblockUser can enforce that only that user may lift it.
func (s *Store) BlockUser(ctx context.Context, blockerID, targetID string) error {
	if err := s.RemoveFriendship(ctx, blockerID, targetID); err != nil {
		return fmt.Errorf("BlockUser remove existing: %w", err)
	}
	now := time.Now().Unix()
	for _, item := range []Friendship{
		{UserID: blockerID, FriendID: targetID, Status: FriendshipBlocked, RequestedBy: blockerID, CreatedAt: now},
		{UserID: targetID, FriendID: blockerID, Status: FriendshipBlocked, RequestedBy: blockerID, CreatedAt: now},
	} {
		av, err := attributevalue.MarshalMap(item)
		if err != nil {
			return fmt.Errorf("BlockUser marshal: %w", err)
		}
		if _, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(s.friendshipsTable),
			Item:      av,
		}); err != nil {
			return fmt.Errorf("BlockUser put: %w", err)
		}
	}
	return nil
}

// UnblockUser removes a blocked-status pair, but only if callerID is the one
// who placed the block. Returns ErrNotFound if there's no block between the
// two users, or ErrNotBlocker if callerID was the one who got blocked rather
// than the one who did the blocking.
func (s *Store) UnblockUser(ctx context.Context, callerID, targetID string) error {
	f, err := s.GetFriendship(ctx, callerID, targetID)
	if err != nil {
		return err
	}
	if f.Status != FriendshipBlocked {
		return ErrNotFound
	}
	if f.RequestedBy != callerID {
		return ErrNotBlocker
	}
	return s.RemoveFriendship(ctx, callerID, targetID)
}

// ListFriendships returns every relationship (pending or accepted, in either
// direction) involving userID, from userID's own partition.
func (s *Store) ListFriendships(ctx context.Context, userID string) ([]Friendship, error) {
	keyExpr := expression.Key("userId").Equal(expression.Value(userID))
	expr, err := expression.NewBuilder().WithKeyCondition(keyExpr).Build()
	if err != nil {
		return nil, fmt.Errorf("build expression: %w", err)
	}
	input := &dynamodb.QueryInput{
		TableName:                 aws.String(s.friendshipsTable),
		KeyConditionExpression:    expr.KeyCondition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	}
	friendships := []Friendship{}
	for {
		out, err := s.db.Query(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("query friendships for user %s: %w", userID, err)
		}
		var batch []Friendship
		if err := attributevalue.UnmarshalListOfMaps(out.Items, &batch); err != nil {
			return nil, fmt.Errorf("unmarshal friendships: %w", err)
		}
		friendships = append(friendships, batch...)
		if out.LastEvaluatedKey == nil {
			break
		}
		input.ExclusiveStartKey = out.LastEvaluatedKey
	}
	return friendships, nil
}
