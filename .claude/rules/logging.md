# Logging

- Use `log/slog` exclusively. Do not import `log` for anything but `log.Fatalf` at startup before slog is configured.
- `internal/logging/Setup(level, devMode)` installs the default handler. Call it once from `main.go` after config is loaded. After that, use `slog.Info`/`slog.Warn`/`slog.Error` (or `slog.With` for contextual loggers).
- Prefer key-value attrs over formatted messages:
  - Good: `slog.Info("listening", "addr", srv.Addr)`
  - Bad:  `slog.Info(fmt.Sprintf("listening on %s", srv.Addr))`
- Production emits JSON. Dev mode (`DEV_MODE=true` or a TTY on stderr) uses the colorized handler in `internal/logging/dev.go`.
- Log levels: `debug` for development-time detail, `info` for lifecycle and request outcomes, `warn` for recoverable anomalies, `error` for failures that need human attention.
- Never log secrets, full auth headers, or PII. When unsure, redact.
- Attach errors with the `"err"` key: `slog.Error("shutdown failed", "err", err)`.
