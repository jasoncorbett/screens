package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Session represents a server-side user session.
type Session struct {
	TokenHash string
	UserID    string
	CSRFToken string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// GenerateToken creates a cryptographically random token (32 bytes, hex-encoded = 64 chars).
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HashToken returns the SHA-256 hex digest of a raw token string.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
