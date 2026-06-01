package db

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// PutMap writes a map record, overwriting any existing record for the same mapId.
func (s *Store) PutMap(ctx context.Context, m Map) error {
	item, err := attributevalue.MarshalMap(m)
	if err != nil {
		return fmt.Errorf("marshal map: %w", err)
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.mapsTable,
		Item:      item,
	})
	return err
}

// GetMapByID returns the map with the given mapId. Returns ErrNotFound if absent.
func (s *Store) GetMapByID(ctx context.Context, mapID string) (Map, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.mapsTable,
		Key:       mapKey(mapID),
	})
	if err != nil {
		return Map{}, fmt.Errorf("get map %s: %w", mapID, err)
	}
	if len(out.Item) == 0 {
		return Map{}, ErrNotFound
	}
	var m Map
	if err := attributevalue.UnmarshalMap(out.Item, &m); err != nil {
		return Map{}, fmt.Errorf("unmarshal map %s: %w", mapID, err)
	}
	return m, nil
}

// GetMapBySlug returns the map with the given slug using the slug-index GSI.
// Returns ErrNotFound if no map exists with that slug.
func (s *Store) GetMapBySlug(ctx context.Context, slug string) (Map, error) {
	keyExpr := expression.Key("slug").Equal(expression.Value(slug))
	expr, err := expression.NewBuilder().WithKeyCondition(keyExpr).Build()
	if err != nil {
		return Map{}, fmt.Errorf("build expression: %w", err)
	}

	out, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:                 &s.mapsTable,
		IndexName:                 aws.String("slug-index"),
		KeyConditionExpression:    expr.KeyCondition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		Limit:                     aws.Int32(1),
	})
	if err != nil {
		return Map{}, fmt.Errorf("query map by slug %s: %w", slug, err)
	}
	if len(out.Items) == 0 {
		return Map{}, ErrNotFound
	}
	var m Map
	if err := attributevalue.UnmarshalMap(out.Items[0], &m); err != nil {
		return Map{}, fmt.Errorf("unmarshal map: %w", err)
	}
	return m, nil
}

// ListActiveMaps returns all maps where isActive = true. Used by the map picker.
func (s *Store) ListActiveMaps(ctx context.Context) ([]Map, error) {
	filt := expression.Name("isActive").Equal(expression.Value(true))
	expr, err := expression.NewBuilder().WithFilter(filt).Build()
	if err != nil {
		return nil, fmt.Errorf("build expression: %w", err)
	}

	input := &dynamodb.ScanInput{
		TableName:                 &s.mapsTable,
		FilterExpression:          expr.Filter(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	}

	var maps []Map
	for {
		out, err := s.db.Scan(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("scan active maps: %w", err)
		}
		var batch []Map
		if err := attributevalue.UnmarshalListOfMaps(out.Items, &batch); err != nil {
			return nil, fmt.Errorf("unmarshal maps: %w", err)
		}
		maps = append(maps, batch...)
		if out.LastEvaluatedKey == nil {
			break
		}
		input.ExclusiveStartKey = out.LastEvaluatedKey
	}
	return maps, nil
}

// UpdateMap updates the mutable fields of a map record: name, description, and
// isActive. The slug and layout are immutable after creation.
func (s *Store) UpdateMap(ctx context.Context, mapID, name, description string, isActive bool) error {
	upd := expression.Set(expression.Name("name"), expression.Value(name)).
		Set(expression.Name("description"), expression.Value(description)).
		Set(expression.Name("isActive"), expression.Value(isActive))

	expr, err := expression.NewBuilder().WithUpdate(upd).Build()
	if err != nil {
		return fmt.Errorf("build expression: %w", err)
	}
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &s.mapsTable,
		Key:                       mapKey(mapID),
		UpdateExpression:          expr.Update(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	return err
}
