package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	// Prevent .env loading by ensuring DEV_MODE is false.
	t.Setenv("DEV_MODE", "false")

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
}

func TestLoadCustomDBEnvVars(t *testing.T) {
	t.Setenv("DEV_MODE", "false")
	t.Setenv("DB_PATH", "/data/myapp.db")
	t.Setenv("DB_MAX_OPEN_CONNS", "10")

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
