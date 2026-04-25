package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateIDToken_EmptyString(t *testing.T) {
	t.Parallel()

	gc := NewGoogleClient("client-id", "secret", "http://localhost/callback")
	_, _, err := gc.ValidateIDToken(context.Background(), "", "client-id")
	if err == nil {
		t.Fatal("ValidateIDToken(\"\") should return error, got nil")
	}
}

func TestValidateIDToken_EmptyParts(t *testing.T) {
	t.Parallel()

	// ".." produces 3 empty parts -- should fail on JSON unmarshal of empty header
	gc := NewGoogleClient("client-id", "secret", "http://localhost/callback")
	_, _, err := gc.ValidateIDToken(context.Background(), "..", "client-id")
	if err == nil {
		t.Fatal("ValidateIDToken(\"..\") should return error, got nil")
	}
}

func TestValidateIDToken_HugeInput(t *testing.T) {
	t.Parallel()

	gc := NewGoogleClient("client-id", "secret", "http://localhost/callback")

	// 100KB+ JWT should be rejected by size limit
	huge := strings.Repeat("A", 100*1024) + "." + strings.Repeat("B", 100*1024) + "." + strings.Repeat("C", 100*1024)
	_, _, err := gc.ValidateIDToken(context.Background(), huge, "client-id")
	if err == nil {
		t.Fatal("ValidateIDToken() with huge input should return error, got nil")
	}
	if !strings.Contains(err.Error(), "maximum size") {
		t.Errorf("error = %q, want it to mention maximum size", err)
	}
}

func TestValidateIDToken_NullBytesInToken(t *testing.T) {
	t.Parallel()

	gc := NewGoogleClient("client-id", "secret", "http://localhost/callback")
	// Token with null bytes should fail cleanly
	_, _, err := gc.ValidateIDToken(context.Background(), "header\x00.payload\x00.sig\x00", "client-id")
	if err == nil {
		t.Fatal("ValidateIDToken() with null bytes should return error, got nil")
	}
}

func TestValidateIDToken_FourParts(t *testing.T) {
	t.Parallel()

	gc := NewGoogleClient("client-id", "secret", "http://localhost/callback")
	_, _, err := gc.ValidateIDToken(context.Background(), "a.b.c.d", "client-id")
	if err == nil {
		t.Fatal("ValidateIDToken() with 4 parts should return error, got nil")
	}
	if !strings.Contains(err.Error(), "expected 3 parts") {
		t.Errorf("error = %q, want it to mention expected 3 parts", err)
	}
}

func TestValidateIDToken_InvalidBase64Header(t *testing.T) {
	t.Parallel()

	gc := NewGoogleClient("client-id", "secret", "http://localhost/callback")
	_, _, err := gc.ValidateIDToken(context.Background(), "!!!invalid-base64!!!.payload.sig", "client-id")
	if err == nil {
		t.Fatal("ValidateIDToken() with invalid base64 header should return error, got nil")
	}
	if !strings.Contains(err.Error(), "decode JWT header") {
		t.Errorf("error = %q, want it to mention decode JWT header", err)
	}
}

func TestValidateIDToken_MalformedJSONHeader(t *testing.T) {
	t.Parallel()

	gc := NewGoogleClient("client-id", "secret", "http://localhost/callback")
	// Valid base64 but not valid JSON
	headerB64 := base64.RawURLEncoding.EncodeToString([]byte("not json"))
	_, _, err := gc.ValidateIDToken(context.Background(), headerB64+".payload.sig", "client-id")
	if err == nil {
		t.Fatal("ValidateIDToken() with malformed JSON header should return error, got nil")
	}
	if !strings.Contains(err.Error(), "parse JWT header") {
		t.Errorf("error = %q, want it to mention parse JWT header", err)
	}
}

func TestValidateIDToken_MissingKID(t *testing.T) {
	t.Parallel()

	gc := NewGoogleClient("client-id", "secret", "http://localhost/callback")
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	_, _, err := gc.ValidateIDToken(context.Background(), header+".payload.sig", "client-id")
	if err == nil {
		t.Fatal("ValidateIDToken() without kid should return error, got nil")
	}
	if !strings.Contains(err.Error(), "missing kid") {
		t.Errorf("error = %q, want it to mention missing kid", err)
	}
}

func TestValidateIDToken_NoneAlgorithm(t *testing.T) {
	t.Parallel()

	gc := NewGoogleClient("client-id", "secret", "http://localhost/callback")
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"1"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"accounts.google.com","aud":"client-id","exp":9999999999,"email":"admin@example.com"}`))
	// "none" algorithm attack: empty signature
	token := header + "." + payload + "."
	_, _, err := gc.ValidateIDToken(context.Background(), token, "client-id")
	if err == nil {
		t.Fatal("ValidateIDToken() with 'none' algorithm should return error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported JWT algorithm") {
		t.Errorf("error = %q, want it to mention unsupported algorithm", err)
	}
}

func TestValidateIDToken_HS256Algorithm(t *testing.T) {
	t.Parallel()

	gc := NewGoogleClient("client-id", "secret", "http://localhost/callback")
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","kid":"1"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"accounts.google.com","aud":"client-id","exp":9999999999,"email":"admin@example.com"}`))
	// HMAC algorithm substitution attack
	token := header + "." + payload + ".fakesig"
	_, _, err := gc.ValidateIDToken(context.Background(), token, "client-id")
	if err == nil {
		t.Fatal("ValidateIDToken() with HS256 algorithm should return error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported JWT algorithm") {
		t.Errorf("error = %q, want it to mention unsupported algorithm", err)
	}
}

func TestValidateIDToken_ErrorsDoNotLeakToken(t *testing.T) {
	t.Parallel()

	privKey, pubKey := generateTestKeyPair(t)
	jwksServer := newJWKSServer(t, "leak-kid", pubKey)
	defer jwksServer.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(""),
		jwks:        NewJWKSCache(jwksServer.URL, nil),
	}

	// Create a token with wrong audience -- will produce an error
	token := signTestJWT(t, privKey, "leak-kid", map[string]any{
		"iss":   "https://accounts.google.com",
		"aud":   "wrong-aud",
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"email": "user@example.com",
		"name":  "Test User",
	})

	_, _, err := gc.ValidateIDToken(context.Background(), token, "correct-aud")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// The error message should NOT contain the raw token
	if strings.Contains(err.Error(), token) {
		t.Error("error message contains raw token value -- information leak")
	}

	// Also check that audience values are not leaked in the error
	if strings.Contains(err.Error(), "wrong-aud") {
		t.Error("error message contains the actual audience value -- information leak")
	}
}

func TestValidateIDToken_ExpInFarPast(t *testing.T) {
	t.Parallel()

	privKey, pubKey := generateTestKeyPair(t)
	jwksServer := newJWKSServer(t, "exp-kid", pubKey)
	defer jwksServer.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(""),
		jwks:        NewJWKSCache(jwksServer.URL, nil),
	}

	// Token that expires far in the past
	token := signTestJWT(t, privKey, "exp-kid", map[string]any{
		"iss":   "https://accounts.google.com",
		"aud":   "test-client",
		"exp":   time.Now().Add(-10 * time.Second).Unix(),
		"email": "user@example.com",
		"name":  "Expired User",
	})

	_, _, err := gc.ValidateIDToken(context.Background(), token, "test-client")
	if err == nil {
		t.Fatal("ValidateIDToken() should reject expired token, got nil")
	}
	if !strings.Contains(err.Error(), "JWT expired") {
		t.Errorf("error = %q, want it to mention JWT expired", err)
	}
}

func TestValidateIDToken_ExpZero(t *testing.T) {
	t.Parallel()

	privKey, pubKey := generateTestKeyPair(t)
	jwksServer := newJWKSServer(t, "zero-kid", pubKey)
	defer jwksServer.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(""),
		jwks:        NewJWKSCache(jwksServer.URL, nil),
	}

	// Token with exp=0 (epoch) -- definitely expired
	token := signTestJWT(t, privKey, "zero-kid", map[string]any{
		"iss":   "https://accounts.google.com",
		"aud":   "test-client",
		"exp":   0,
		"email": "user@example.com",
		"name":  "Zero Exp User",
	})

	_, _, err := gc.ValidateIDToken(context.Background(), token, "test-client")
	if err == nil {
		t.Fatal("ValidateIDToken() should reject token with exp=0, got nil")
	}
}

func TestValidateIDToken_InvalidBase64Signature(t *testing.T) {
	t.Parallel()

	_, pubKey := generateTestKeyPair(t)
	jwksServer := newJWKSServer(t, "badsig-kid", pubKey)
	defer jwksServer.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(""),
		jwks:        NewJWKSCache(jwksServer.URL, nil),
	}

	// Create a valid header and payload but with corrupted base64 in signature
	headerJSON, _ := json.Marshal(jwtHeader{Alg: "RS256", KID: "badsig-kid"})
	payloadJSON, _ := json.Marshal(map[string]any{
		"iss":   "https://accounts.google.com",
		"aud":   "test-client",
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"email": "user@example.com",
	})
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	// Use invalid base64 characters in signature
	token := headerB64 + "." + payloadB64 + ".!!!not-valid-base64!!!"
	_, _, err := gc.ValidateIDToken(context.Background(), token, "test-client")
	if err == nil {
		t.Fatal("ValidateIDToken() with invalid base64 signature should return error, got nil")
	}
	if !strings.Contains(err.Error(), "decode JWT signature") {
		t.Errorf("error = %q, want it to mention decode JWT signature", err)
	}
}

func TestValidateIDToken_TruncatedSignature(t *testing.T) {
	t.Parallel()

	privKey, pubKey := generateTestKeyPair(t)
	jwksServer := newJWKSServer(t, "trunc-kid", pubKey)
	defer jwksServer.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(""),
		jwks:        NewJWKSCache(jwksServer.URL, nil),
	}

	// Create a valid JWT, then truncate the signature
	validToken := signTestJWT(t, privKey, "trunc-kid", map[string]any{
		"iss":   "https://accounts.google.com",
		"aud":   "test-client",
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"email": "user@example.com",
		"name":  "Test User",
	})
	parts := strings.Split(validToken, ".")
	truncatedSig := parts[2][:10] // Truncate signature to 10 chars
	token := parts[0] + "." + parts[1] + "." + truncatedSig

	_, _, err := gc.ValidateIDToken(context.Background(), token, "test-client")
	if err == nil {
		t.Fatal("ValidateIDToken() with truncated signature should return error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid JWT signature") {
		t.Errorf("error = %q, want it to mention invalid JWT signature", err)
	}
}

func TestValidateIDToken_UnicodeInClaims(t *testing.T) {
	t.Parallel()

	privKey, pubKey := generateTestKeyPair(t)
	jwksServer := newJWKSServer(t, "unicode-kid", pubKey)
	defer jwksServer.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(""),
		jwks:        NewJWKSCache(jwksServer.URL, nil),
	}

	// Token with unicode display name
	token := signTestJWT(t, privKey, "unicode-kid", map[string]any{
		"iss":   "https://accounts.google.com",
		"aud":   "test-client",
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"email": "user@example.com",
		"name":  "\u65e5\u672c\u8a9e\u30e6\u30fc\u30b6\u30fc",
	})

	email, name, err := gc.ValidateIDToken(context.Background(), token, "test-client")
	if err != nil {
		t.Fatalf("ValidateIDToken() error: %v", err)
	}
	if email != "user@example.com" {
		t.Errorf("email = %q, want %q", email, "user@example.com")
	}
	if name != "\u65e5\u672c\u8a9e\u30e6\u30fc\u30b6\u30fc" {
		t.Errorf("name = %q, want %q", name, "\u65e5\u672c\u8a9e\u30e6\u30fc\u30b6\u30fc")
	}
}

func TestValidateIDToken_EmptyNameClaim(t *testing.T) {
	t.Parallel()

	privKey, pubKey := generateTestKeyPair(t)
	jwksServer := newJWKSServer(t, "noname-kid", pubKey)
	defer jwksServer.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(""),
		jwks:        NewJWKSCache(jwksServer.URL, nil),
	}

	// Token with email but empty name -- should succeed since name is optional
	token := signTestJWT(t, privKey, "noname-kid", map[string]any{
		"iss":   "https://accounts.google.com",
		"aud":   "test-client",
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"email": "user@example.com",
		"name":  "",
	})

	email, name, err := gc.ValidateIDToken(context.Background(), token, "test-client")
	if err != nil {
		t.Fatalf("ValidateIDToken() error: %v", err)
	}
	if email != "user@example.com" {
		t.Errorf("email = %q, want %q", email, "user@example.com")
	}
	if name != "" {
		t.Errorf("name = %q, want empty string", name)
	}
}

func TestValidateIDToken_ExtraFieldsInHeader(t *testing.T) {
	t.Parallel()

	privKey, pubKey := generateTestKeyPair(t)
	jwksServer := newJWKSServer(t, "extra-kid", pubKey)
	defer jwksServer.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(""),
		jwks:        NewJWKSCache(jwksServer.URL, nil),
	}

	// JWT header with extra fields that should be ignored
	token := buildJWTWithRawHeader(t, privKey, `{"alg":"RS256","kid":"extra-kid","typ":"JWT","extra":"field"}`, map[string]any{
		"iss":   "https://accounts.google.com",
		"aud":   "test-client",
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"email": "user@example.com",
		"name":  "Extra User",
	})

	email, _, err := gc.ValidateIDToken(context.Background(), token, "test-client")
	if err != nil {
		t.Fatalf("ValidateIDToken() with extra header fields error: %v", err)
	}
	if email != "user@example.com" {
		t.Errorf("email = %q, want %q", email, "user@example.com")
	}
}

func TestValidateIDToken_ExtraFieldsInPayload(t *testing.T) {
	t.Parallel()

	privKey, pubKey := generateTestKeyPair(t)
	jwksServer := newJWKSServer(t, "xpay-kid", pubKey)
	defer jwksServer.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(""),
		jwks:        NewJWKSCache(jwksServer.URL, nil),
	}

	// Payload with extra claims that should be ignored
	token := signTestJWT(t, privKey, "xpay-kid", map[string]any{
		"iss":            "https://accounts.google.com",
		"aud":            "test-client",
		"exp":            time.Now().Add(1 * time.Hour).Unix(),
		"email":          "user@example.com",
		"name":           "Extra Payload User",
		"sub":            "123456789",
		"email_verified": true,
		"hd":             "example.com",
		"at_hash":        "abc123",
		"nonce":          "random-nonce",
	})

	email, name, err := gc.ValidateIDToken(context.Background(), token, "test-client")
	if err != nil {
		t.Fatalf("ValidateIDToken() with extra payload fields error: %v", err)
	}
	if email != "user@example.com" {
		t.Errorf("email = %q, want %q", email, "user@example.com")
	}
	if name != "Extra Payload User" {
		t.Errorf("name = %q, want %q", name, "Extra Payload User")
	}
}

func TestExchangeCode_ContextCancellation(t *testing.T) {
	t.Parallel()

	// Use a context that's already cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Server that would respond normally, but context is already cancelled.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "token",
			"token_type":   "Bearer",
			"id_token":     "id-token",
		})
	}))
	defer server.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(server.URL),
		jwks:        NewJWKSCache("", nil),
	}

	_, err := gc.ExchangeCode(ctx, "code")
	if err == nil {
		t.Fatal("ExchangeCode() with cancelled context should return error, got nil")
	}
}

func TestExchangeCode_ServerError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(server.URL),
		jwks:        NewJWKSCache("", nil),
	}

	_, err := gc.ExchangeCode(context.Background(), "code")
	if err == nil {
		t.Fatal("ExchangeCode() with server error should return error, got nil")
	}
	if !strings.Contains(err.Error(), "exchange authorization code") {
		t.Errorf("error = %q, want it to mention exchange authorization code", err)
	}
}

func TestExchangeCode_MalformedJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not json at all"))
	}))
	defer server.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(server.URL),
		jwks:        NewJWKSCache("", nil),
	}

	_, err := gc.ExchangeCode(context.Background(), "code")
	if err == nil {
		t.Fatal("ExchangeCode() with malformed JSON should return error, got nil")
	}
}

func TestExchangeCode_EmptyIDTokenField(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "token",
			"token_type":   "Bearer",
			"id_token":     "",
		})
	}))
	defer server.Close()

	gc := &GoogleClient{
		oauthConfig: testOAuthConfig(server.URL),
		jwks:        NewJWKSCache("", nil),
	}

	_, err := gc.ExchangeCode(context.Background(), "code")
	if err == nil {
		t.Fatal("ExchangeCode() with empty id_token should return error, got nil")
	}
	if !strings.Contains(err.Error(), "no id_token") {
		t.Errorf("error = %q, want it to mention no id_token", err)
	}
}

func TestAuthorizationURL_EmptyState(t *testing.T) {
	t.Parallel()

	gc := NewGoogleClient("client-id", "secret", "http://localhost/callback")
	url := gc.AuthorizationURL("")

	// The oauth2 library omits state when empty. The URL should still contain
	// other required parameters.
	if !strings.Contains(url, "client_id=client-id") {
		t.Error("AuthorizationURL should include client_id")
	}
	if !strings.Contains(url, "response_type=code") {
		t.Error("AuthorizationURL should include response_type=code")
	}
}

func TestAuthorizationURL_SpecialCharsInState(t *testing.T) {
	t.Parallel()

	gc := NewGoogleClient("client-id", "secret", "http://localhost/callback")
	url := gc.AuthorizationURL("state<>\"'&=?/\\")

	// State should be URL-encoded
	if strings.Contains(url, "<>") {
		t.Error("AuthorizationURL should URL-encode special characters in state")
	}
	if !strings.Contains(url, "state=") {
		t.Error("AuthorizationURL should include state parameter")
	}
}

// buildJWTWithRawHeader builds a JWT with a raw JSON header string.
func buildJWTWithRawHeader(t *testing.T, privKey *rsa.PrivateKey, rawHeaderJSON string, claims map[string]any) string {
	t.Helper()

	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(rawHeaderJSON))
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signedContent := headerB64 + "." + payloadB64

	hash := sha256.Sum256([]byte(signedContent))
	sig, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}

	return signedContent + "." + base64.RawURLEncoding.EncodeToString(sig)
}
