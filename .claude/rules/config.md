# Configuration

- All configuration flows through `internal/config/config.go`. `config.Load()` is called once from `main.go` and returns a `Config` value (no globals, no singletons).
- Configuration is environment-variable driven with sensible defaults. In dev mode, `.env` is auto-loaded; real env vars always win over `.env` entries.
- Helpers already available: `env`, `envInt`, `envDuration`, `envBool`. Prefer these over calling `os.Getenv` directly so parse errors produce consistent messages.
- Adding a new setting:
  1. Add a typed field to the appropriate sub-struct (`HTTPConfig`, `LogConfig`, or a new one).
  2. Parse it in `Load()` using the `env*` helpers with a sensible default.
  3. If the value has constraints (ranges, enums), validate in `Config.Validate()` and accumulate errors via the existing `errs` pattern.
  4. Document the env var name, default, and description in the top-level `README.md` configuration table.
- Do not read environment variables outside this package. Pass the resolved `Config` (or a narrow sub-struct) into components that need it.
- Never log the full `Config`; it may include secrets in the future. If a secret field is added, implement a `String()` method that redacts it.
