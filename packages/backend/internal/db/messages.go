package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// messageRetentionDays is the resolved retention window for item 223's
// chat (2026-07-09 spec resolution): messages TTL-expire 30 days after
// sentAt, with no manual delete UI.
const messageRetentionDays = 30

// Message is a single 1:1 chat message between two accepted friends,
// gated by a not-blocked friendship check at the handler layer (item 226).
type Message struct {
	ConversationID string `dynamodbav:"conversationId" json:"-"`
	MessageID      string `dynamodbav:"messageId"      json:"messageId"`
	SenderID       string `dynamodbav:"senderId"       json:"senderId"`
	RecipientID    string `dynamodbav:"recipientId"    json:"recipientId"`
	Body           string `dynamodbav:"body"           json:"body"`
	SentAt         int64  `dynamodbav:"sentAt"         json:"sentAt"`
	TTL            int64  `dynamodbav:"ttl"            json:"-"`
}

// ConversationID returns the stable, order-independent ID for a 1:1
// conversation between two users — the two IDs sorted and joined, so
// either participant computes the same key.
func ConversationID(userA, userB string) string {
	ids := []string{userA, userB}
	sort.Strings(ids)
	return ids[0] + "#" + ids[1]
}

// newMessageID returns a sort key that orders lexicographically the same as
// chronologically: a zero-padded millisecond timestamp (good for centuries)
// plus a random suffix to break ties between messages sent in the same
// millisecond.
func newMessageID(sentAt time.Time) string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("%020d-%s", sentAt.UnixMilli(), hex.EncodeToString(buf))
}

// SendMessage writes a new message to senderID/recipientID's conversation.
// Callers must check friendship/block status first — this method doesn't.
func (s *Store) SendMessage(ctx context.Context, senderID, recipientID, body string) (Message, error) {
	now := time.Now()
	m := Message{
		ConversationID: ConversationID(senderID, recipientID),
		MessageID:      newMessageID(now),
		SenderID:       senderID,
		RecipientID:    recipientID,
		Body:           body,
		SentAt:         now.Unix(),
		TTL:            now.AddDate(0, 0, messageRetentionDays).Unix(),
	}
	item, err := attributevalue.MarshalMap(m)
	if err != nil {
		return Message{}, fmt.Errorf("SendMessage marshal: %w", err)
	}
	if _, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.messagesTable),
		Item:      item,
	}); err != nil {
		return Message{}, fmt.Errorf("SendMessage put: %w", err)
	}
	return m, nil
}

// ListMessages returns messages in a conversation in chronological order.
// If sinceMessageID is non-empty, only messages after it are returned
// (used for polling); otherwise the most recent limit messages are returned.
func (s *Store) ListMessages(ctx context.Context, conversationID, sinceMessageID string, limit int32) ([]Message, error) {
	keyCond := expression.Key("conversationId").Equal(expression.Value(conversationID))
	scanForward := true
	if sinceMessageID != "" {
		keyCond = keyCond.And(expression.Key("messageId").GreaterThan(expression.Value(sinceMessageID)))
	} else {
		// No cursor: caller wants the most recent page — scan backwards and
		// re-sort ascending below so the response is always chronological.
		scanForward = false
	}
	expr, err := expression.NewBuilder().WithKeyCondition(keyCond).Build()
	if err != nil {
		return nil, fmt.Errorf("build expression: %w", err)
	}
	out, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:                 aws.String(s.messagesTable),
		KeyConditionExpression:    expr.KeyCondition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		ScanIndexForward:          aws.Bool(scanForward),
		Limit:                     aws.Int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("ListMessages query: %w", err)
	}
	var messages []Message
	if err := attributevalue.UnmarshalListOfMaps(out.Items, &messages); err != nil {
		return nil, fmt.Errorf("ListMessages unmarshal: %w", err)
	}
	if !scanForward {
		for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
			messages[i], messages[j] = messages[j], messages[i]
		}
	}
	return messages, nil
}

// GetLatestMessage returns the single most recent message in a conversation,
// or ErrNotFound if the conversation has no messages (used to surface
// unread-badge state on the friends list without a separate read-receipt
// schema).
func (s *Store) GetLatestMessage(ctx context.Context, conversationID string) (Message, error) {
	keyCond := expression.Key("conversationId").Equal(expression.Value(conversationID))
	expr, err := expression.NewBuilder().WithKeyCondition(keyCond).Build()
	if err != nil {
		return Message{}, fmt.Errorf("build expression: %w", err)
	}
	out, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:                 aws.String(s.messagesTable),
		KeyConditionExpression:    expr.KeyCondition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		ScanIndexForward:          aws.Bool(false),
		Limit:                     aws.Int32(1),
	})
	if err != nil {
		return Message{}, fmt.Errorf("GetLatestMessage query: %w", err)
	}
	if len(out.Items) == 0 {
		return Message{}, ErrNotFound
	}
	var m Message
	if err := attributevalue.UnmarshalMap(out.Items[0], &m); err != nil {
		return Message{}, fmt.Errorf("GetLatestMessage unmarshal: %w", err)
	}
	return m, nil
}
