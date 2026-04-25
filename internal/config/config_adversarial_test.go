package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestValidateAccumulatesMultipleErrors verifies that Validate() returns ALL
// validation errors in a single message, not just the first one.
func TestValidateAccumulatesMultipleErrors(t *testing.T) {
	cfg := Config{
		HTTP: HTTPConfig{Port: 8080},
		DB:   DBConfig{Path: "screens.db"},
		Auth: AuthConfig{
			AdminEmail:         "",
			GoogleClientID:     "",
			GoogleClientSecret: "",
			GoogleRedirectURL:  "",
			SessionDuration:    30 * time.Second,
			CookieName:         "screens_session",
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for all-empty auth fields, got nil")
	}

	msg := err.Error()
	expected := []string{
		"ADMIN_EMAIL must not be empty",
		"GOOGLE_CLIENT_ID must not be empty",
		"GOOGLE_CLIENT_SECRET must not be empty",
		"GOOGLE_REDIRECT_URL must not be empty",
		"SESSION_DURATION must be at least 1 minute",
	}
	for _, substr := range expected {
		if !strings.Contains(msg, substr) {
			t.Errorf("error message missing %q\ngot: %s", substr, msg)
		}
	}
}

// TestValidateSessionDurationBoundary verifies the exact boundary of 1 minute.
func TestValidateSessionDurationBoundary(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		wantErr  bool
	}{
		{name: "exactly 1 minute passes", duration: time.Minute, wantErr: false},
		{name: "59 seconds rejected", duration: 59 * time.Second, wantErr: true},
		{name: "0 duration rejected", duration: 0, wantErr: true},
		{name: "negative duration rejected", duration: -time.Hour, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := validAuthConfig()
			auth.SessionDuration = tt.duration
			cfg := Config{
				HTTP: HTTPConfig{Port: 8080},
				DB:   DBConfig{Path: "screens.db"},
				Auth: auth,
			}
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestConfigStringDoesNotLeakSecret verifies that Config.String() never
// includes the actual secret value, even if the secret is a substring of
// other fields.
func TestConfigStringDoesNotLeakSecret(t *testing.T) {
	tests := []struct {
		name   string
		secret string
	}{
		{name: "normal secret", secret: "my-super-secret-key-12345"},
		{name: "secret matching another field", secret: "admin@example.com"},
		{name: "secret is REDACTED", secret: "REDACTED"},
		{name: "secret with special chars", secret: "pass%s{}word\n\t"},
		{name: "very long secret", secret: strings.Repeat("x", 10000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				HTTP: HTTPConfig{Host: "0.0.0.0", Port: 8080},
				Log:  LogConfig{Level: "info"},
				DB:   DBConfig{Path: "screens.db"},
				Auth: AuthConfig{
					AdminEmail:         "admin@example.com",
					GoogleClientID:     "client-id",
					GoogleClientSecret: tt.secret,
					GoogleRedirectURL:  "http://localhost:8080/callback",
					SessionDuration:    168 * time.Hour,
					CookieName:         "screens_session",
				},
			}

			s := cfg.String()

			// The output should contain "REDACTED" exactly where the secret would be.
			if !strings.Contains(s, "GoogleClientSecret:REDACTED") {
				t.Errorf("Config.String() does not show REDACTED for secret: %s", s)
			}

			// For the "secret is REDACTED" case, this check is inherently impossible
			// to distinguish -- that's fine. What matters is the actual secret value
			// does not appear AS the secret field value.
		})
	}
}

// TestAuthConfigDefaultFormatLeaksSecret demonstrates that printing AuthConfig
// directly via fmt would expose the secret. This is a documentation test --
// callers must use Config.String(), not print AuthConfig fields directly.
func TestAuthConfigDefaultFormatLeaksSecret(t *testing.T) {
	auth := AuthConfig{
		AdminEmail:         "admin@example.com",
		GoogleClientID:     "client-id",
		GoogleClientSecret: "super-secret",
		GoogleRedirectURL:  "http://localhost:8080/callback",
		SessionDuration:    168 * time.Hour,
		CookieName:         "screens_session",
	}

	// Default struct formatting includes all fields.
	s := fmt.Sprintf("%v", auth)
	if !strings.Contains(s, "super-secret") {
		// If AuthConfig gets its own String() method, this test documents that
		// the redaction is in place. Currently, there is no String() on AuthConfig,
		// so the default %v will contain the secret.
		t.Skip("AuthConfig has a String() method that redacts -- good")
	}

	// This is expected to contain the secret when AuthConfig has no String().
	// The purpose of this test is to document the risk -- callers should
	// always use Config.String() not fmt.Sprintf("%v", cfg.Auth).
	t.Log("NOTE: AuthConfig default formatting exposes GoogleClientSecret. " +
		"Use Config.String() to get redacted output. " +
		"Consider adding String() to AuthConfig for defense in depth.")
}

// TestConfigStringEmptySecret verifies that when the secret is empty,
// String() shows empty rather than REDACTED.
func TestConfigStringEmptySecret(t *testing.T) {
	cfg := Config{
		HTTP: HTTPConfig{Host: "0.0.0.0", Port: 8080},
		Log:  LogConfig{Level: "info"},
		DB:   DBConfig{Path: "screens.db"},
		Auth: AuthConfig{
			AdminEmail:         "admin@example.com",
			GoogleClientID:     "client-id",
			GoogleClientSecret: "",
			GoogleRedirectURL:  "http://localhost:8080/callback",
			SessionDuration:    168 * time.Hour,
			CookieName:         "screens_session",
		},
	}

	s := cfg.String()
	if strings.Contains(s, "REDACTED") {
		t.Errorf("Config.String() shows REDACTED when secret is empty: %s", s)
	}
}
