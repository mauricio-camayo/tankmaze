package db

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const adConfigKey = "ad_config"

// AdConfig holds the Google AdSense configuration stored in the platform-config table.
type AdConfig struct {
	ConfigKey    string `dynamodbav:"configKey"    json:"configKey,omitempty"`
	Enabled      bool   `dynamodbav:"enabled"      json:"enabled"`
	PublisherID  string `dynamodbav:"publisherId"  json:"publisherId"`
	TopSlotID    string `dynamodbav:"topSlotId"    json:"topSlotId"`
	RightSlotID  string `dynamodbav:"rightSlotId"  json:"rightSlotId"`
	BottomSlotID string `dynamodbav:"bottomSlotId" json:"bottomSlotId"`
}

// GetAdConfig retrieves the ad configuration item. Returns a zero-value AdConfig
// (Enabled=false) when the item does not yet exist.
func (s *Store) GetAdConfig(ctx context.Context) (AdConfig, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.configTable),
		Key:       map[string]dbtypes.AttributeValue{"configKey": strAttr(adConfigKey)},
	})
	if err != nil {
		return AdConfig{}, fmt.Errorf("GetAdConfig: %w", err)
	}
	if out.Item == nil {
		return AdConfig{ConfigKey: adConfigKey}, nil
	}
	var cfg AdConfig
	if err := attributevalue.UnmarshalMap(out.Item, &cfg); err != nil {
		return AdConfig{}, fmt.Errorf("GetAdConfig unmarshal: %w", err)
	}
	return cfg, nil
}

// PutAdConfig writes the ad configuration item.
func (s *Store) PutAdConfig(ctx context.Context, cfg AdConfig) error {
	cfg.ConfigKey = adConfigKey
	item, err := attributevalue.MarshalMap(cfg)
	if err != nil {
		return fmt.Errorf("PutAdConfig marshal: %w", err)
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.configTable),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("PutAdConfig: %w", err)
	}
	return nil
}
