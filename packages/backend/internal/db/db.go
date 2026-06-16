// Package db provides typed DynamoDB access for all seven TankMaze tables.
// Table names are read from environment variables at construction time
// (TANKS_TABLE, TANK_VERSIONS_TABLE, MATCHES_TABLE, CONNECTIONS_TABLE,
// GAMEDAYS_TABLE, RANKINGS_TABLE, MAPS_TABLE).
//
// ErrNotFound is returned by Get* methods when the requested item does not
// exist. All other errors are DynamoDB SDK errors wrapped with context.
package db

import (
	"errors"
	"os"

	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// ErrNotFound is returned when a requested item does not exist in the table.
var ErrNotFound = errors.New("item not found")

// ErrConflict is returned by PutGameDay when the optimistic-locking condition
// fails — another writer updated the record since it was last read.
var ErrConflict = errors.New("optimistic lock conflict")

// Store provides typed access to all TankMaze DynamoDB tables.
type Store struct {
	db               *dynamodb.Client
	tanksTable       string
	versionsTable    string
	matchesTable     string
	connectionsTable string
	gamedaysTable    string
	rankingsTable    string
	mapsTable        string
}

// New creates a Store from a pre-configured DynamoDB client. Table names are
// read from the process environment once at construction time.
func New(client *dynamodb.Client) *Store {
	return &Store{
		db:               client,
		tanksTable:       os.Getenv("TANKS_TABLE"),
		versionsTable:    os.Getenv("TANK_VERSIONS_TABLE"),
		matchesTable:     os.Getenv("MATCHES_TABLE"),
		connectionsTable: os.Getenv("CONNECTIONS_TABLE"),
		gamedaysTable:    os.Getenv("GAMEDAYS_TABLE"),
		rankingsTable:    os.Getenv("RANKINGS_TABLE"),
		mapsTable:        os.Getenv("MAPS_TABLE"),
	}
}

// strAttr returns a DynamoDB String attribute value.
func strAttr(v string) dbtypes.AttributeValue {
	return &dbtypes.AttributeValueMemberS{Value: v}
}

func tankKey(tankID string) map[string]dbtypes.AttributeValue {
	return map[string]dbtypes.AttributeValue{"tankId": strAttr(tankID)}
}

func versionKey(tankID, version string) map[string]dbtypes.AttributeValue {
	return map[string]dbtypes.AttributeValue{
		"tankId":  strAttr(tankID),
		"version": strAttr(version),
	}
}

func matchKey(matchID string) map[string]dbtypes.AttributeValue {
	return map[string]dbtypes.AttributeValue{"matchId": strAttr(matchID)}
}

func connectionKey(connID string) map[string]dbtypes.AttributeValue {
	return map[string]dbtypes.AttributeValue{"connectionId": strAttr(connID)}
}

func gamedayKey(gameDayID string) map[string]dbtypes.AttributeValue {
	return map[string]dbtypes.AttributeValue{"gameDayId": strAttr(gameDayID)}
}

func rankingKey(tankID, gameDayID string) map[string]dbtypes.AttributeValue {
	return map[string]dbtypes.AttributeValue{
		"tankId":    strAttr(tankID),
		"gameDayId": strAttr(gameDayID),
	}
}

func mapKey(mapID string) map[string]dbtypes.AttributeValue {
	return map[string]dbtypes.AttributeValue{"mapId": strAttr(mapID)}
}
