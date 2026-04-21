package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	// Prevent .env loading by ensuring DEV_MODE is false.
	t.Setenv("DEV_MODE", "false")
	// Auth fields are required -- set them so Load() doesn't fail validation.
	t.Setenv("ADMIN_EMAIL", "admin@example.com")
	t.Setenv("GOOGLE_CLIENT_ID", "test-client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "test-client-secret")
	t.Setenv("GOOGLE_REDIRECT_URL", "http://localhost:8080/auth/google/callback")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.DB.Path != "screens.db" {
		t.Errorf("DB.Path = %q, want %q", cfg.DB.Path, "screens.db")
	}
	if cfg.DB.MaxOpenConns != 1 {
		t.Errorf("DB.MaxOpenConns = %d, want %d", cfg.DB.MaxOpenConns, 1)
	}
	if cfg.DB.MaxIdleConns != 1 {
		t.Errorf("DB.MaxIdleConns = %d, want %d", cfg.DB.MaxIdleConns, 1)
	}
	if cfg.DB.ConnMaxLifetime != 0 {
		t.Errorf("DB.ConnMaxLifetime = %v, want %v", cfg.DB.ConnMaxLifetime, time.Duration(0))
	}
	if cfg.Auth.SessionDuration != 168*time.Hour {
		t.Errorf("Auth.SessionDuration = %v, want %v", cfg.Auth.SessionDuration, 168*time.Hour)
	}
	if cfg.Auth.CookieName != "screens_session" {
		t.Errorf("Auth.CookieName = %q, want %q", cfg.Auth.CookieName, "screens_session")
	}
}

func TestLoadCustomDBEnvVars(t *testing.T) {
	t.Setenv("DEV_MODE", "false")
	t.Setenv("DB_PATH", "/data/myapp.db")
	t.Setenv("DB_MAX_OPEN_CONNS", "10")
	t.Setenv("ADMIN_EMAIL", "admin@example.com")
	t.Setenv("GOOGLE_CLIENT_ID", "test-client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "test-client-secret")
	t.Setenv("GOOGLE_REDIRECT_URL", "http://localhost:8080/auth/google/callback")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.DB.Path != "/data/myapp.db" {
		t.Errorf("DB.Path = %q, want %q", cfg.DB.Path, "/data/myapp.db")
	}
	if cfg.DB.MaxOpenConns != 10 {
		t.Errorf("DB.MaxOpenConns = %d, want %d", cfg.DB.MaxOpenConns, 10)
	}
}

// validAuthConfig returns an AuthConfig with all required fields populated.
func validAuthConfig() AuthConfig {
	return AuthConfig{
		AdminEmail:         "admin@example.com",
		GoogleClientID:     "test-client-id",
		GoogleClientSecret: "test-client-secret",
		GoogleRedirectURL:  "http://localhost:8080/auth/google/callback",
		SessionDuration:    168 * time.Hour,
		CookieName:         "screens_session",
	}
}

func TestValidateDBPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "empty path rejected",
			path:    "",
			wantErr: true,
		},
		{
			name:    "non-empty path accepted",
			path:    "screens.db",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				HTTP: HTTPConfig{Port: 8080},
				DB:   DBConfig{Path: tt.path},
				Auth: validAuthConfig(),
			}
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil {
				if got := err.Error(); !strings.Contains(got, "DB_PATH must not be empty") {
					t.Errorf("error message %q does not mention DB_PATH", got)
				}
			}
		})
	}
}

func TestValidateAuthFields(t *testing.T) {
	tests := []struct {
		name      string
		modify    func(*AuthConfig)
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "empty AdminEmail rejected",
			modify:    func(a *AuthConfig) { a.AdminEmail = "" },
			wantErr:   true,
			errSubstr: "ADMIN_EMAIL must not be empty",
		},
		{
			name:      "empty GoogleClientID rejected",
			modify:    func(a *AuthConfig) { a.GoogleClientID = "" },
			wantErr:   true,
			errSubstr: "GOOGLE_CLIENT_ID must not be empty",
		},
		{
			name:      "empty GoogleClientSecret rejected",
			modify:    func(a *AuthConfig) { a.GoogleClientSecret = "" },
			wantErr:   true,
			errSubstr: "GOOGLE_CLIENT_SECRET must not be empty",
		},
		{
			name:      "empty GoogleRedirectURL rejected",
			modify:    func(a *AuthConfig) { a.GoogleRedirectURL = "" },
			wantErr:   true,
			errSubstr: "GOOGLE_REDIRECT_URL must not be empty",
		},
		{
			name:      "session duration below 1 minute rejected",
			modify:    func(a *AuthConfig) { a.SessionDuration = 30 * time.Second },
			wantErr:   true,
			errSubstr: "SESSION_DURATION must be at least 1 minute",
		},
		{
			name:    "all required auth fields set passes",
			modify:  func(a *AuthConfig) {},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := validAuthConfig()
			tt.modify(&auth)
			cfg := Config{
				HTTP: HTTPConfig{Port: 8080},
				DB:   DBConfig{Path: "screens.db"},
				Auth: auth,
			}
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil {
				if got := err.Error(); !strings.Contains(got, tt.errSubstr) {
					t.Errorf("error message %q does not contain %q", got, tt.errSubstr)
				}
			}
		})
	}
}

func TestConfigStringRedactsSecret(t *testing.T) {
	cfg := Config{
		HTTP: HTTPConfig{Host: "0.0.0.0", Port: 8080},
		Log:  LogConfig{Level: "info"},
		DB:   DBConfig{Path: "screens.db"},
		Auth: AuthConfig{
			AdminEmail:         "admin@example.com",
			GoogleClientID:     "my-client-id",
			GoogleClientSecret: "super-secret-value",
			GoogleRedirectURL:  "http://localhost:8080/auth/google/callback",
			SessionDuration:    168 * time.Hour,
			CookieName:         "screens_session",
		},
	}

	s := cfg.String()
	if strings.Contains(s, "super-secret-value") {
		t.Errorf("Config.String() contains the secret value: %s", s)
	}
	if !strings.Contains(s, "REDACTED") {
		t.Errorf("Config.String() does not contain REDACTED: %s", s)
	}
}
