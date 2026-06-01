package db

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// PutConnection writes an observer connection record with a 2-hour TTL.
func (s *Store) PutConnection(ctx context.Context, c Connection) error {
	item, err := attributevalue.MarshalMap(c)
	if err != nil {
		return fmt.Errorf("marshal connection: %w", err)
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.connectionsTable,
		Item:      item,
	})
	return err
}

// GetConnection returns the connection with the given connectionId. Returns
// ErrNotFound if the record does not exist (e.g. already expired via TTL).
func (s *Store) GetConnection(ctx context.Context, connectionID string) (Connection, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.connectionsTable,
		Key:       connectionKey(connectionID),
	})
	if err != nil {
		return Connection{}, fmt.Errorf("get connection %s: %w", connectionID, err)
	}
	if len(out.Item) == 0 {
		return Connection{}, ErrNotFound
	}
	var c Connection
	if err := attributevalue.UnmarshalMap(out.Item, &c); err != nil {
		return Connection{}, fmt.Errorf("unmarshal connection: %w", err)
	}
	return c, nil
}

// UpdateConnectionReplay persists the observer's current replay position and
// speed into the connection record. Called by REPLAY_SEEK and REPLAY_SPEED;
// read by the next OBSERVE invocation to resume streaming.
func (s *Store) UpdateConnectionReplay(ctx context.Context, connectionID string, tick int, speed string) error {
	upd := expression.Set(expression.Name("replayTick"), expression.Value(tick)).
		Set(expression.Name("replaySpeed"), expression.Value(speed))
	expr, err := expression.NewBuilder().WithUpdate(upd).Build()
	if err != nil {
		return fmt.Errorf("build expression: %w", err)
	}
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &s.connectionsTable,
		Key:                       connectionKey(connectionID),
		UpdateExpression:          expr.Update(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	return err
}

// DeleteConnection removes an observer connection record by connectionId.
func (s *Store) DeleteConnection(ctx context.Context, connectionID string) error {
	_, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &s.connectionsTable,
		Key:       connectionKey(connectionID),
	})
	return err
}

// ListConnectionsByMatch returns all observer connections for a match using the
// matchId-index GSI. Used by match-runner to broadcast TICK_UPDATE events.
func (s *Store) ListConnectionsByMatch(ctx context.Context, matchID string) ([]Connection, error) {
	keyExpr := expression.Key("matchId").Equal(expression.Value(matchID))
	expr, err := expression.NewBuilder().WithKeyCondition(keyExpr).Build()
	if err != nil {
		return nil, fmt.Errorf("build expression: %w", err)
	}

	input := &dynamodb.QueryInput{
		TableName:                 &s.connectionsTable,
		IndexName:                 aws.String("matchId-index"),
		KeyConditionExpression:    expr.KeyCondition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	}

	var conns []Connection
	for {
		out, err := s.db.Query(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("query connections for match %s: %w", matchID, err)
		}
		var batch []Connection
		if err := attributevalue.UnmarshalListOfMaps(out.Items, &batch); err != nil {
			return nil, fmt.Errorf("unmarshal connections: %w", err)
		}
		conns = append(conns, batch...)
		if out.LastEvaluatedKey == nil {
			break
		}
		input.ExclusiveStartKey = out.LastEvaluatedKey
	}
	return conns, nil
}
