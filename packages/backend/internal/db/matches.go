package db

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// PutMatch writes a match record, overwriting any existing record for the same matchId.
func (s *Store) PutMatch(ctx context.Context, m Match) error {
	item, err := attributevalue.MarshalMap(m)
	if err != nil {
		return fmt.Errorf("marshal match: %w", err)
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.matchesTable,
		Item:      item,
	})
	return err
}

// GetMatch returns the match with the given matchId. Returns ErrNotFound if absent.
func (s *Store) GetMatch(ctx context.Context, matchID string) (Match, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.matchesTable,
		Key:       matchKey(matchID),
	})
	if err != nil {
		return Match{}, fmt.Errorf("get match %s: %w", matchID, err)
	}
	if len(out.Item) == 0 {
		return Match{}, ErrNotFound
	}
	var m Match
	if err := attributevalue.UnmarshalMap(out.Item, &m); err != nil {
		return Match{}, fmt.Errorf("unmarshal match %s: %w", matchID, err)
	}
	return m, nil
}

// UpdateMatchStatus updates only the status field of a match record.
func (s *Store) UpdateMatchStatus(ctx context.Context, matchID, status string) error {
	upd := expression.Set(expression.Name("status"), expression.Value(status))
	expr, err := expression.NewBuilder().WithUpdate(upd).Build()
	if err != nil {
		return fmt.Errorf("build expression: %w", err)
	}
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &s.matchesTable,
		Key:                       matchKey(matchID),
		UpdateExpression:          expr.Update(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	return err
}

// SetMatchResult atomically writes the match result, tick-log S3 key, and
// sets status to "ended". Called by match-runner on match completion.
func (s *Store) SetMatchResult(ctx context.Context, matchID, tickLogS3Key string, result MatchResult) error {
	upd := expression.Set(expression.Name("status"), expression.Value("ended")).
		Set(expression.Name("tickLogS3Key"), expression.Value(tickLogS3Key)).
		Set(expression.Name("result"), expression.Value(result))

	expr, err := expression.NewBuilder().WithUpdate(upd).Build()
	if err != nil {
		return fmt.Errorf("build expression: %w", err)
	}
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &s.matchesTable,
		Key:                       matchKey(matchID),
		UpdateExpression:          expr.Update(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	return err
}
