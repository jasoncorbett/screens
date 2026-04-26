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
		AdminEmail:             "admin@example.com",
		GoogleClientID:         "test-client-id",
		GoogleClientSecret:     "test-client-secret",
		GoogleRedirectURL:      "http://localhost:8080/auth/google/callback",
		SessionDuration:        168 * time.Hour,
		CookieName:             "screens_session",
		DeviceCookieName:       "screens_device",
		DeviceLastSeenInterval: time.Minute,
		DeviceLandingURL:       "/device/",
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

func TestLoadDeviceDefaults(t *testing.T) {
	t.Setenv("DEV_MODE", "false")
	t.Setenv("ADMIN_EMAIL", "admin@example.com")
	t.Setenv("GOOGLE_CLIENT_ID", "test-client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "test-client-secret")
	t.Setenv("GOOGLE_REDIRECT_URL", "http://localhost:8080/auth/google/callback")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.Auth.DeviceCookieName != "screens_device" {
		t.Errorf("Auth.DeviceCookieName = %q, want %q", cfg.Auth.DeviceCookieName, "screens_device")
	}
	if cfg.Auth.DeviceLastSeenInterval != time.Minute {
		t.Errorf("Auth.DeviceLastSeenInterval = %v, want %v", cfg.Auth.DeviceLastSeenInterval, time.Minute)
	}
	if cfg.Auth.DeviceLandingURL != "/device/" {
		t.Errorf("Auth.DeviceLandingURL = %q, want %q", cfg.Auth.DeviceLandingURL, "/device/")
	}
}

func TestLoadDeviceCustomEnvVars(t *testing.T) {
	t.Setenv("DEV_MODE", "false")
	t.Setenv("ADMIN_EMAIL", "admin@example.com")
	t.Setenv("GOOGLE_CLIENT_ID", "test-client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "test-client-secret")
	t.Setenv("GOOGLE_REDIRECT_URL", "http://localhost:8080/auth/google/callback")
	t.Setenv("DEVICE_COOKIE_NAME", "foo")
	t.Setenv("DEVICE_LAST_SEEN_INTERVAL", "5m")
	t.Setenv("DEVICE_LANDING_URL", "/foo/")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.Auth.DeviceCookieName != "foo" {
		t.Errorf("Auth.DeviceCookieName = %q, want %q", cfg.Auth.DeviceCookieName, "foo")
	}
	if cfg.Auth.DeviceLastSeenInterval != 5*time.Minute {
		t.Errorf("Auth.DeviceLastSeenInterval = %v, want %v", cfg.Auth.DeviceLastSeenInterval, 5*time.Minute)
	}
	if cfg.Auth.DeviceLandingURL != "/foo/" {
		t.Errorf("Auth.DeviceLandingURL = %q, want %q", cfg.Auth.DeviceLandingURL, "/foo/")
	}
}

func TestValidateDeviceFields(t *testing.T) {
	tests := []struct {
		name      string
		modify    func(*AuthConfig)
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "empty DeviceCookieName rejected",
			modify:    func(a *AuthConfig) { a.DeviceCookieName = "" },
			wantErr:   true,
			errSubstr: "DEVICE_COOKIE_NAME must not be empty",
		},
		{
			name:      "negative DeviceLastSeenInterval rejected",
			modify:    func(a *AuthConfig) { a.DeviceLastSeenInterval = -1 * time.Second },
			wantErr:   true,
			errSubstr: "DEVICE_LAST_SEEN_INTERVAL must not be negative",
		},
		{
			name:    "zero DeviceLastSeenInterval accepted",
			modify:  func(a *AuthConfig) { a.DeviceLastSeenInterval = 0 },
			wantErr: false,
		},
		{
			name:      "empty DeviceLandingURL rejected",
			modify:    func(a *AuthConfig) { a.DeviceLandingURL = "" },
			wantErr:   true,
			errSubstr: "DEVICE_LANDING_URL must not be empty",
		},
		{
			name:      "DeviceLandingURL without leading slash rejected",
			modify:    func(a *AuthConfig) { a.DeviceLandingURL = "device" },
			wantErr:   true,
			errSubstr: "DEVICE_LANDING_URL must start with /",
		},
		{
			name:    "all device fields valid passes",
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

func TestLoadDeviceLastSeenIntervalNegativeFailsValidation(t *testing.T) {
	t.Setenv("DEV_MODE", "false")
	t.Setenv("ADMIN_EMAIL", "admin@example.com")
	t.Setenv("GOOGLE_CLIENT_ID", "test-client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "test-client-secret")
	t.Setenv("GOOGLE_REDIRECT_URL", "http://localhost:8080/auth/google/callback")
	t.Setenv("DEVICE_LAST_SEEN_INTERVAL", "-1s")

	_, err := Load()
	if err == nil {
		t.Fatal("expected validation error for negative DEVICE_LAST_SEEN_INTERVAL, got nil")
	}
	if !strings.Contains(err.Error(), "DEVICE_LAST_SEEN_INTERVAL") {
		t.Errorf("error message %q does not mention DEVICE_LAST_SEEN_INTERVAL", err.Error())
	}
}

func TestLoadDeviceLandingURLBadPrefixFailsValidation(t *testing.T) {
	t.Setenv("DEV_MODE", "false")
	t.Setenv("ADMIN_EMAIL", "admin@example.com")
	t.Setenv("GOOGLE_CLIENT_ID", "test-client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "test-client-secret")
	t.Setenv("GOOGLE_REDIRECT_URL", "http://localhost:8080/auth/google/callback")
	t.Setenv("DEVICE_LANDING_URL", "device")

	_, err := Load()
	if err == nil {
		t.Fatal("expected validation error for non-/-prefixed DEVICE_LANDING_URL, got nil")
	}
	if !strings.Contains(err.Error(), "DEVICE_LANDING_URL") {
		t.Errorf("error message %q does not mention DEVICE_LANDING_URL", err.Error())
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
