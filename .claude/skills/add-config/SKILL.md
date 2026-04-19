---
name: add-config
description: Add a new environment-driven configuration setting following the project's config conventions. Use when the user asks to add a config option, env var, tunable, or setting. Wires a typed field, parse call, optional validation, and a README table entry. Read .claude/rules/config.md before applying.
---

# Add a configuration setting

Follow `.claude/rules/config.md` exactly. All config flows through `internal/config/config.go`; no package outside it reads env vars directly.

## Steps

1. **Add a typed field** to the appropriate sub-struct in `internal/config/config.go`:
   - HTTP-facing setting → `HTTPConfig`.
   - Logging setting → `LogConfig`.
   - New concern that does not fit existing sub-structs → add a new sub-struct and embed it on `Config`. Do not flatten unrelated settings into `HTTPConfig` or `LogConfig`.
   - Use the precise type (`time.Duration`, `int`, `bool`, typed string enum) — not `string` for everything.

2. **Parse it in `Load()`** using the existing helpers:
   - `env("NAME", "default")` for strings
   - `envInt("NAME", default)` for ints
   - `envDuration("NAME", default)` for durations
   - `envBool("NAME", default)` for bools
   - Do not call `os.Getenv` directly — the helpers give consistent parse-error messages.

3. **Validate (if constrained)** in `Config.Validate()`:
   - Ranges, enums, mutually-exclusive combinations.
   - Accumulate errors via the existing `errs` pattern (do not early-return on the first failure).

4. **Document** in the top-level `README.md` configuration table:
   - Env var name, type, default, one-line description.
   - Keep the table ordering consistent with the struct ordering.

5. **Consume the value** by passing the resolved `Config` (or a narrow sub-struct) into the component that needs it. Do not re-read the env var at the point of use.

## Secrets

If the new setting could ever hold a secret (token, password, key), ensure `Config` has a `String()` method that redacts it, and never include it in startup log lines. The default logger must not be able to spill this value.

## Before finishing

Run the `green-bar` skill. Config changes commonly break tests that construct `config.Config` literals — fix those tests rather than re-exporting old field names.

## Do not

- Do not read env vars outside `internal/config`.
- Do not add a global or singleton; pass `Config` through.
- Do not skip the README table update — undocumented env vars rot fast.
