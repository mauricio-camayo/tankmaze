package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

// TestSignJWTVerifies is the load-bearing test here: this shim hand-rolls
// RS256 signing (jwt.go) instead of pulling in a JWT library, so this
// actually verifies the produced signature against the derived public key
// with the standard library's own verifier — not just that the output
// looks like a JWT.
func TestSignJWTVerifies(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	kid := keyID(&key.PublicKey)
	claims := map[string]any{"sub": "user-123", "email": "a@example.com", "iat": 1700000000}

	token, err := signJWT(claims, kid, key)
	if err != nil {
		t.Fatalf("signJWT: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3-part JWT, got %d parts: %s", len(parts), token)
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var header map[string]string
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if header["alg"] != "RS256" || header["kid"] != kid {
		t.Errorf("header mismatch: %+v", header)
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var gotClaims map[string]any
	if err := json.Unmarshal(claimsJSON, &gotClaims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if gotClaims["sub"] != "user-123" || gotClaims["email"] != "a@example.com" {
		t.Errorf("claims mismatch: %+v", gotClaims)
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	signingInput := parts[0] + "." + parts[1]
	hash := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hash[:], sig); err != nil {
		t.Errorf("🔍 signature does not verify against the signing key's own public half: %v", err)
	}

	// 🔍 probe: a token signed with a different key must NOT verify against
	// this key — proves the check above isn't vacuously true.
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}
	if err := rsa.VerifyPKCS1v15(&otherKey.PublicKey, crypto.SHA256, hash[:], sig); err == nil {
		t.Error("signature unexpectedly verified against an unrelated key")
	}
}

// TestPublicJWKRoundtrip confirms the JWK's base64url-encoded n/e decode
// back to the exact same modulus/exponent as the source key — a mismatch
// here would make Cognito unable to verify any id_token this shim issues.
// TestDecodeJWTClaimsRoundtrip covers handleUserInfo's dependency: it must
// recover exactly the claims signJWT embedded, and reject non-JWT input
// rather than panic.
func TestDecodeJWTClaimsRoundtrip(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	token, err := signJWT(map[string]any{"sub": "u1", "email": "u1@example.com"}, "kid1", key)
	if err != nil {
		t.Fatalf("signJWT: %v", err)
	}
	claims, err := decodeJWTClaims(token)
	if err != nil {
		t.Fatalf("decodeJWTClaims: %v", err)
	}
	if claims["sub"] != "u1" || claims["email"] != "u1@example.com" {
		t.Errorf("claims mismatch: %+v", claims)
	}

	// 🔍 probe: malformed input must error, not panic
	if _, err := decodeJWTClaims("not-a-jwt"); err == nil {
		t.Error("expected an error for non-JWT input, got nil")
	}
}

func TestPublicJWKRoundtrip(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	h := &handler{signingKey: key}
	jwk := h.publicJWK("test-kid")

	nBytes, err := base64.RawURLEncoding.DecodeString(jwk["n"].(string))
	if err != nil {
		t.Fatalf("decode n: %v", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(jwk["e"].(string))
	if err != nil {
		t.Fatalf("decode e: %v", err)
	}
	gotN := new(big.Int).SetBytes(nBytes)
	gotE := new(big.Int).SetBytes(eBytes).Int64()

	if gotN.Cmp(key.PublicKey.N) != 0 {
		t.Error("decoded modulus (n) does not match the source key")
	}
	if int(gotE) != key.PublicKey.E {
		t.Errorf("decoded exponent (e) = %d, want %d", gotE, key.PublicKey.E)
	}
	if jwk["kty"] != "RSA" || jwk["alg"] != "RS256" || jwk["kid"] != "test-kid" {
		t.Errorf("unexpected JWK metadata: %+v", jwk)
	}
}

func TestKeyIDDeterministicAndDistinct(t *testing.T) {
	key1, _ := rsa.GenerateKey(rand.Reader, 2048)
	key2, _ := rsa.GenerateKey(rand.Reader, 2048)

	if keyID(&key1.PublicKey) != keyID(&key1.PublicKey) {
		t.Error("keyID not deterministic for the same key")
	}
	if keyID(&key1.PublicKey) == keyID(&key2.PublicKey) {
		t.Error("keyID collided for two different keys")
	}
}

func TestWrapUnwrapState(t *testing.T) {
	wrapped := wrapState("cognito-state-abc", "https://auth.tankmaze.org/oauth2/idpresponse")
	got, err := unwrapState(wrapped)
	if err != nil {
		t.Fatalf("unwrapState: %v", err)
	}
	if got.State != "cognito-state-abc" || got.RedirectURI != "https://auth.tankmaze.org/oauth2/idpresponse" {
		t.Errorf("roundtrip mismatch: %+v", got)
	}

	// 🔍 probe: garbage input must error, not silently return a zero value
	// that a caller might mistake for "no state was ever set".
	if _, err := unwrapState("not-valid-base64!!!"); err == nil {
		t.Error("expected an error decoding invalid wrapped state, got nil")
	}
}

func TestBasicAuth(t *testing.T) {
	creds := base64.StdEncoding.EncodeToString([]byte("client-abc:secret-123"))
	req := events.APIGatewayV2HTTPRequest{
		Headers: map[string]string{"Authorization": "Basic " + creds},
	}
	id, secret, ok := basicAuth(req)
	if !ok || id != "client-abc" || secret != "secret-123" {
		t.Errorf("basicAuth = (%q, %q, %v), want (client-abc, secret-123, true)", id, secret, ok)
	}

	// 🔍 probe: case-insensitive header lookup (API Gateway may lowercase it)
	reqLower := events.APIGatewayV2HTTPRequest{
		Headers: map[string]string{"authorization": "Basic " + creds},
	}
	if _, _, ok := basicAuth(reqLower); !ok {
		t.Error("basicAuth should match the Authorization header case-insensitively")
	}

	// 🔍 probe: missing/non-Basic header must report ok=false, not panic
	if _, _, ok := basicAuth(events.APIGatewayV2HTTPRequest{}); ok {
		t.Error("expected ok=false with no Authorization header")
	}
	if _, _, ok := basicAuth(events.APIGatewayV2HTTPRequest{Headers: map[string]string{"Authorization": "Bearer xyz"}}); ok {
		t.Error("expected ok=false for a non-Basic Authorization header")
	}
}

func TestNewRandomCodeIsUnique(t *testing.T) {
	a := newRandomCode()
	b := newRandomCode()
	if a == b {
		t.Error("two consecutive newRandomCode() calls returned the same value")
	}
	if len(a) < 20 {
		t.Errorf("code looks too short to be unguessable: %q", a)
	}
}

func TestErrRespShape(t *testing.T) {
	resp := errResp(http.StatusBadRequest, "boom")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(resp.Body), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if body["error"] != "boom" {
		t.Errorf("body = %+v, want error=boom", body)
	}
}
