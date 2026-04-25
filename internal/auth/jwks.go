package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"
const defaultCacheTTL = 1 * time.Hour

// JWKSCache fetches and caches Google's public RSA keys for JWT signature verification.
type JWKSCache struct {
	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	expiresAt time.Time
	url       string
	client    *http.Client
}

// NewJWKSCache creates a JWKS cache that fetches keys from the given URL.
// If url is empty, the default Google JWKS URL is used.
// If client is nil, http.DefaultClient is used.
func NewJWKSCache(url string, client *http.Client) *JWKSCache {
	if url == "" {
		url = defaultJWKSURL
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &JWKSCache{
		keys:   make(map[string]*rsa.PublicKey),
		url:    url,
		client: client,
	}
}

// GetKey returns the RSA public key for the given key ID.
// It fetches fresh keys if the cache is expired or the kid is not found.
func (c *JWKSCache) GetKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	// Try cached key first.
	c.mu.RLock()
	key, ok := c.keys[kid]
	expired := time.Now().After(c.expiresAt)
	c.mu.RUnlock()

	if ok && !expired {
		return key, nil
	}

	// Refresh keys.
	if err := c.refresh(ctx); err != nil {
		return nil, fmt.Errorf("refresh JWKS: %w", err)
	}

	c.mu.RLock()
	key, ok = c.keys[kid]
	c.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("key ID %q not found in JWKS", kid)
	}
	return key, nil
}

type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	KID string `json:"kid"`
	KTY string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (c *JWKSCache) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned status %d", resp.StatusCode)
	}

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("decode JWKS response: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.KTY != "RSA" {
			continue
		}
		pub, err := parseRSAPublicKey(k.N, k.E)
		if err != nil {
			return fmt.Errorf("parse key %q: %w", k.KID, err)
		}
		keys[k.KID] = pub
	}

	ttl := parseCacheControlMaxAge(resp.Header.Get("Cache-Control"))

	c.mu.Lock()
	c.keys = keys
	c.expiresAt = time.Now().Add(ttl)
	c.mu.Unlock()

	return nil
}

// parseRSAPublicKey converts base64url-encoded modulus and exponent into an *rsa.PublicKey.
func parseRSAPublicKey(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)

	if !e.IsInt64() {
		return nil, fmt.Errorf("exponent too large")
	}

	return &rsa.PublicKey{
		N: n,
		E: int(e.Int64()),
	}, nil
}

// parseCacheControlMaxAge extracts the max-age value from a Cache-Control header.
// Returns defaultCacheTTL if not found or unparseable.
func parseCacheControlMaxAge(cc string) time.Duration {
	for _, directive := range strings.Split(cc, ",") {
		directive = strings.TrimSpace(directive)
		if strings.HasPrefix(directive, "max-age=") {
			val := strings.TrimPrefix(directive, "max-age=")
			seconds, err := strconv.Atoi(val)
			if err == nil && seconds >= 0 {
				return time.Duration(seconds) * time.Second
			}
		}
	}
	return defaultCacheTTL
}
