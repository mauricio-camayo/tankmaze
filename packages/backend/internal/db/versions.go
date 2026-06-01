package db

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

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

// UpdateVersionRegistration sets or clears the registeredForGameDay attribute.
// Pass an empty gameDayID to deregister.
func (s *Store) UpdateVersionRegistration(ctx context.Context, tankID, version, gameDayID string) error {
	var upd expression.UpdateBuilder
	if gameDayID != "" {
		upd = expression.Set(expression.Name("registeredForGameDay"), expression.Value(gameDayID))
	} else {
		upd = expression.Remove(expression.Name("registeredForGameDay"))
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
