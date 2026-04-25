package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// GoogleClient handles OAuth 2.0 interactions with Google.
// Wraps golang.org/x/oauth2 for the authorization code flow
// and provides ID token validation.
type GoogleClient struct {
	oauthConfig *oauth2.Config
	jwks        *JWKSCache
}

// NewGoogleClient creates a Google OAuth client.
func NewGoogleClient(clientID, clientSecret, redirectURL string) *GoogleClient {
	return &GoogleClient{
		oauthConfig: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		},
		jwks: NewJWKSCache("", nil),
	}
}

// AuthorizationURL builds the Google authorization URL with the given state.
func (g *GoogleClient) AuthorizationURL(state string) string {
	return g.oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

// ExchangeCode exchanges an authorization code for tokens and returns the raw ID token string.
func (g *GoogleClient) ExchangeCode(ctx context.Context, code string) (string, error) {
	token, err := g.oauthConfig.Exchange(ctx, code)
	if err != nil {
		return "", fmt.Errorf("exchange authorization code: %w", err)
	}

	idToken, ok := token.Extra("id_token").(string)
	if !ok || idToken == "" {
		return "", fmt.Errorf("no id_token in token response")
	}

	return idToken, nil
}

// maxJWTSize limits the size of JWT tokens to prevent excessive memory allocation.
// Google ID tokens are typically under 2KB; 64KB provides generous headroom.
const maxJWTSize = 64 * 1024

// ValidateIDToken verifies the ID token JWT and extracts user info.
// Validates: signature (via Google's JWKS), expiry, audience, issuer.
// Returns the user's email and display name.
func (g *GoogleClient) ValidateIDToken(ctx context.Context, rawIDToken string, expectedAudience string) (email, displayName string, err error) {
	if len(rawIDToken) > maxJWTSize {
		return "", "", fmt.Errorf("JWT exceeds maximum size")
	}

	parts := strings.Split(rawIDToken, ".")
	if len(parts) != 3 {
		return "", "", fmt.Errorf("invalid JWT: expected 3 parts, got %d", len(parts))
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", fmt.Errorf("decode JWT header: %w", err)
	}

	var header jwtHeader
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return "", "", fmt.Errorf("parse JWT header: %w", err)
	}

	if header.Alg != "RS256" {
		return "", "", fmt.Errorf("unsupported JWT algorithm: %s", header.Alg)
	}

	if header.KID == "" {
		return "", "", fmt.Errorf("JWT header missing kid")
	}

	pubKey, err := g.jwks.GetKey(ctx, header.KID)
	if err != nil {
		return "", "", fmt.Errorf("get signing key: %w", err)
	}

	// Verify RS256 signature: SHA-256 hash of "header.payload", verified with RSA PKCS1v15.
	signedContent := parts[0] + "." + parts[1]
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", "", fmt.Errorf("decode JWT signature: %w", err)
	}

	hash := sha256.Sum256([]byte(signedContent))
	if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hash[:], sigBytes); err != nil {
		return "", "", fmt.Errorf("invalid JWT signature")
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", fmt.Errorf("decode JWT payload: %w", err)
	}

	var claims idTokenClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return "", "", fmt.Errorf("parse JWT claims: %w", err)
	}

	if claims.Iss != "accounts.google.com" && claims.Iss != "https://accounts.google.com" {
		return "", "", fmt.Errorf("invalid JWT issuer: %s", claims.Iss)
	}

	if claims.Aud != expectedAudience {
		return "", "", fmt.Errorf("invalid JWT audience")
	}

	if time.Now().Unix() > claims.Exp {
		return "", "", fmt.Errorf("JWT expired")
	}

	if claims.Email == "" {
		return "", "", fmt.Errorf("JWT missing email claim")
	}

	return claims.Email, claims.Name, nil
}

type jwtHeader struct {
	Alg string `json:"alg"`
	KID string `json:"kid"`
}

type idTokenClaims struct {
	Iss   string `json:"iss"`
	Aud   string `json:"aud"`
	Exp   int64  `json:"exp"`
	Email string `json:"email"`
	Name  string `json:"name"`
}
