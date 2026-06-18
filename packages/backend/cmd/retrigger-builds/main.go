// retrigger-builds re-queues CodeBuild jobs for every non-AI major tank version
// whose WASM is missing from S3 (compileStatus=ready but S3 returns 404).
//
// Usage (needs admin creds with codebuild:StartBuild, s3:HeadObject on the WASM
// bucket, and dynamodb:Scan + dynamodb:UpdateItem on the versions table):
//
//	WASM_BUCKET=tankmaze-wasm-artifacts-<account> \
//	TANK_VERSIONS_TABLE=tankmaze-tank-versions    \
//	CODEBUILD_PROJECT=tank-compiler               \
//	AWS_REGION=us-east-1                          \
//	  GOTOOLCHAIN=local go run ./cmd/retrigger-builds/
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
	wasmBucket := mustEnv("WASM_BUCKET")
	versionsTable := mustEnv("TANK_VERSIONS_TABLE")
	cbProject := mustEnv("CODEBUILD_PROJECT")
	region := envOrDefault("AWS_REGION", "us-east-1")

	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}

	ddb := dynamodb.NewFromConfig(cfg)
	s3c := s3.NewFromConfig(cfg)
	cb := codebuild.NewFromConfig(cfg)

	// Scan all major versions that show compileStatus=ready.
	var broken []map[string]dbtypes.AttributeValue
	var lastKey map[string]dbtypes.AttributeValue
	for {
		out, err := ddb.Scan(ctx, &dynamodb.ScanInput{
			TableName:        aws.String(versionsTable),
			FilterExpression: aws.String("versionType = :major AND compileStatus = :ready"),
			ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
				":major": &dbtypes.AttributeValueMemberS{Value: "major"},
				":ready": &dbtypes.AttributeValueMemberS{Value: "ready"},
			},
			ExclusiveStartKey: lastKey,
		})
		if err != nil {
			log.Fatalf("scan versions: %v", err)
		}
		broken = append(broken, out.Items...)
		lastKey = out.LastEvaluatedKey
		if lastKey == nil {
			break
		}
	}

	log.Printf("found %d major versions with compileStatus=ready", len(broken))

	triggered := 0
	skipped := 0
	for _, item := range broken {
		tankID := strAttr(item, "tankId")
		version := strAttr(item, "version")
		wasmKey := strAttr(item, "wasmS3Key")
		sourceKey := strAttr(item, "sourceS3Key")

		if tankID == "builtin-scout" || tankID == "builtin-bruiser" {
			skipped++
			continue
		}
		if wasmKey == "" || sourceKey == "" {
			log.Printf("skip %s/%s: missing keys (wasm=%q source=%q)", tankID, version, wasmKey, sourceKey)
			skipped++
			continue
		}

		// Check if WASM actually exists in S3.
		_, err := s3c.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(wasmBucket),
			Key:    aws.String(wasmKey),
		})
		if err == nil {
			// WASM exists — skip.
			log.Printf("skip %s/%s: WASM already present", tankID, version)
			skipped++
			continue
		}

		// WASM missing — re-trigger compilation.
		log.Printf("triggering build for %s/%s (wasm=%s)", tankID, version, wasmKey)
		_, err = cb.StartBuild(ctx, &codebuild.StartBuildInput{
			ProjectName: aws.String(cbProject),
			EnvironmentVariablesOverride: []cbtypes.EnvironmentVariable{
				{Name: aws.String("TANK_ID"), Value: aws.String(tankID), Type: cbtypes.EnvironmentVariableTypePlaintext},
				{Name: aws.String("VERSION"), Value: aws.String(version), Type: cbtypes.EnvironmentVariableTypePlaintext},
				{Name: aws.String("SOURCE_S3_KEY"), Value: aws.String(sourceKey), Type: cbtypes.EnvironmentVariableTypePlaintext},
				{Name: aws.String("OUTPUT_WASM_KEY"), Value: aws.String(wasmKey), Type: cbtypes.EnvironmentVariableTypePlaintext},
				{Name: aws.String("TANK_VERSIONS_TABLE"), Value: aws.String(versionsTable), Type: cbtypes.EnvironmentVariableTypePlaintext},
			},
		})
		if err != nil {
			log.Printf("  ERROR starting build: %v", err)
			continue
		}

		// Mark as compiling so the UI shows correct status.
		_, err = ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName: aws.String(versionsTable),
			Key: map[string]dbtypes.AttributeValue{
				"tankId":  &dbtypes.AttributeValueMemberS{Value: tankID},
				"version": &dbtypes.AttributeValueMemberS{Value: version},
			},
			UpdateExpression: aws.String("SET compileStatus = :s"),
			ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
				":s": &dbtypes.AttributeValueMemberS{Value: "compiling"},
			},
		})
		if err != nil {
			log.Printf("  WARNING: could not mark as compiling: %v", err)
		}
		triggered++
	}

	fmt.Printf("\nDone: %d builds triggered, %d skipped.\n", triggered, skipped)
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("env var %s is required", k)
	}
	return v
}

func envOrDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func strAttr(item map[string]dbtypes.AttributeValue, key string) string {
	if v, ok := item[key].(*dbtypes.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}
