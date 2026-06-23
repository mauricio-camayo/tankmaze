package db

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// DeleteVersion removes a single version record.
func (s *Store) DeleteVersion(ctx context.Context, tankID, version string) error {
	_, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &s.versionsTable,
		Key:       versionKey(tankID, version),
	})
	return err
}

// PutVersion writes a version record, overwriting any existing record for the
// same (tankId, version) pair.
func (s *Store) PutVersion(ctx context.Context, v TankVersion) error {
	item, err := attributevalue.MarshalMap(v)
	if err != nil {
		return fmt.Errorf("marshal version: %w", err)
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.versionsTable,
		Item:      item,
	})
	return err
}

// GetVersion returns the version record for (tankId, version). Returns
// ErrNotFound if the record does not exist.
func (s *Store) GetVersion(ctx context.Context, tankID, version string) (TankVersion, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.versionsTable,
		Key:       versionKey(tankID, version),
	})
	if err != nil {
		return TankVersion{}, fmt.Errorf("get version %s/%s: %w", tankID, version, err)
	}
	if len(out.Item) == 0 {
		return TankVersion{}, ErrNotFound
	}
	var v TankVersion
	if err := attributevalue.UnmarshalMap(out.Item, &v); err != nil {
		return TankVersion{}, fmt.Errorf("unmarshal version %s/%s: %w", tankID, version, err)
	}
	return v, nil
}

// UpdateVersionCompile writes the fields produced by the CodeBuild tank-compiler
// after a compilation attempt. Status must be "compiling", "ready", or "failed".
func (s *Store) UpdateVersionCompile(ctx context.Context, tankID, version string, u CompileUpdate) error {
	upd := expression.Set(expression.Name("compileStatus"), expression.Value(u.Status))
	if u.WasmS3Key != "" {
		upd = upd.Set(expression.Name("wasmS3Key"), expression.Value(u.WasmS3Key)).
			Set(expression.Name("wasmSha256"), expression.Value(u.WasmSHA256))
	}
	if u.CompileError != "" {
		upd = upd.Set(expression.Name("compileError"), expression.Value(u.CompileError))
	}
	if u.BuildID != "" {
		upd = upd.Set(expression.Name("buildId"), expression.Value(u.BuildID))
	}
	if u.CompileStartedAt != 0 {
		upd = upd.Set(expression.Name("compileStartedAt"), expression.Value(u.CompileStartedAt))
	}

	expr, err := expression.NewBuilder().WithUpdate(upd).Build()
	if err != nil {
		return fmt.Errorf("build expression: %w", err)
	}
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &s.versionsTable,
		Key:                       versionKey(tankID, version),
		UpdateExpression:          expr.Update(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	return err
}

// AddVersionRegistration appends gameDayID to the registeredForGameDays list.
// No-ops if already present.
func (s *Store) AddVersionRegistration(ctx context.Context, tankID, version, gameDayID string) error {
	ver, err := s.GetVersion(ctx, tankID, version)
	if err != nil {
		return err
	}
	for _, id := range ver.RegisteredForGameDays {
		if id == gameDayID {
			return nil
		}
	}
	updated := append(ver.RegisteredForGameDays, gameDayID)
	upd := expression.Set(expression.Name("registeredForGameDays"), expression.Value(updated))
	expr, err := expression.NewBuilder().WithUpdate(upd).Build()
	if err != nil {
		return fmt.Errorf("build expression: %w", err)
	}
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &s.versionsTable,
		Key:                       versionKey(tankID, version),
		UpdateExpression:          expr.Update(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	return err
}

// RemoveVersionRegistration removes gameDayID from the registeredForGameDays list.
func (s *Store) RemoveVersionRegistration(ctx context.Context, tankID, version, gameDayID string) error {
	ver, err := s.GetVersion(ctx, tankID, version)
	if err != nil {
		return err
	}
	filtered := make([]string, 0, len(ver.RegisteredForGameDays))
	for _, id := range ver.RegisteredForGameDays {
		if id != gameDayID {
			filtered = append(filtered, id)
		}
	}
	var upd expression.UpdateBuilder
	if len(filtered) == 0 {
		upd = expression.Remove(expression.Name("registeredForGameDays"))
	} else {
		upd = expression.Set(expression.Name("registeredForGameDays"), expression.Value(filtered))
	}
	expr, err := expression.NewBuilder().WithUpdate(upd).Build()
	if err != nil {
		return fmt.Errorf("build expression: %w", err)
	}
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &s.versionsTable,
		Key:                       versionKey(tankID, version),
		UpdateExpression:          expr.Update(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	return err
}

// IncrementTestMatchCount atomically increments testMatchCount by 1 on a minor version.
func (s *Store) IncrementTestMatchCount(ctx context.Context, tankID, version string) error {
	upd := expression.Add(expression.Name("testMatchCount"), expression.Value(1))
	expr, err := expression.NewBuilder().WithUpdate(upd).Build()
	if err != nil {
		return fmt.Errorf("build expression: %w", err)
	}
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &s.versionsTable,
		Key:                       versionKey(tankID, version),
		UpdateExpression:          expr.Update(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	return err
}

// SetVersionDisqualified marks a version as disqualified due to excessive tick violations.
func (s *Store) SetVersionDisqualified(ctx context.Context, tankID, version string) error {
	upd := expression.Set(expression.Name("disqualified"), expression.Value(true))
	expr, err := expression.NewBuilder().WithUpdate(upd).Build()
	if err != nil {
		return fmt.Errorf("build expression: %w", err)
	}
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &s.versionsTable,
		Key:                       versionKey(tankID, version),
		UpdateExpression:          expr.Update(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	return err
}

// ScanVersionsByGameDay returns all version records whose registeredForGameDays
// list contains gameDayID. This requires a full table scan.
func (s *Store) ScanVersionsByGameDay(ctx context.Context, gameDayID string) ([]TankVersion, error) {
	filt := expression.Name("registeredForGameDays").Contains(expression.Value(gameDayID))
	expr, err := expression.NewBuilder().WithFilter(filt).Build()
	if err != nil {
		return nil, fmt.Errorf("build expression: %w", err)
	}
	input := &dynamodb.ScanInput{
		TableName:                 &s.versionsTable,
		FilterExpression:          expr.Filter(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	}
	versions := []TankVersion{}
	for {
		out, err := s.db.Scan(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("scan versions by gameday %s: %w", gameDayID, err)
		}
		var batch []TankVersion
		if err := attributevalue.UnmarshalListOfMaps(out.Items, &batch); err != nil {
			return nil, fmt.Errorf("unmarshal versions: %w", err)
		}
		versions = append(versions, batch...)
		if out.LastEvaluatedKey == nil {
			break
		}
		input.ExclusiveStartKey = out.LastEvaluatedKey
	}
	return versions, nil
}

// ListVersionsByTank returns all version records for the given tankId, sorted by
// the DynamoDB sort key (version string).
func (s *Store) ListVersionsByTank(ctx context.Context, tankID string) ([]TankVersion, error) {
	keyExpr := expression.Key("tankId").Equal(expression.Value(tankID))
	expr, err := expression.NewBuilder().WithKeyCondition(keyExpr).Build()
	if err != nil {
		return nil, fmt.Errorf("build expression: %w", err)
	}

	input := &dynamodb.QueryInput{
		TableName:                 &s.versionsTable,
		KeyConditionExpression:    expr.KeyCondition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	}

	versions := []TankVersion{}
	for {
		out, err := s.db.Query(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("query versions for tank %s: %w", tankID, err)
		}
		var batch []TankVersion
		if err := attributevalue.UnmarshalListOfMaps(out.Items, &batch); err != nil {
			return nil, fmt.Errorf("unmarshal versions: %w", err)
		}
		versions = append(versions, batch...)
		if out.LastEvaluatedKey == nil {
			break
		}
		input.ExclusiveStartKey = out.LastEvaluatedKey
	}
	return versions, nil
}

// UpdateVersionStats overwrites the post-match performance stats on a major version.
func (s *Store) UpdateVersionStats(ctx context.Context, tankID, version string, st VersionStats) error {
	upd := expression.Set(expression.Name("winRate"), expression.Value(st.WinRate)).
		Set(expression.Name("matchesPlayed"), expression.Value(st.MatchesPlayed)).
		Set(expression.Name("avgDamageDealt"), expression.Value(st.AvgDamageDealt)).
		Set(expression.Name("avgSurvivalTicks"), expression.Value(st.AvgSurvivalTicks))

	expr, err := expression.NewBuilder().WithUpdate(upd).Build()
	if err != nil {
		return fmt.Errorf("build expression: %w", err)
	}
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &s.versionsTable,
		Key:                       versionKey(tankID, version),
		UpdateExpression:          expr.Update(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	return err
}
