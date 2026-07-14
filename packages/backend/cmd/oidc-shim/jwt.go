package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// signJWT hand-rolls a minimal RS256 JWT (RFC 7515) rather than pulling in a
// JWT library dependency — this repo pins its Go SDK versions carefully for
// toolchain compatibility (see internal/db, forgot-password-worker), and
// RS256 signing is a small, well-understood primitive to implement directly
// against the standard library.
func signJWT(claims map[string]any, kid string, priv *rsa.PrivateKey) (string, error) {
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)

	hash := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, hash[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// publicJWK renders the RSA public half as a JWK Set entry (RFC 7517) for
// the /jwks endpoint, so Cognito can verify signJWT's output.
func (h *handler) publicJWK(kid string) map[string]any {
	pub := h.signingKey.PublicKey
	return map[string]any{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": kid,
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// decodeJWTClaims extracts the claims payload from a JWT without
// re-verifying its signature — used only by handleUserInfo, where the
// caller (Cognito) already verified the token via /jwks before ever
// presenting it back to us, so this is a decode, not a trust decision.
func decodeJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("not a JWT: expected 3 parts, got %d", len(parts))
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func keyID(pub *rsa.PublicKey) string {
	sum := sha256.Sum256(pub.N.Bytes())
	return fmt.Sprintf("%x", sum)[:16]
}
