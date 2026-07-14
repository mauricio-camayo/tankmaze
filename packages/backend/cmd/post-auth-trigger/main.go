// Package main implements a Cognito PostAuthentication Lambda trigger (item
// 241) that records a per-user "last sign-in" timestamp. Cognito's
// ListUsers/AdminGetUser APIs expose no such field natively, so this is the
// only way to surface it in the admin Users panel. Fires after every
// successful sign-in — native or federated — regardless of which identity
// provider was used.
//
// A failure here must never block sign-in: PostAuthentication triggers that
// return an error fail the whole auth flow, so every error is logged and
// swallowed rather than returned.
package main

import (
	"context"
	"log"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/tankmaze/backend/internal/db"
)

var store *db.Store

func main() {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}
	store = db.New(dynamodb.NewFromConfig(cfg))
	lambda.Start(handle)
}

func handle(ctx context.Context, ev events.CognitoEventUserPoolsPostAuthentication) (events.CognitoEventUserPoolsPostAuthentication, error) {
	sub := ev.Request.UserAttributes["sub"]
	if sub == "" {
		log.Printf("post-auth-trigger: event has no sub attribute, skipping")
		return ev, nil
	}
	if err := store.UpdateLastLogin(ctx, sub, time.Now().Unix()); err != nil {
		log.Printf("post-auth-trigger: update last login for %s: %v", sub, err)
	}
	return ev, nil
}
