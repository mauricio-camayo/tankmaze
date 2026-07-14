// Package main implements a minimal OAuth2-to-OIDC shim so Cognito (which
// only speaks OIDC/SAML for third-party federation) can treat GitHub or
// Discord as an identity provider — neither exposes an OIDC discovery
// document or issues an id_token; both are classic OAuth2-only (item 233
// for GitHub, item 240 for Discord, which explicitly asks to share this
// same shim rather than build a second one).
//
// Flow (this Lambda sits in the middle of every step except the initial
// browser redirect from Cognito and the final one back to it):
//
//  1. Cognito redirects the browser to GET /authorize?redirect_uri=...&state=...
//     (Cognito is the OIDC relying party here). This handler wraps Cognito's
//     redirect_uri+state into its own opaque `state` and 302s the browser to
//     the real provider's OAuth2 authorize endpoint.
//  2. The provider redirects back to GET /callback?code=...&state=<wrapped>.
//     This handler exchanges `code` for the provider's own access token,
//     fetches the user's profile from the provider's API, stores it under a
//     short-lived opaque code (DynamoDB, TTL), and 302s the browser back to
//     Cognito's original redirect_uri with that code + Cognito's original
//     state.
//  3. Cognito calls POST /token (authorization_code grant, authenticating
//     itself with the shim's own client_id/client_secret) to redeem that
//     code for a signed id_token JWT carrying the fetched profile as claims.
//  4. Cognito calls GET /jwks to verify the id_token's signature.
//
// DISABLED BY DEFAULT: gated behind GITHUB_LOGIN_ENABLED/DISCORD_LOGIN_ENABLED
// on the frontend and behind the presence of real OAuth App credentials in
// CDK context — see auth-stack.ts. This has not been exercised against a
// real deployed Cognito User Pool; see PRIORITIES.md items 233/240 for what
// remains before flipping either flag on.
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

type handler struct {
	provider oauthProvider

	ddb *dynamodb.Client
	sm  *secretsmanager.Client

	codeTable           string
	signingKeySecretArn string
	baseURL             string // this Lambda's own public URL, e.g. https://abc123.execute-api.us-east-1.amazonaws.com
	issuer              string // == baseURL; the `iss` claim on minted id_tokens

	// Cognito authenticates to /token as if the shim were a real OIDC
	// provider — rather than mint a third credential pair, this reuses the
	// same client_id/client_secret configured for the real GitHub/Discord
	// OAuth App, since only Cognito and this Lambda ever see it.
	clientID     string
	clientSecret string

	signingKey *rsa.PrivateKey // cached across warm invocations — see jwt.go
	kid        string
}

var h *handler

func main() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("oidc-shim: load AWS config: %v", err)
	}

	providerName := os.Getenv("PROVIDER")
	clientID := os.Getenv("CLIENT_ID")
	clientSecret := os.Getenv("CLIENT_SECRET")
	httpClient := &http.Client{Timeout: 10 * time.Second}

	var provider oauthProvider
	switch providerName {
	case "github":
		provider = &githubProvider{clientID: clientID, clientSecret: clientSecret, httpClient: httpClient}
	case "discord":
		provider = &discordProvider{clientID: clientID, clientSecret: clientSecret, httpClient: httpClient}
	default:
		log.Fatalf("oidc-shim: unknown PROVIDER %q (want \"github\" or \"discord\")", providerName)
	}

	baseURL := strings.TrimSuffix(os.Getenv("SHIM_BASE_URL"), "/")
	h = &handler{
		provider:            provider,
		ddb:                 dynamodb.NewFromConfig(cfg),
		sm:                  secretsmanager.NewFromConfig(cfg),
		codeTable:           os.Getenv("CODE_TABLE_NAME"),
		signingKeySecretArn: os.Getenv("SIGNING_KEY_SECRET_ARN"),
		baseURL:             baseURL,
		issuer:              baseURL,
		clientID:            clientID,
		clientSecret:        clientSecret,
	}

	lambda.Start(h.handle)
}

// handle routes HTTP API v2 proxy events to the four OIDC-shaped endpoints.
func (h *handler) handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	method := req.RequestContext.HTTP.Method
	path := strings.Trim(req.RawPath, "/")

	switch {
	case method == http.MethodGet && path == "authorize":
		return h.handleAuthorize(req), nil
	case method == http.MethodGet && path == "callback":
		return h.handleCallback(ctx, req)
	case method == http.MethodPost && path == "token":
		return h.handleToken(ctx, req)
	case method == http.MethodGet && path == "jwks":
		return h.handleJWKS(ctx)
	case method == http.MethodGet && path == "userinfo":
		return h.handleUserInfo(req)
	default:
		return errResp(http.StatusNotFound, "not found"), nil
	}
}

// handleAuthorize is the shim's own "authorization_endpoint" as far as
// Cognito is concerned. It never shows any UI itself — it immediately
// forwards the browser to the real provider, after wrapping Cognito's own
// state+redirect_uri so handleCallback can recover them later (the real
// provider only ever echoes back whatever opaque `state` it was given).
func (h *handler) handleAuthorize(req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	q := req.QueryStringParameters
	cogRedirectURI := q["redirect_uri"]
	cogState := q["state"]
	if cogRedirectURI == "" {
		return errResp(http.StatusBadRequest, "redirect_uri is required")
	}
	wrapped := wrapState(cogState, cogRedirectURI)
	shimRedirectURI := h.baseURL + "/callback"
	return events.APIGatewayV2HTTPResponse{
		StatusCode: http.StatusFound,
		Headers:    map[string]string{"Location": h.provider.authorizeURL(shimRedirectURI, wrapped)},
	}
}

// handleCallback is where the real provider redirects the browser back to
// after the user approves (or denies) access. Exchanges the provider's own
// authorization code for an access token, fetches the profile, stashes it
// under a fresh single-use code, then sends the browser on to Cognito's
// real redirect_uri exactly as a normal OIDC provider would.
func (h *handler) handleCallback(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	q := req.QueryStringParameters
	code := q["code"]
	stateParam := q["state"]
	if code == "" || stateParam == "" {
		return errResp(http.StatusBadRequest, "missing code or state"), nil
	}
	w, err := unwrapState(stateParam)
	if err != nil {
		return errResp(http.StatusBadRequest, "invalid state"), nil
	}

	shimRedirectURI := h.baseURL + "/callback"
	accessToken, err := h.provider.exchange(ctx, code, shimRedirectURI)
	if err != nil {
		log.Printf("oidc-shim (%s) exchange: %v", h.provider.name(), err)
		return errResp(http.StatusBadGateway, "failed to exchange code with provider"), nil
	}
	profile, err := h.provider.profile(ctx, accessToken)
	if err != nil {
		log.Printf("oidc-shim (%s) profile: %v", h.provider.name(), err)
		return errResp(http.StatusBadGateway, "failed to fetch profile from provider"), nil
	}

	shimCode := newRandomCode()
	if err := h.storeCode(ctx, shimCode, profile); err != nil {
		log.Printf("oidc-shim (%s) store code: %v", h.provider.name(), err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	redirectTo := w.RedirectURI + "?code=" + url.QueryEscape(shimCode) + "&state=" + url.QueryEscape(w.State)
	return events.APIGatewayV2HTTPResponse{
		StatusCode: http.StatusFound,
		Headers:    map[string]string{"Location": redirectTo},
	}, nil
}

// handleToken is the shim's "token_endpoint". Cognito calls this directly
// (server-to-server, no browser involved) to redeem the code handleCallback
// minted, authenticating with the same client_id/client_secret configured
// for the real provider (see handler.clientID doc comment above).
func (h *handler) handleToken(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	body := req.Body
	if req.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			return errResp(http.StatusBadRequest, "invalid body"), nil
		}
		body = string(decoded)
	}
	form, err := url.ParseQuery(body)
	if err != nil {
		return errResp(http.StatusBadRequest, "invalid form body"), nil
	}

	clientID, clientSecret, ok := basicAuth(req)
	if !ok {
		clientID, clientSecret = form.Get("client_id"), form.Get("client_secret")
	}
	if clientID != h.clientID || clientSecret != h.clientSecret {
		return errResp(http.StatusUnauthorized, "invalid client credentials"), nil
	}
	if form.Get("grant_type") != "authorization_code" {
		return errResp(http.StatusBadRequest, "unsupported grant_type"), nil
	}

	profile, err := h.consumeCode(ctx, form.Get("code"))
	if err != nil {
		return errResp(http.StatusBadRequest, "invalid or expired code"), nil
	}

	key, kid, err := h.loadSigningKey(ctx)
	if err != nil {
		log.Printf("oidc-shim (%s) load signing key: %v", h.provider.name(), err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	now := time.Now().Unix()
	claims := map[string]any{
		"iss":        h.issuer,
		"sub":        profile.Sub,
		"aud":        h.clientID,
		"iat":        now,
		"exp":        now + 300,
		"email":      profile.Email,
		"given_name": profile.Name,
		"picture":    profile.Picture,
	}
	idToken, err := signJWT(claims, kid, key)
	if err != nil {
		log.Printf("oidc-shim (%s) sign jwt: %v", h.provider.name(), err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}

	respBody, _ := json.Marshal(map[string]any{
		// Cognito's OIDC integration only requires id_token when no
		// userInfoEndpoint is configured (see auth-stack.ts) — access_token
		// is set to the same value since nothing else ever consumes it.
		"access_token": idToken,
		"id_token":     idToken,
		"token_type":   "Bearer",
		"expires_in":   300,
	})
	return events.APIGatewayV2HTTPResponse{
		StatusCode: http.StatusOK,
		Headers:    map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"},
		Body:       string(respBody),
	}, nil
}

// handleUserInfo is the shim's "userinfo_endpoint" — required by CDK's
// UserPoolIdentityProviderOidc whenever explicit endpoints are given (no
// discovery document), even though every claim Cognito needs is already in
// the id_token from handleToken. The access_token Cognito presents here is
// that same JWT, so this just decodes (not re-verifies — Cognito already
// verified the signature via /jwks before ever calling this) its payload
// and echoes it back, matching what a real userinfo endpoint returns.
func (h *handler) handleUserInfo(req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	var authHeader string
	for k, v := range req.Headers {
		if strings.EqualFold(k, "authorization") {
			authHeader = v
			break
		}
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" || token == authHeader {
		return errResp(http.StatusUnauthorized, "missing bearer token"), nil
	}
	claims, err := decodeJWTClaims(token)
	if err != nil {
		return errResp(http.StatusUnauthorized, "invalid token"), nil
	}
	body, _ := json.Marshal(claims)
	return events.APIGatewayV2HTTPResponse{
		StatusCode: http.StatusOK,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(body),
	}, nil
}

// handleJWKS is the shim's "jwks_uri" — Cognito fetches this to verify the
// id_token signatures handleToken produces.
func (h *handler) handleJWKS(ctx context.Context) (events.APIGatewayV2HTTPResponse, error) {
	_, kid, err := h.loadSigningKey(ctx)
	if err != nil {
		log.Printf("oidc-shim (%s) load signing key: %v", h.provider.name(), err)
		return errResp(http.StatusInternalServerError, "internal error"), nil
	}
	body, _ := json.Marshal(map[string]any{"keys": []any{h.publicJWK(kid)}})
	return events.APIGatewayV2HTTPResponse{
		StatusCode: http.StatusOK,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(body),
	}, nil
}

func errResp(status int, msg string) events.APIGatewayV2HTTPResponse {
	body, _ := json.Marshal(map[string]string{"error": msg})
	return events.APIGatewayV2HTTPResponse{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(body),
	}
}

func basicAuth(req events.APIGatewayV2HTTPRequest) (string, string, bool) {
	var authHeader string
	for k, v := range req.Headers {
		if strings.EqualFold(k, "authorization") {
			authHeader = v
			break
		}
	}
	const prefix = "Basic "
	if !strings.HasPrefix(authHeader, prefix) {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(authHeader, prefix))
	if err != nil {
		return "", "", false
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func newRandomCode() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// wrappedState carries Cognito's own state+redirect_uri through the real
// provider's OAuth2 round trip, which only ever echoes `state` back
// verbatim — no server-side storage needed for this half.
type wrappedState struct {
	State       string `json:"s"`
	RedirectURI string `json:"r"`
}

func wrapState(state, redirectURI string) string {
	b, _ := json.Marshal(wrappedState{State: state, RedirectURI: redirectURI})
	return base64.RawURLEncoding.EncodeToString(b)
}

func unwrapState(s string) (wrappedState, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return wrappedState{}, err
	}
	var w wrappedState
	err = json.Unmarshal(b, &w)
	return w, err
}
