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
	}

	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	var errs []string

	if c.HTTP.Port < 1 || c.HTTP.Port > 65535 {
		errs = append(errs, fmt.Sprintf("HTTP_PORT %d is out of range", c.HTTP.Port))
	}

	if len(errs) > 0 {
		return fmt.Errorf("configuration errors:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
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
