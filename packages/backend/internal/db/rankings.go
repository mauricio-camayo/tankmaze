package db

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// PutRanking writes a ranking record, overwriting any existing record for the
// same (tankId, gameDayId) pair.
func (s *Store) PutRanking(ctx context.Context, r Ranking) error {
	item, err := attributevalue.MarshalMap(r)
	if err != nil {
		return fmt.Errorf("marshal ranking: %w", err)
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.rankingsTable,
		Item:      item,
	})
	return err
}

// GetRanking returns the ranking for (tankId, gameDayId). Returns ErrNotFound if absent.
func (s *Store) GetRanking(ctx context.Context, tankID, gameDayID string) (Ranking, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.rankingsTable,
		Key:       rankingKey(tankID, gameDayID),
	})
	if err != nil {
		return Ranking{}, fmt.Errorf("get ranking %s/%s: %w", tankID, gameDayID, err)
	}
	if len(out.Item) == 0 {
		return Ranking{}, ErrNotFound
	}
	var r Ranking
	if err := attributevalue.UnmarshalMap(out.Item, &r); err != nil {
		return Ranking{}, fmt.Errorf("unmarshal ranking: %w", err)
	}
	return r, nil
}

// ListRankingsByTank returns all ranking records for a tank (its full Game Day
// history) by querying the primary key.
func (s *Store) ListRankingsByTank(ctx context.Context, tankID string) ([]Ranking, error) {
	keyExpr := expression.Key("tankId").Equal(expression.Value(tankID))
	expr, err := expression.NewBuilder().WithKeyCondition(keyExpr).Build()
	if err != nil {
		return nil, fmt.Errorf("build expression: %w", err)
	}

	input := &dynamodb.QueryInput{
		TableName:                 &s.rankingsTable,
		KeyConditionExpression:    expr.KeyCondition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	}

	rankings := []Ranking{}
	for {
		out, err := s.db.Query(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("query rankings for tank %s: %w", tankID, err)
		}
		var batch []Ranking
		if err := attributevalue.UnmarshalListOfMaps(out.Items, &batch); err != nil {
			return nil, fmt.Errorf("unmarshal rankings: %w", err)
		}
		rankings = append(rankings, batch...)
		if out.LastEvaluatedKey == nil {
			break
		}
		input.ExclusiveStartKey = out.LastEvaluatedKey
	}
	return rankings, nil
}

// ScanRankingsByGameDay returns all rankings for a specific Game Day using a
// table scan with filter. There is no GSI on gameDayId in the rankings table.
func (s *Store) ScanRankingsByGameDay(ctx context.Context, gameDayID string) ([]Ranking, error) {
	filt := expression.Name("gameDayId").Equal(expression.Value(gameDayID))
	expr, err := expression.NewBuilder().WithFilter(filt).Build()
	if err != nil {
		return nil, fmt.Errorf("build expression: %w", err)
	}

	input := &dynamodb.ScanInput{
		TableName:                 &s.rankingsTable,
		FilterExpression:          expr.Filter(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	}

	rankings := []Ranking{}
	for {
		out, err := s.db.Scan(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("scan rankings for gameday %s: %w", gameDayID, err)
		}
		var batch []Ranking
		if err := attributevalue.UnmarshalListOfMaps(out.Items, &batch); err != nil {
			return nil, fmt.Errorf("unmarshal rankings: %w", err)
		}
		rankings = append(rankings, batch...)
		if out.LastEvaluatedKey == nil {
			break
		}
		input.ExclusiveStartKey = out.LastEvaluatedKey
	}
	return rankings, nil
}

// ScoreTransfer atomically copies all source rankings to the target tank, resets
// the source tank stats, and updates the target tank stats in a single DynamoDB
// transaction. After the transaction succeeds, source rankings are batch-deleted.
//
// DynamoDB transactions are limited to 100 items. ScoreTransfer returns an error
// when len(in.SourceRankings)+2 > 100 (the +2 accounts for the two tank updates).
// In practice this limit is not reached within the 365-day ranking validity window.
func (s *Store) ScoreTransfer(ctx context.Context, in ScoreTransferInput) error {
	const maxTransactItems = 100
	if len(in.SourceRankings)+2 > maxTransactItems {
		return fmt.Errorf("score transfer: %d rankings exceed transaction limit of %d",
			len(in.SourceRankings), maxTransactItems-2)
	}

	items := make([]dbtypes.TransactWriteItem, 0, len(in.SourceRankings)+2)

	// Put a copy of each ranking under the target tankId.
	for _, r := range in.SourceRankings {
		r.TankID = in.TargetTankID
		av, err := attributevalue.MarshalMap(r)
		if err != nil {
			return fmt.Errorf("marshal ranking for transfer: %w", err)
		}
		items = append(items, dbtypes.TransactWriteItem{
			Put: &dbtypes.Put{
				TableName: &s.rankingsTable,
				Item:      av,
			},
		})
	}

	// Reset source tank: zero out scores, record where they went.
	srcUpd := expression.Set(expression.Name("globalScore"), expression.Value(0)).
		Set(expression.Name("gameDaysCount"), expression.Value(0)).
		Remove(expression.Name("bestFinish")).
		Set(expression.Name("scoreTransferredTo"), expression.Value(in.TargetTankID))
	srcExpr, err := expression.NewBuilder().WithUpdate(srcUpd).Build()
	if err != nil {
		return fmt.Errorf("build source expression: %w", err)
	}
	items = append(items, dbtypes.TransactWriteItem{
		Update: &dbtypes.Update{
			TableName:                 &s.tanksTable,
			Key:                       tankKey(in.SourceTankID),
			UpdateExpression:          srcExpr.Update(),
			ExpressionAttributeNames:  srcExpr.Names(),
			ExpressionAttributeValues: srcExpr.Values(),
		},
	})

	// Update target tank with the transferred stats.
	tgtUpd := expression.Set(expression.Name("globalScore"), expression.Value(in.GlobalScore)).
		Set(expression.Name("gameDaysCount"), expression.Value(in.GameDaysCount)).
		Set(expression.Name("lastActiveAt"), expression.Value(in.LastActiveAt)).
		Set(expression.Name("scoreTransferredFrom"), expression.Value(in.SourceTankID))
	if in.BestFinish != nil {
		tgtUpd = tgtUpd.Set(expression.Name("bestFinish"), expression.Value(*in.BestFinish))
	}
	tgtExpr, err := expression.NewBuilder().WithUpdate(tgtUpd).Build()
	if err != nil {
		return fmt.Errorf("build target expression: %w", err)
	}
	items = append(items, dbtypes.TransactWriteItem{
		Update: &dbtypes.Update{
			TableName:                 &s.tanksTable,
			Key:                       tankKey(in.TargetTankID),
			UpdateExpression:          tgtExpr.Update(),
			ExpressionAttributeNames:  tgtExpr.Names(),
			ExpressionAttributeValues: tgtExpr.Values(),
		},
	})

	if _, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: items,
	}); err != nil {
		return fmt.Errorf("score transfer transaction: %w", err)
	}

	// Delete source rankings after successful transaction (separate operation;
	// idempotent on retry since the records are already gone).
	return s.batchDeleteRankings(ctx, in.SourceRankings)
}

// batchDeleteRankings removes a slice of ranking records in batches of 25
// (the BatchWriteItem maximum).
func (s *Store) batchDeleteRankings(ctx context.Context, rankings []Ranking) error {
	for i := 0; i < len(rankings); i += 25 {
		end := i + 25
		if end > len(rankings) {
			end = len(rankings)
		}
		reqs := make([]dbtypes.WriteRequest, end-i)
		for j, r := range rankings[i:end] {
			reqs[j] = dbtypes.WriteRequest{
				DeleteRequest: &dbtypes.DeleteRequest{
					Key: rankingKey(r.TankID, r.GameDayID),
				},
			}
		}
		if _, err := s.db.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
			RequestItems: map[string][]dbtypes.WriteRequest{s.rankingsTable: reqs},
		}); err != nil {
			return fmt.Errorf("batch delete rankings (offset %d): %w", i, err)
		}
	}
	return nil
}
