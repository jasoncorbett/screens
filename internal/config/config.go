package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTP HTTPConfig
	Log  LogConfig
	DB   DBConfig
	Auth AuthConfig
}

type AuthConfig struct {
	AdminEmail             string
	GoogleClientID         string
	GoogleClientSecret     string
	GoogleRedirectURL      string
	SessionDuration        time.Duration
	CookieName             string
	DeviceCookieName       string
	DeviceLastSeenInterval time.Duration
	DeviceLandingURL       string
}

type DBConfig struct {
	Path            string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

type HTTPConfig struct {
	Host            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
}

type LogConfig struct {
	Level   string
	DevMode bool
}

func Load() (Config, error) {
	devMode := envBool("DEV_MODE", isTerminal())
	if devMode {
		loadDotEnv(".env")
	}

	cfg := Config{
		HTTP: HTTPConfig{
			Host:            env("HTTP_HOST", "0.0.0.0"),
			Port:            envInt("HTTP_PORT", 8080),
			ReadTimeout:     envDuration("HTTP_READ_TIMEOUT", 5*time.Second),
			WriteTimeout:    envDuration("HTTP_WRITE_TIMEOUT", 10*time.Second),
			ShutdownTimeout: envDuration("HTTP_SHUTDOWN_TIMEOUT", 30*time.Second),
		},
		Log: LogConfig{
			Level:   env("LOG_LEVEL", "info"),
			DevMode: devMode,
		},
		DB: DBConfig{
			Path:            env("DB_PATH", "screens.db"),
			MaxOpenConns:    envInt("DB_MAX_OPEN_CONNS", 1),
			MaxIdleConns:    envInt("DB_MAX_IDLE_CONNS", 1),
			ConnMaxLifetime: envDuration("DB_CONN_MAX_LIFETIME", 0),
		},
		Auth: AuthConfig{
			AdminEmail:             env("ADMIN_EMAIL", ""),
			GoogleClientID:         env("GOOGLE_CLIENT_ID", ""),
			GoogleClientSecret:     env("GOOGLE_CLIENT_SECRET", ""),
			GoogleRedirectURL:      env("GOOGLE_REDIRECT_URL", ""),
			SessionDuration:        envDuration("SESSION_DURATION", 168*time.Hour),
			CookieName:             env("SESSION_COOKIE_NAME", "screens_session"),
			DeviceCookieName:       env("DEVICE_COOKIE_NAME", "screens_device"),
			DeviceLastSeenInterval: envDuration("DEVICE_LAST_SEEN_INTERVAL", time.Minute),
			DeviceLandingURL:       env("DEVICE_LANDING_URL", "/device/"),
		},
	}

	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	var errs []string

	if c.HTTP.Port < 1 || c.HTTP.Port > 65535 {
		errs = append(errs, fmt.Sprintf("HTTP_PORT %d is out of range", c.HTTP.Port))
	}

	if c.DB.Path == "" {
		errs = append(errs, "DB_PATH must not be empty")
	}

	if c.Auth.AdminEmail == "" {
		errs = append(errs, "ADMIN_EMAIL must not be empty")
	}
	if c.Auth.GoogleClientID == "" {
		errs = append(errs, "GOOGLE_CLIENT_ID must not be empty")
	}
	if c.Auth.GoogleClientSecret == "" {
		errs = append(errs, "GOOGLE_CLIENT_SECRET must not be empty")
	}
	if c.Auth.GoogleRedirectURL == "" {
		errs = append(errs, "GOOGLE_REDIRECT_URL must not be empty")
	}
	if c.Auth.SessionDuration < time.Minute {
		errs = append(errs, "SESSION_DURATION must be at least 1 minute")
	}
	if c.Auth.DeviceCookieName == "" {
		errs = append(errs, "DEVICE_COOKIE_NAME must not be empty")
	}
	if c.Auth.DeviceLastSeenInterval < 0 {
		errs = append(errs, "DEVICE_LAST_SEEN_INTERVAL must not be negative")
	}
	if c.Auth.DeviceLandingURL == "" {
		errs = append(errs, "DEVICE_LANDING_URL must not be empty")
	} else if !strings.HasPrefix(c.Auth.DeviceLandingURL, "/") {
		errs = append(errs, "DEVICE_LANDING_URL must start with /")
	}

	if len(errs) > 0 {
		return fmt.Errorf("configuration errors:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

func (c Config) String() string {
	secret := "REDACTED"
	if c.Auth.GoogleClientSecret == "" {
		secret = ""
	}
	return fmt.Sprintf(
		"HTTP{Host:%s Port:%d} Log{Level:%s DevMode:%v} DB{Path:%s} Auth{AdminEmail:%s GoogleClientID:%s GoogleClientSecret:%s GoogleRedirectURL:%s SessionDuration:%s CookieName:%s DeviceCookieName:%s DeviceLastSeenInterval:%s DeviceLandingURL:%s}",
		c.HTTP.Host, c.HTTP.Port,
		c.Log.Level, c.Log.DevMode,
		c.DB.Path,
		c.Auth.AdminEmail, c.Auth.GoogleClientID, secret, c.Auth.GoogleRedirectURL,
		c.Auth.SessionDuration, c.Auth.CookieName,
		c.Auth.DeviceCookieName, c.Auth.DeviceLastSeenInterval, c.Auth.DeviceLandingURL,
	)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		panic(fmt.Sprintf("env %q must be an integer, got %q", key, v))
	}
	return n
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		panic(fmt.Sprintf("env %q must be a duration (e.g. 5s, 1m), got %q", key, v))
	}
	return d
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		panic(fmt.Sprintf("env %q must be a boolean, got %q", key, v))
	}
	return b
}

// loadDotEnv reads a .env file and sets any variables not already present in
// the environment. Existing env vars take precedence. Silently does nothing if
// the file does not exist.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		// Don't overwrite existing env vars — real env takes precedence.
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

func isTerminal() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
