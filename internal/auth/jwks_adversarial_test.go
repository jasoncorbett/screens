package auth

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestJWKSCache_ConcurrentGetKey(t *testing.T) {
	t.Parallel()

	_, pubKey := generateTestKeyPair(t)

	var fetchCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		serveJWKS(t, w, "concurrent-kid", pubKey)
	}))
	defer server.Close()

	cache := NewJWKSCache(server.URL, nil)

	// Launch many concurrent requests for the same key.
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errors := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			_, err := cache.GetKey(context.Background(), "concurrent-kid")
			errors[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errors {
		if err != nil {
			t.Errorf("goroutine %d: GetKey() error: %v", i, err)
		}
	}

	// The stampede fix should limit fetches. Without the fix, we could see
	// up to 50 fetches. With the fix, we expect a small number (typically 1-2).
	fetches := fetchCount.Load()
	if fetches > 5 {
		t.Errorf("JWKS fetched %d times for %d concurrent requests, expected <= 5 (stampede protection broken)", fetches, goroutines)
	}
}

func TestJWKSCache_ConcurrentDifferentKIDs(t *testing.T) {
	t.Parallel()

	_, pubKey1 := generateTestKeyPair(t)
	_, pubKey2 := generateTestKeyPair(t)

	var fetchCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		serveJWKSMulti(t, w, map[string]*rsa.PublicKey{
			"kid-a": pubKey1,
			"kid-b": pubKey2,
		})
	}))
	defer server.Close()

	cache := NewJWKSCache(server.URL, nil)

	// Warm cache with kid-a.
	_, err := cache.GetKey(context.Background(), "kid-a")
	if err != nil {
		t.Fatalf("GetKey(kid-a) error: %v", err)
	}

	// Now concurrently request kid-b (not in initial cache).
	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := cache.GetKey(context.Background(), "kid-b")
			if err != nil {
				t.Errorf("GetKey(kid-b) error: %v", err)
			}
		}()
	}
	wg.Wait()

	// Should have the initial fetch + one refresh (not 20 refreshes).
	fetches := fetchCount.Load()
	if fetches > 5 {
		t.Errorf("JWKS fetched %d times, expected <= 5 (stampede protection broken)", fetches)
	}
}

func TestJWKSCache_ContextCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Delay long enough for context to be cancelled.
		select {
		case <-r.Context().Done():
			return
		case <-time.After(5 * time.Second):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"keys":[]}`))
		}
	}))
	defer server.Close()

	cache := NewJWKSCache(server.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := cache.GetKey(ctx, "any-kid")
	if err == nil {
		t.Fatal("GetKey() with cancelled context should return error, got nil")
	}
}

func TestJWKSCache_MalformedJSONResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("this is not json"))
	}))
	defer server.Close()

	cache := NewJWKSCache(server.URL, nil)
	_, err := cache.GetKey(context.Background(), "any-kid")
	if err == nil {
		t.Fatal("GetKey() with malformed JSON should return error, got nil")
	}
}

func TestJWKSCache_EmptyKeysArray(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{}})
	}))
	defer server.Close()

	cache := NewJWKSCache(server.URL, nil)
	_, err := cache.GetKey(context.Background(), "missing-kid")
	if err == nil {
		t.Fatal("GetKey() with empty keys array should return error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want it to mention not found", err)
	}
}

func TestJWKSCache_NonRSAKeysSkipped(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{
				{
					"kid": "ec-key",
					"kty": "EC",
					"x":   "abc",
					"y":   "def",
				},
			},
		})
	}))
	defer server.Close()

	cache := NewJWKSCache(server.URL, nil)
	_, err := cache.GetKey(context.Background(), "ec-key")
	if err == nil {
		t.Fatal("GetKey() should fail for EC key type, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want it to mention not found", err)
	}
}

func TestJWKSCache_InvalidModulusBase64(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{
				{
					"kid": "bad-n-key",
					"kty": "RSA",
					"n":   "!!!not-valid-base64!!!",
					"e":   "AQAB",
				},
			},
		})
	}))
	defer server.Close()

	cache := NewJWKSCache(server.URL, nil)
	_, err := cache.GetKey(context.Background(), "bad-n-key")
	if err == nil {
		t.Fatal("GetKey() with invalid modulus should return error, got nil")
	}
}

func TestJWKSCache_InvalidExponentBase64(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{
				{
					"kid": "bad-e-key",
					"kty": "RSA",
					"n":   "validbase64data",
					"e":   "!!!invalid!!!",
				},
			},
		})
	}))
	defer server.Close()

	cache := NewJWKSCache(server.URL, nil)
	_, err := cache.GetKey(context.Background(), "bad-e-key")
	if err == nil {
		t.Fatal("GetKey() with invalid exponent should return error, got nil")
	}
}

func TestParseCacheControlMaxAge_NegativeValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  time.Duration
	}{
		{"negative max-age", "max-age=-1", defaultCacheTTL},
		{"negative large", "max-age=-9999", defaultCacheTTL},
		{"max-age with spaces", "max-age= 300", defaultCacheTTL}, // space before value
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

func TestJWKSCache_HTTP404Response(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cache := NewJWKSCache(server.URL, nil)
	_, err := cache.GetKey(context.Background(), "any-kid")
	if err == nil {
		t.Fatal("GetKey() with 404 response should return error, got nil")
	}
	if !strings.Contains(err.Error(), "status 404") {
		t.Errorf("error = %q, want it to mention status 404", err)
	}
}

func TestJWKSCache_LargeResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Write a response larger than the 1MB limit.
		// The body limit should cause a decode error.
		w.Write([]byte(`{"keys":[{"kid":"k","kty":"RSA","n":"`))
		// Write 2MB of valid base64 characters
		chunk := strings.Repeat("A", 1024)
		for i := 0; i < 2048; i++ {
			w.Write([]byte(chunk))
		}
		w.Write([]byte(`","e":"AQAB"}]}`))
	}))
	defer server.Close()

	cache := NewJWKSCache(server.URL, nil)
	_, err := cache.GetKey(context.Background(), "k")
	if err == nil {
		t.Fatal("GetKey() with oversized response should return error, got nil")
	}
}

func TestNewJWKSCache_DefaultURL(t *testing.T) {
	t.Parallel()

	cache := NewJWKSCache("", nil)
	if cache.url != defaultJWKSURL {
		t.Errorf("url = %q, want %q", cache.url, defaultJWKSURL)
	}
}

func TestNewJWKSCache_DefaultClient(t *testing.T) {
	t.Parallel()

	cache := NewJWKSCache("https://example.com", nil)
	if cache.client != http.DefaultClient {
		t.Error("client should default to http.DefaultClient when nil is passed")
	}
}
