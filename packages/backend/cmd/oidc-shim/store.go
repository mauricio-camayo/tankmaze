package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// codeRecord is the DynamoDB-backed ephemeral mapping from the shim's own
// single-use authorization code (minted in handleCallback) to the profile
// handleToken later redeems it for. expiresAt doubles as the table's TTL
// attribute — see auth-stack.ts's timeToLiveAttribute config.
type codeRecord struct {
	Code        string `dynamodbav:"code"`
	ProfileJSON string `dynamodbav:"profileJson"`
	ExpiresAt   int64  `dynamodbav:"expiresAt"`
}

const codeTTL = 2 * time.Minute

func (h *handler) storeCode(ctx context.Context, code string, profile shimProfile) error {
	profileJSON, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	rec := codeRecord{
		Code:        code,
		ProfileJSON: string(profileJSON),
		ExpiresAt:   time.Now().Add(codeTTL).Unix(),
	}
	item, err := attributevalue.MarshalMap(rec)
	if err != nil {
		return err
	}
	_, err = h.ddb.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(h.codeTable),
		Item:      item,
	})
	return err
}

// consumeCode is single-use by design: it deletes the record immediately
// after a successful read, so a leaked/replayed code can't be redeemed
// twice even before the DynamoDB TTL sweep runs.
func (h *handler) consumeCode(ctx context.Context, code string) (shimProfile, error) {
	if code == "" {
		return shimProfile{}, fmt.Errorf("empty code")
	}
	key := map[string]dbtypes.AttributeValue{"code": &dbtypes.AttributeValueMemberS{Value: code}}
	out, err := h.ddb.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(h.codeTable), Key: key})
	if err != nil {
		return shimProfile{}, err
	}
	if len(out.Item) == 0 {
		return shimProfile{}, fmt.Errorf("code not found")
	}
	var rec codeRecord
	if err := attributevalue.UnmarshalMap(out.Item, &rec); err != nil {
		return shimProfile{}, err
	}
	_, _ = h.ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(h.codeTable), Key: key})

	if rec.ExpiresAt < time.Now().Unix() {
		return shimProfile{}, fmt.Errorf("code expired")
	}
	var profile shimProfile
	if err := json.Unmarshal([]byte(rec.ProfileJSON), &profile); err != nil {
		return shimProfile{}, err
	}
	return profile, nil
}

// loadSigningKey returns the shim's RS256 signing key, generating and
// persisting a new one on first use if the secret doesn't exist yet — this
// avoids a manual "run openssl and paste the key in" deploy step. Benign
// race if two cold Lambda instances both hit "not found" simultaneously:
// the second PutSecretValue just overwrites the first, and the losing
// instance's in-memory key goes stale until its next cold start re-reads
// the secret. Acceptable for a not-yet-enabled scaffold (items 233/240);
// worth a conditional-put guard if this ever needs stronger guarantees.
func (h *handler) loadSigningKey(ctx context.Context) (*rsa.PrivateKey, string, error) {
	if h.signingKey != nil {
		return h.signingKey, h.kid, nil
	}

	out, err := h.sm.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(h.signingKeySecretArn),
	})
	if err == nil && out.SecretString != nil {
		if key, kid, perr := parseSigningKey(*out.SecretString); perr == nil {
			h.signingKey, h.kid = key, kid
			return h.signingKey, h.kid, nil
		}
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, "", err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if _, err := h.sm.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     aws.String(h.signingKeySecretArn),
		SecretString: aws.String(string(pemBytes)),
	}); err != nil {
		return nil, "", err
	}
	h.signingKey = key
	h.kid = keyID(&key.PublicKey)
	return h.signingKey, h.kid, nil
}

func parseSigningKey(pemStr string) (*rsa.PrivateKey, string, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, "", fmt.Errorf("no PEM block found")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, "", err
	}
	return key, keyID(&key.PublicKey), nil
}
