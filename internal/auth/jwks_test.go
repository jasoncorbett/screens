package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestJWKSCacheFetchesAndCachesKeys(t *testing.T) {
	t.Parallel()

	_, pubKey := generateTestKeyPair(t)

	var fetchCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		serveJWKS(t, w, "kid-1", pubKey)
	}))
	defer server.Close()

	cache := NewJWKSCache(server.URL, nil)

	// First call should fetch.
	key1, err := cache.GetKey(context.Background(), "kid-1")
	if err != nil {
		t.Fatalf("GetKey() first call error: %v", err)
	}
	if key1 == nil {
		t.Fatal("GetKey() returned nil key")
	}

	// Second call should use cache (no new fetch).
	key2, err := cache.GetKey(context.Background(), "kid-1")
	if err != nil {
		t.Fatalf("GetKey() second call error: %v", err)
	}
	if key2 == nil {
		t.Fatal("GetKey() second call returned nil key")
	}

	if fetchCount.Load() != 1 {
		t.Errorf("JWKS fetched %d times, want 1", fetchCount.Load())
	}
}

func TestJWKSCacheRefetchesOnExpiry(t *testing.T) {
	t.Parallel()

	_, pubKey := generateTestKeyPair(t)

	var fetchCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// Very short TTL to test expiry.
		w.Header().Set("Cache-Control", "max-age=0")
		serveJWKS(t, w, "kid-expire", pubKey)
	}))
	defer server.Close()

	cache := NewJWKSCache(server.URL, nil)

	_, err := cache.GetKey(context.Background(), "kid-expire")
	if err != nil {
		t.Fatalf("GetKey() first call error: %v", err)
	}

	// Sleep briefly to ensure cache expires (max-age=0 means immediately expired).
	time.Sleep(10 * time.Millisecond)

	_, err = cache.GetKey(context.Background(), "kid-expire")
	if err != nil {
		t.Fatalf("GetKey() second call error: %v", err)
	}

	if fetchCount.Load() < 2 {
		t.Errorf("JWKS fetched %d times, want at least 2 after expiry", fetchCount.Load())
	}
}

func TestJWKSCacheRefetchesOnUnknownKID(t *testing.T) {
	t.Parallel()

	_, pubKey1 := generateTestKeyPair(t)
	_, pubKey2 := generateTestKeyPair(t)

	var fetchCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := fetchCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		// On second fetch, serve both keys.
		if count >= 2 {
			serveJWKSMulti(t, w, map[string]*rsa.PublicKey{
				"kid-1": pubKey1,
				"kid-2": pubKey2,
			})
		} else {
			serveJWKS(t, w, "kid-1", pubKey1)
		}
	}))
	defer server.Close()

	cache := NewJWKSCache(server.URL, nil)

	// First call, kid-1 available.
	_, err := cache.GetKey(context.Background(), "kid-1")
	if err != nil {
		t.Fatalf("GetKey(kid-1) error: %v", err)
	}

	// Ask for kid-2 which is not cached yet -- should refetch.
	_, err = cache.GetKey(context.Background(), "kid-2")
	if err != nil {
		t.Fatalf("GetKey(kid-2) error: %v", err)
	}

	if fetchCount.Load() != 2 {
		t.Errorf("JWKS fetched %d times, want 2", fetchCount.Load())
	}
}

func TestJWKSCacheUnknownKIDAfterRefresh(t *testing.T) {
	t.Parallel()

	_, pubKey := generateTestKeyPair(t)
	server := newJWKSServer(t, "kid-known", pubKey)
	defer server.Close()

	cache := NewJWKSCache(server.URL, nil)

	_, err := cache.GetKey(context.Background(), "kid-nonexistent")
	if err == nil {
		t.Fatal("GetKey() expected error for unknown kid, got nil")
	}
}

func TestJWKSCacheServerError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cache := NewJWKSCache(server.URL, nil)

	_, err := cache.GetKey(context.Background(), "any-kid")
	if err == nil {
		t.Fatal("GetKey() expected error for server error, got nil")
	}
}

func TestParseCacheControlMaxAge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  time.Duration
	}{
		{"simple max-age", "max-age=300", 300 * time.Second},
		{"with other directives", "public, max-age=600, must-revalidate", 600 * time.Second},
		{"no max-age", "public, no-cache", defaultCacheTTL},
		{"empty string", "", defaultCacheTTL},
		{"invalid number", "max-age=abc", defaultCacheTTL},
		{"zero max-age", "max-age=0", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCacheControlMaxAge(tt.input)
			if got != tt.want {
				t.Errorf("parseCacheControlMaxAge(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- Helpers ---

func serveJWKS(t *testing.T, w http.ResponseWriter, kid string, pubKey *rsa.PublicKey) {
	t.Helper()
	serveJWKSMulti(t, w, map[string]*rsa.PublicKey{kid: pubKey})
}

func serveJWKSMulti(t *testing.T, w http.ResponseWriter, keys map[string]*rsa.PublicKey) {
	t.Helper()
	var jwkKeys []map[string]any
	for kid, pubKey := range keys {
		jwkKeys = append(jwkKeys, jwkFromPublicKey(kid, pubKey))
	}
	json.NewEncoder(w).Encode(map[string]any{"keys": jwkKeys})
}

func jwkFromPublicKey(kid string, pubKey *rsa.PublicKey) map[string]any {
	return map[string]any{
		"kid": kid,
		"kty": "RSA",
		"n":   base64RawURL(pubKey.N.Bytes()),
		"e":   base64RawURL(bigIntBytes(pubKey.E)),
	}
}

func base64RawURL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func bigIntBytes(i int) []byte {
	return big.NewInt(int64(i)).Bytes()
}
