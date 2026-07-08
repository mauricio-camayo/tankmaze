// Package main implements the async worker behind POST /auth/forgot-password
// (tank-api's forgotPassword handler). tank-api responds 202 to the caller
// immediately and self-invokes this Lambda (InvocationType Event) to do the
// actual work in the background — this is what makes the endpoint
// enumeration-safe: response timing never depends on whether the email
// exists, on Cognito lookups, or on which branch below runs. See item 217.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	cognitoidp "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	cognitotypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	sestypes "github.com/aws/aws-sdk-go-v2/service/ses/types"
)

type event struct {
	Email string `json:"email"`
}

type handler struct {
	cognito    *cognitoidp.Client
	ses        *ses.Client
	userPoolID string
	clientID   string
	sesSender  string // "" until item 214 (custom SES sender domain) ships
}

var h *handler

func main() {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}
	h = &handler{
		cognito:    cognitoidp.NewFromConfig(cfg),
		ses:        ses.NewFromConfig(cfg),
		userPoolID: os.Getenv("USER_POOL_ID"),
		clientID:   os.Getenv("USER_POOL_CLIENT_ID"),
		sesSender:  os.Getenv("SES_SENDER_EMAIL"),
	}
	lambda.Start(h.handle)
}

func (h *handler) handle(ctx context.Context, ev event) error {
	email := strings.TrimSpace(ev.Email)
	if email == "" {
		return nil
	}

	out, err := h.cognito.ListUsers(ctx, &cognitoidp.ListUsersInput{
		UserPoolId: aws.String(h.userPoolID),
		Filter:     aws.String(fmt.Sprintf(`email = "%s"`, email)),
		Limit:      aws.Int32(1),
	})
	if err != nil {
		log.Printf("forgot-password: list users for email lookup: %v", err)
		return nil
	}
	if len(out.Users) == 0 {
		// Branch (c): email doesn't exist — do nothing.
		return nil
	}

	user := out.Users[0]
	identities := cognitoAttr(user.Attributes, "identities")
	if identities == "" {
		// Branch (a): native email+password account — trigger Cognito's own
		// forgot-password code/link email via the same sender used for
		// signup verification codes today (Cognito default until item 214).
		if _, err := h.cognito.ForgotPassword(ctx, &cognitoidp.ForgotPasswordInput{
			ClientId: aws.String(h.clientID),
			Username: user.Username,
		}); err != nil {
			log.Printf("forgot-password: cognito ForgotPassword: %v", err)
		}
		return nil
	}

	// Branch (b): Google/Facebook IdP account — no Cognito password exists to
	// reset. Send a custom SES email naming the IdP instead.
	provider := identityProviderName(identities)
	if h.sesSender == "" {
		log.Printf("forgot-password: SES sender not configured (item 214 pending) — skipping IdP notice email for provider %s", provider)
		return nil
	}
	if err := h.sendIdpNoticeEmail(ctx, email, provider); err != nil {
		log.Printf("forgot-password: send IdP notice email: %v", err)
	}
	return nil
}

func (h *handler) sendIdpNoticeEmail(ctx context.Context, toEmail, provider string) error {
	subject := "Sign in to TankMaze"
	body := fmt.Sprintf(
		"This TankMaze account uses \"Sign in with %s\" — there is no password to reset. "+
			"Please sign in using the %s button on the TankMaze login page.",
		provider, provider,
	)
	_, err := h.ses.SendEmail(ctx, &ses.SendEmailInput{
		Source: aws.String(h.sesSender),
		Destination: &sestypes.Destination{
			ToAddresses: []string{toEmail},
		},
		Message: &sestypes.Message{
			Subject: &sestypes.Content{Data: aws.String(subject)},
			Body: &sestypes.Body{
				Text: &sestypes.Content{Data: aws.String(body)},
			},
		},
	})
	return err
}

// identityProviderName extracts the first linked IdP's display name (e.g.
// "Google", "Facebook") from a Cognito "identities" attribute value, which is
// a JSON array like [{"providerName":"Google", ...}]. Falls back to a generic
// label if parsing fails for any reason — never blocks sending the email.
func identityProviderName(identitiesJSON string) string {
	var identities []struct {
		ProviderName string `json:"providerName"`
	}
	if err := json.Unmarshal([]byte(identitiesJSON), &identities); err != nil || len(identities) == 0 {
		return "your identity provider"
	}
	return identities[0].ProviderName
}

func cognitoAttr(attrs []cognitotypes.AttributeType, name string) string {
	for _, a := range attrs {
		if aws.ToString(a.Name) == name {
			return aws.ToString(a.Value)
		}
	}
	return ""
}
