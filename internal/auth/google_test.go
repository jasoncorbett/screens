package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestAuthorizationURL(t *testing.T) {
	t.Parallel()

	gc := NewGoogleClient("test-client-id", "test-secret", "http://localhost/callback")
	url := gc.AuthorizationURL("random-state-value")

	checks := []struct {
		name     string
		contains string
	}{
		{"client_id", "client_id=test-client-id"},
		{"redirect_uri", "redirect_uri="},
		{"scope contains openid", "openid"},
		{"scope contains email", "email"},
		{"scope contains profile", "profile"},
		{"state", "state=random-state-value"},
		{"response_type", "response_type=code"},
	}

	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(url, tc.contains) {
				t.Errorf("AuthorizationURL() = %q, want it to contain %q", url, tc.contains)
			}
		})
	}
}

func TestExchangeCode(t *testing.T) {
	t.Parallel()

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     "mock-id-token-value",
		})
	}))
	defer tokenServer.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(tokenServer.URL),
		jwks:        NewJWKSCache("", nil),
	}

	idToken, err := gc.ExchangeCode(context.Background(), "valid-code")
	if err != nil {
		t.Fatalf("ExchangeCode() error: %v", err)
	}
	if idToken != "mock-id-token-value" {
		t.Errorf("ExchangeCode() = %q, want %q", idToken, "mock-id-token-value")
	}
}

func TestExchangeCodeNoIDToken(t *testing.T) {
	t.Parallel()

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer tokenServer.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(tokenServer.URL),
		jwks:        NewJWKSCache("", nil),
	}

	_, err := gc.ExchangeCode(context.Background(), "valid-code")
	if err == nil {
		t.Fatal("ExchangeCode() expected error for missing id_token, got nil")
	}
	if !strings.Contains(err.Error(), "no id_token") {
		t.Errorf("ExchangeCode() error = %q, want it to mention missing id_token", err)
	}
}

func TestValidateIDToken(t *testing.T) {
	t.Parallel()

	privKey, pubKey := generateTestKeyPair(t)
	jwksServer := newJWKSServer(t, "test-kid-1", pubKey)
	defer jwksServer.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(""),
		jwks:        NewJWKSCache(jwksServer.URL, nil),
	}

	token := signTestJWT(t, privKey, "test-kid-1", map[string]any{
		"iss":   "https://accounts.google.com",
		"aud":   "test-client-id",
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"email": "user@example.com",
		"name":  "Test User",
	})

	email, name, err := gc.ValidateIDToken(context.Background(), token, "test-client-id")
	if err != nil {
		t.Fatalf("ValidateIDToken() error: %v", err)
	}
	if email != "user@example.com" {
		t.Errorf("email = %q, want %q", email, "user@example.com")
	}
	if name != "Test User" {
		t.Errorf("name = %q, want %q", name, "Test User")
	}
}

func TestValidateIDTokenRejections(t *testing.T) {
	t.Parallel()

	privKey, pubKey := generateTestKeyPair(t)
	otherPrivKey, _ := generateTestKeyPair(t)

	jwksServer := newJWKSServer(t, "test-kid-1", pubKey)
	defer jwksServer.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(""),
		jwks:        NewJWKSCache(jwksServer.URL, nil),
	}

	validClaims := map[string]any{
		"iss":   "https://accounts.google.com",
		"aud":   "test-client-id",
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"email": "user@example.com",
		"name":  "Test User",
	}

	tests := []struct {
		name     string
		token    string
		audience string
		wantErr  string
	}{
		{
			name:     "invalid signature (wrong key)",
			token:    signTestJWT(t, otherPrivKey, "test-kid-1", validClaims),
			audience: "test-client-id",
			wantErr:  "invalid JWT signature",
		},
		{
			name: "expired token",
			token: signTestJWT(t, privKey, "test-kid-1", map[string]any{
				"iss":   "https://accounts.google.com",
				"aud":   "test-client-id",
				"exp":   time.Now().Add(-1 * time.Hour).Unix(),
				"email": "user@example.com",
				"name":  "Test User",
			}),
			audience: "test-client-id",
			wantErr:  "JWT expired",
		},
		{
			name:     "wrong audience",
			token:    signTestJWT(t, privKey, "test-kid-1", validClaims),
			audience: "wrong-client-id",
			wantErr:  "invalid JWT audience",
		},
		{
			name: "wrong issuer",
			token: signTestJWT(t, privKey, "test-kid-1", map[string]any{
				"iss":   "https://evil.example.com",
				"aud":   "test-client-id",
				"exp":   time.Now().Add(1 * time.Hour).Unix(),
				"email": "user@example.com",
				"name":  "Test User",
			}),
			audience: "test-client-id",
			wantErr:  "invalid JWT issuer",
		},
		{
			name:     "not enough parts",
			token:    "only.two",
			audience: "test-client-id",
			wantErr:  "invalid JWT: expected 3 parts",
		},
		{
			name: "unsupported algorithm",
			token: buildJWTWithHeader(t, privKey, jwtHeader{Alg: "RS384", KID: "test-kid-1"}, map[string]any{
				"iss":   "https://accounts.google.com",
				"aud":   "test-client-id",
				"exp":   time.Now().Add(1 * time.Hour).Unix(),
				"email": "user@example.com",
			}),
			audience: "test-client-id",
			wantErr:  "unsupported JWT algorithm",
		},
		{
			name: "missing email claim",
			token: signTestJWT(t, privKey, "test-kid-1", map[string]any{
				"iss":  "https://accounts.google.com",
				"aud":  "test-client-id",
				"exp":  time.Now().Add(1 * time.Hour).Unix(),
				"name": "Test User",
			}),
			audience: "test-client-id",
			wantErr:  "JWT missing email claim",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := gc.ValidateIDToken(context.Background(), tt.token, tt.audience)
			if err == nil {
				t.Fatal("ValidateIDToken() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ValidateIDToken() error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateIDTokenAlternateIssuer(t *testing.T) {
	t.Parallel()

	privKey, pubKey := generateTestKeyPair(t)
	jwksServer := newJWKSServer(t, "kid-alt", pubKey)
	defer jwksServer.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(""),
		jwks:        NewJWKSCache(jwksServer.URL, nil),
	}

	// accounts.google.com (without https://) is also valid
	token := signTestJWT(t, privKey, "kid-alt", map[string]any{
		"iss":   "accounts.google.com",
		"aud":   "my-client",
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"email": "user@example.com",
		"name":  "Alt User",
	})

	email, _, err := gc.ValidateIDToken(context.Background(), token, "my-client")
	if err != nil {
		t.Fatalf("ValidateIDToken() error: %v", err)
	}
	if email != "user@example.com" {
		t.Errorf("email = %q, want %q", email, "user@example.com")
	}
}

// --- Test helpers ---

func testOAuthConfig(tokenURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost/callback",
		Scopes:       []string{"openid", "email", "profile"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.google.com/o/oauth2/auth",
			TokenURL: tokenURL,
		},
	}
}

func generateTestKeyPair(t *testing.T) (*rsa.PrivateKey, *rsa.PublicKey) {
	t.Helper()
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return privKey, &privKey.PublicKey
}

func signTestJWT(t *testing.T, privKey *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	return buildJWTWithHeader(t, privKey, jwtHeader{Alg: "RS256", KID: kid}, claims)
}

func buildJWTWithHeader(t *testing.T, privKey *rsa.PrivateKey, header jwtHeader, claims map[string]any) string {
	t.Helper()

	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signedContent := headerB64 + "." + payloadB64

	hash := sha256.Sum256([]byte(signedContent))
	sig, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}

	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	return signedContent + "." + sigB64
}

func newJWKSServer(t *testing.T, kid string, pubKey *rsa.PublicKey) *httptest.Server {
	t.Helper()

	nB64 := base64.RawURLEncoding.EncodeToString(pubKey.N.Bytes())
	eBytes := big.NewInt(int64(pubKey.E)).Bytes()
	eB64 := base64.RawURLEncoding.EncodeToString(eBytes)

	jwks := map[string]any{
		"keys": []map[string]any{
			{
				"kid": kid,
				"kty": "RSA",
				"n":   nB64,
				"e":   eB64,
			},
		},
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		json.NewEncoder(w).Encode(jwks)
	}))
}
