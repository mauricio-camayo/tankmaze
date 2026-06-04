package db

import (
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// DeleteTank removes a tank record by tankId.
func (s *Store) DeleteTank(ctx context.Context, tankID string) error {
	_, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &s.tanksTable,
		Key:       tankKey(tankID),
	})
	return err
}

// PutTank writes a tank record, overwriting any existing record for the same tankId.
func (s *Store) PutTank(ctx context.Context, t Tank) error {
	item, err := attributevalue.MarshalMap(t)
	if err != nil {
		return fmt.Errorf("marshal tank: %w", err)
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.tanksTable,
		Item:      item,
	})
	return err
}

// GetTank returns the tank with the given tankId. Returns ErrNotFound if absent.
func (s *Store) GetTank(ctx context.Context, tankID string) (Tank, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.tanksTable,
		Key:       tankKey(tankID),
	})
	if err != nil {
		return Tank{}, fmt.Errorf("get tank %s: %w", tankID, err)
	}
	if len(out.Item) == 0 {
		return Tank{}, ErrNotFound
	}
	var t Tank
	if err := attributevalue.UnmarshalMap(out.Item, &t); err != nil {
		return Tank{}, fmt.Errorf("unmarshal tank %s: %w", tankID, err)
	}
	return t, nil
}

// ListTanksByUser returns all tanks owned by userId using the userId-index GSI.
func (s *Store) ListTanksByUser(ctx context.Context, userID string) ([]Tank, error) {
	keyExpr := expression.Key("userId").Equal(expression.Value(userID))
	expr, err := expression.NewBuilder().WithKeyCondition(keyExpr).Build()
	if err != nil {
		return nil, fmt.Errorf("build expression: %w", err)
	}

	input := &dynamodb.QueryInput{
		TableName:                 &s.tanksTable,
		IndexName:                 aws.String("userId-index"),
		KeyConditionExpression:    expr.KeyCondition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	}

	tanks := []Tank{}
	for {
		out, err := s.db.Query(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("query tanks for user %s: %w", userID, err)
		}
		var batch []Tank
		if err := attributevalue.UnmarshalListOfMaps(out.Items, &batch); err != nil {
			return nil, fmt.Errorf("unmarshal tanks: %w", err)
		}
		tanks = append(tanks, batch...)
		if out.LastEvaluatedKey == nil {
			break
		}
		input.ExclusiveStartKey = out.LastEvaluatedKey
	}
	return tanks, nil
}

// UpdateTankStats updates the aggregate stats fields on a tank record. A nil
// BestFinish removes the attribute (tank has never placed).
func (s *Store) UpdateTankStats(ctx context.Context, tankID string, stats TankStats) error {
	upd := expression.Set(expression.Name("globalScore"), expression.Value(stats.GlobalScore)).
		Set(expression.Name("gameDaysCount"), expression.Value(stats.GameDaysCount)).
		Set(expression.Name("lastActiveAt"), expression.Value(stats.LastActiveAt))
	if stats.BestFinish != nil {
		upd = upd.Set(expression.Name("bestFinish"), expression.Value(*stats.BestFinish))
	} else {
		upd = upd.Remove(expression.Name("bestFinish"))
	}

	expr, err := expression.NewBuilder().WithUpdate(upd).Build()
	if err != nil {
		return fmt.Errorf("build expression: %w", err)
	}
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &s.tanksTable,
		Key:                       tankKey(tankID),
		UpdateExpression:          expr.Update(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	return err
}

// UpdateTankName updates the name field on a tank record.
func (s *Store) UpdateTankName(ctx context.Context, tankID, name string) error {
	upd := expression.Set(expression.Name("name"), expression.Value(name))
	expr, err := expression.NewBuilder().WithUpdate(upd).Build()
	if err != nil {
		return fmt.Errorf("build expression: %w", err)
	}
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &s.tanksTable,
		Key:                       tankKey(tankID),
		UpdateExpression:          expr.Update(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	return err
}

// ScanTanksByScore returns all tanks sorted by GlobalScore descending. Used to
// build the global leaderboard. Results are sorted in application memory.
func (s *Store) ScanTanksByScore(ctx context.Context) ([]Tank, error) {
	input := &dynamodb.ScanInput{TableName: &s.tanksTable}
	tanks := []Tank{}
	for {
		out, err := s.db.Scan(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("scan tanks: %w", err)
		}
		var batch []Tank
		if err := attributevalue.UnmarshalListOfMaps(out.Items, &batch); err != nil {
			return nil, fmt.Errorf("unmarshal tanks: %w", err)
		}
		tanks = append(tanks, batch...)
		if out.LastEvaluatedKey == nil {
			break
		}
		input.ExclusiveStartKey = out.LastEvaluatedKey
	}
	sort.Slice(tanks, func(i, j int) bool {
		return tanks[i].GlobalScore > tanks[j].GlobalScore
	})
	return tanks, nil
}
