# Go Style

- Target **Go 1.26+**. Use modern stdlib features (pattern-based `net/http.ServeMux`, `log/slog`, `errors.Join`, `slices`/`maps` packages).
- **Standard library only.** Do not add third-party dependencies without explicit approval — no frameworks, no routers, no DI containers, no assertion libraries. Keep `go.mod` empty of `require` blocks unless there is a strong reason.
- When a dependency is approved and added (or an existing one upgraded), use the current **latest stable** release. Don't default to old or pinned versions unless there's a specific compatibility reason. This applies to Go modules, CDN-hosted assets, and any other external resources.
- Keep packages small and focused. Non-exported helpers live under `internal/`.
- Return errors; do not panic in request paths. Wrap with `fmt.Errorf("context: %w", err)` to preserve the chain. Use `errors.Is` / `errors.As` at handling sites.
- Accept `context.Context` as the first parameter on any function that does I/O or is cancellable.
- Exported identifiers need doc comments. Unexported ones only when the name is not self-explanatory.
- Minimum green bar before commit: `gofmt -l .` is empty, `go vet ./...` is clean, `go build ./...` and `go test ./...` pass.
