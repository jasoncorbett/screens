---
id: ARCH-004
title: "Theme System"
spec: SPEC-004
status: draft
created: 2026-04-30
author: architect
---

# Theme System Architecture

## Overview

The Theme System adds a new domain entity -- the Theme -- that bundles colors, fonts, and a corner-radius value into a named, admin-managed unit. Themes live in their own table in the existing SQLite database, are managed through a new `/admin/themes` family of routes, and expose a single rendering helper (`Theme.CSSVariables()`) that returns a `:root { ... }` CSS block ready to embed in a `<style>` tag. Screen Display (a later Phase 2 spec) will be the primary consumer of that helper. A default theme is seeded on first start so that downstream code never has to handle the "no theme exists" case.

The implementation reuses everything Phase 1 established: the migration runner, sqlc-generated query code, `auth.GenerateToken` for ID generation, the `views/` route registration pattern, the `auth.Service` / `views.Deps` wiring idiom, and the existing `RequireAuth` + `RequireRole(RoleAdmin)` + `RequireCSRF` middleware chain. No new third-party dependencies are introduced.

## References

- Spec: `docs/plans/specs/phase-2-display/spec-theme-system.md`
- Related ADRs: ADR-004 (this feature -- DB-stored themes, server-rendered CSS injection, hex-only color storage)
- Prerequisite architecture: ARCH-001 (Storage Engine), ARCH-002 (Admin Auth), ARCH-003 (Device Auth)

## Data Model

### Database Schema

```sql
-- 006_create-themes.sql
-- +up
CREATE TABLE IF NOT EXISTS themes (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL UNIQUE,
    is_default       INTEGER NOT NULL DEFAULT 0,
    color_bg         TEXT NOT NULL,
    color_surface    TEXT NOT NULL,
    color_border     TEXT NOT NULL,
    color_text       TEXT NOT NULL,
    color_text_muted TEXT NOT NULL,
    color_accent     TEXT NOT NULL,
    font_family      TEXT NOT NULL,
    font_family_mono TEXT NOT NULL DEFAULT '',
    radius           TEXT NOT NULL,
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Partial unique index enforces "at most one default theme" at the DB layer.
CREATE UNIQUE INDEX themes_one_default ON themes(is_default) WHERE is_default = 1;

-- +down
DROP INDEX IF EXISTS themes_one_default;
DROP TABLE IF EXISTS themes;
```

Notes on the schema:

- `id` is a 32-char hex string (16 bytes of entropy), generated via the same `auth.GenerateToken[:32]` primitive used by `users.id`, `sessions.token_hash`, `invitations.id`, and `devices.id`.
- `name` is `UNIQUE` so the admin UI can identify themes by a human-readable handle.
- `is_default` is `INTEGER` (SQLite's boolean idiom: `0` or `1`). The partial unique index `themes_one_default` makes "two rows both having `is_default = 1`" a hard SQL error rather than a silent data corruption.
- All color fields are TEXT and validated at the application layer (strict hex). They are NOT CHECK-constrained at the DB layer; SQLite CHECK constraints would either need to embed regex (no native support) or duplicate the Go-side regex in SQL form. Application-layer validation is the single source of truth.
- `font_family_mono` defaults to `''`. The default-seeded theme provides a real value; the admin UI does not surface this column in v1, so manually-created themes inherit the empty default. The CSS rendering helper omits the `--font-family-mono` declaration when the field is empty.
- Timestamps follow the existing TEXT-as-ISO8601 pattern.
- No FK constraint to anything else. The Screen Model spec adds `screens.theme_id REFERENCES themes(id)` from the screens side; this spec does not pre-emptively model that relationship.

### Go Types

```go
// internal/themes/theme.go
package themes

import "time"

// Theme is the domain type returned by the service. Color and radius values
// are validated and normalised before they reach this struct, so callers
// can render them into CSS without re-checking.
type Theme struct {
    ID             string
    Name           string
    IsDefault      bool
    ColorBg        string
    ColorSurface   string
    ColorBorder    string
    ColorText      string
    ColorTextMuted string
    ColorAccent    string
    FontFamily     string
    FontFamilyMono string // may be ""; renderer omits the declaration when empty
    Radius         string
    CreatedAt      time.Time
    UpdatedAt      time.Time
}

// themeFromRow translates the sqlc-generated row into the domain type.
func themeFromRow(row db.Theme) (Theme, error) // implementation in service.go
```

```go
// internal/themes/css.go
package themes

import (
    "strings"
)

// CSSVariables returns a :root { ... } CSS block with the theme's
// values written as custom properties. The output is safe to embed
// in a <style>...</style> element provided the input theme was
// produced via Service.Create / Service.Update / Service.EnsureDefault
// (all of which run the field validators).
//
// Output is deterministic: the same Theme value yields the same string
// byte-for-byte.
func (t Theme) CSSVariables() string {
    var b strings.Builder
    b.WriteString(":root {\n")
    b.WriteString("  --bg: ")          ; b.WriteString(t.ColorBg)        ; b.WriteString(";\n")
    b.WriteString("  --surface: ")     ; b.WriteString(t.ColorSurface)   ; b.WriteString(";\n")
    b.WriteString("  --border: ")      ; b.WriteString(t.ColorBorder)    ; b.WriteString(";\n")
    b.WriteString("  --text: ")        ; b.WriteString(t.ColorText)      ; b.WriteString(";\n")
    b.WriteString("  --text-muted: ")  ; b.WriteString(t.ColorTextMuted) ; b.WriteString(";\n")
    b.WriteString("  --accent: ")      ; b.WriteString(t.ColorAccent)    ; b.WriteString(";\n")
    b.WriteString("  --radius: ")      ; b.WriteString(t.Radius)         ; b.WriteString(";\n")
    b.WriteString("  --font-family: ") ; b.WriteString(t.FontFamily)     ; b.WriteString(";\n")
    if t.FontFamilyMono != "" {
        b.WriteString("  --font-family-mono: ") ; b.WriteString(t.FontFamilyMono) ; b.WriteString(";\n")
    }
    b.WriteString("}\n")
    return b.String()
}
```

The `CSSVariables` method is the contract that downstream Screen Display will use. Its signature is stable: future schema additions become new declarations inside the same block; the method signature does not change.

## API Contract

### Endpoints

| Method | Path | Request Body | Response | Auth |
|--------|------|--------------|----------|------|
| GET    | /admin/themes                       | -                                                      | HTML theme list                  | admin |
| POST   | /admin/themes                       | form: name, color_bg, color_surface, color_border, color_text, color_text_muted, color_accent, font_family, radius, _csrf | 302 -> /admin/themes?msg=created | admin |
| GET    | /admin/themes/{id}/edit             | -                                                      | HTML edit form                   | admin |
| POST   | /admin/themes/{id}                  | same as create                                          | 302 -> /admin/themes?msg=updated | admin |
| POST   | /admin/themes/{id}/delete           | form: _csrf                                             | 302 -> /admin/themes?msg=deleted | admin |
| POST   | /admin/themes/{id}/set-default      | form: _csrf                                             | 302 -> /admin/themes?msg=set_default | admin |

All endpoints are gated by `RequireAuth` -> `RequireRole(RoleAdmin)` -> `RequireCSRF` (the existing chain wrapping the admin sub-mux). Device identities receive 403 from `RequireRole`.

### Request/Response Examples

A successful create:

```http
POST /admin/themes HTTP/1.1
Content-Type: application/x-www-form-urlencoded

name=kitchen-day&color_bg=%23ffffff&color_surface=%23f5f5f5&color_border=%23dcdcdc&color_text=%23111111&color_text_muted=%23555555&color_accent=%237b93ff&font_family=system-ui&radius=10px&_csrf=...

HTTP/1.1 302 Found
Location: /admin/themes?msg=created
```

A validation error redisplays the form inline (no redirect) with the rejected values and a clear error message:

```http
POST /admin/themes HTTP/1.1
Content-Type: application/x-www-form-urlencoded

name=ok&color_bg=red&...

HTTP/1.1 200 OK
Content-Type: text/html

<...form re-rendered with the previously typed values and the message
"color_bg: value must be a hex color like #1a2b3c">
```

Set-default:

```http
POST /admin/themes/abc.../set-default HTTP/1.1
Content-Type: application/x-www-form-urlencoded

_csrf=...

HTTP/1.1 302 Found
Location: /admin/themes?msg=set_default
```

The CSS rendering helper invocation (consumed by Screen Display in a future spec) looks like:

```go
theme, err := themesSvc.GetDefault(ctx)
// ...
@layout("Screen", theme.CSSVariables()) {
    ...
}
```

In templ:

```go
templ layout(title, themeCSS string) {
    <!DOCTYPE html>
    <html>
        <head>
            <title>{ title }</title>
            <link rel="stylesheet" href="/static/css/app.css"/>
            <style>{ themeCSS }</style>
        </head>
        ...
    </html>
}
```

(The above is illustrative for downstream Screen Display. This spec ships only the helper; layout templ changes belong to Screen Display's task list.)

## Component Design

### Package Layout

```
internal/
  themes/
    theme.go         -- NEW: Theme type, themeFromRow, validators
    service.go       -- NEW: Service struct, NewService, Create/Get/List/Update/Delete/SetDefault/GetDefault/EnsureDefault
    css.go           -- NEW: Theme.CSSVariables()
    validate.go      -- NEW: validateName, validateHex, validateFont, validateRadius, normalize helpers
    service_test.go  -- NEW: service tests (CRUD, defaults, seed)
    css_test.go      -- NEW: CSS rendering tests
    validate_test.go -- NEW: validator tests
  config/
    config.go        -- MODIFY: add ThemeConfig sub-struct (or extend an existing one) with DefaultName field
  db/
    migrations/
      006_create-themes.sql  -- NEW
    queries/
      themes.sql             -- NEW (sqlc query file)
    themes.sql.go            -- NEW (sqlc-generated)
    models.go                -- MODIFY: gains a `Theme` struct from sqlc
views/
  themes.go            -- NEW: list / create / edit / update / delete / set-default handlers
  themes.templ         -- NEW: list page + edit form
  routes.go            -- MODIFY: register /admin/themes routes inside the admin sub-mux (RequireRole(admin)
                                  pattern, mirror /admin/devices)
  themes_test.go       -- NEW: handler tests using httptest + db.OpenTestDB(t)
main.go               -- MODIFY: construct themes.Service, run EnsureDefault at startup, pass into views.Deps
```

### Key Interfaces and Functions

#### internal/themes/theme.go

```go
package themes

import (
    "time"
)

// Theme is the validated, domain-level theme type.
type Theme struct {
    ID             string
    Name           string
    IsDefault      bool
    ColorBg        string
    ColorSurface   string
    ColorBorder    string
    ColorText      string
    ColorTextMuted string
    ColorAccent    string
    FontFamily     string
    FontFamilyMono string
    Radius         string
    CreatedAt      time.Time
    UpdatedAt      time.Time
}

// Input collects the user-supplied fields needed to create or update a theme.
// Service.Create / Service.Update validate and normalise this struct.
// Validation errors are returned as ValidationError.
type Input struct {
    Name           string
    ColorBg        string
    ColorSurface   string
    ColorBorder    string
    ColorText      string
    ColorTextMuted string
    ColorAccent    string
    FontFamily     string
    FontFamilyMono string // optional; "" is allowed
    Radius         string
}
```

#### internal/themes/validate.go

```go
package themes

import (
    "errors"
    "fmt"
    "regexp"
    "strings"
)

// ValidationError carries per-field validation messages back to the
// admin UI so that a re-rendered form can show errors next to the
// offending input.
type ValidationError struct {
    Fields map[string]string // field name -> human-readable message
}

func (v *ValidationError) Error() string {
    parts := make([]string, 0, len(v.Fields))
    for k, msg := range v.Fields {
        parts = append(parts, fmt.Sprintf("%s: %s", k, msg))
    }
    return "theme validation failed: " + strings.Join(parts, "; ")
}

// IsValidationError reports whether err is a *ValidationError.
func IsValidationError(err error) bool {
    var ve *ValidationError
    return errors.As(err, &ve)
}

var (
    nameRe   = regexp.MustCompile(`^[A-Za-z0-9 _-]{1,64}$`)
    hexRe    = regexp.MustCompile(`^#([0-9A-Fa-f]{3}|[0-9A-Fa-f]{6})$`)
    radiusRe = regexp.MustCompile(`^(0|[0-9]+px|[0-9]+(\.[0-9]+)?(rem|em))$`)
)

// validateInput runs every field through its validator and returns a
// non-nil *ValidationError if any field fails. Returns a normalised copy
// of the input on success (lower-cased hex values, trimmed strings).
func validateInput(in Input) (Input, error)

// normaliseHex returns the lower-case form of a validated hex color.
func normaliseHex(v string) string { return strings.ToLower(v) }

// validateFontFamily rejects values containing characters that would
// break out of a CSS declaration (`;`, `{`, `}`, `<`, `>`, backslash,
// or any control character including newline).
func validateFontFamily(v string) (string, error)
```

#### internal/themes/service.go

```go
package themes

import (
    "context"
    "database/sql"
    "errors"

    "github.com/jasoncorbett/screens/internal/db"
)

// ErrThemeNotFound is returned when an operation targets a theme id that
// does not exist.
var ErrThemeNotFound = errors.New("theme not found")

// ErrCannotDeleteDefault is returned when an operation would delete the
// system-default theme.
var ErrCannotDeleteDefault = errors.New("cannot delete the default theme")

// ErrDuplicateName is returned when create / update would produce a
// duplicate name.
var ErrDuplicateName = errors.New("theme name already in use")

// Config holds theme-specific configuration. Mirrors the auth.Config
// shape -- a small struct passed in at construction.
type Config struct {
    // DefaultName is the name used for the auto-seeded default theme on
    // first startup. Defaults to "default" via config.AuthConfig.
    DefaultName string
}

// Service orchestrates theme operations. Construct with NewService.
type Service struct {
    sqlDB   *sql.DB
    queries *db.Queries
    config  Config
}

func NewService(sqlDB *sql.DB, cfg Config) *Service {
    return &Service{sqlDB: sqlDB, queries: db.New(sqlDB), config: cfg}
}

// EnsureDefault inserts a theme with is_default=1 if and only if no
// such row currently exists. Idempotent. Called once from main.go after
// migrations have run. The seeded theme's color values are constants
// matching the existing static/css/app.css palette.
func (s *Service) EnsureDefault(ctx context.Context) error

// Create validates the input and inserts a new theme row. Returns
// *ValidationError for input failures, ErrDuplicateName for unique-name
// violations, or other database errors.
func (s *Service) Create(ctx context.Context, in Input) (Theme, error)

// GetByID returns a theme by ID. Returns ErrThemeNotFound when no row
// matches.
func (s *Service) GetByID(ctx context.Context, id string) (Theme, error)

// List returns every theme ordered by name.
func (s *Service) List(ctx context.Context) ([]Theme, error)

// GetDefault returns the system default theme. Returns ErrThemeNotFound
// if (somehow) no default exists -- callers should treat this as a
// startup invariant violation.
func (s *Service) GetDefault(ctx context.Context) (Theme, error)

// Update mutates an existing theme. Validation and uniqueness checks
// match Create. Updates "updated_at" to the current time. is_default is
// preserved across updates -- to change it, call SetDefault.
func (s *Service) Update(ctx context.Context, id string, in Input) (Theme, error)

// Delete removes a theme by ID. Returns ErrCannotDeleteDefault if the
// theme is currently the system default. Returns ErrThemeNotFound if
// no row matches.
func (s *Service) Delete(ctx context.Context, id string) error

// SetDefault marks the given theme as the system default. Atomically
// clears is_default on the previously-default theme. Returns
// ErrThemeNotFound if no row matches.
func (s *Service) SetDefault(ctx context.Context, id string) error
```

#### internal/themes/css.go

See the snippet under "Go Types" above.

#### views/themes.go

Mirrors `views/devices.go` exactly in shape: handler factories returning `http.HandlerFunc`, reading the user / session from context, calling the service, rendering or redirecting.

```go
func handleThemeList(themesSvc *themes.Service) http.HandlerFunc
func handleThemeCreate(themesSvc *themes.Service) http.HandlerFunc
func handleThemeEditForm(themesSvc *themes.Service) http.HandlerFunc
func handleThemeUpdate(themesSvc *themes.Service) http.HandlerFunc
func handleThemeDelete(themesSvc *themes.Service) http.HandlerFunc
func handleThemeSetDefault(themesSvc *themes.Service) http.HandlerFunc
```

The create / update handlers:
- Read form values into a `themes.Input`.
- Call `themesSvc.Create` / `themesSvc.Update`.
- On `*themes.ValidationError`: render the form template inline with the rejected values and per-field error messages (no redirect, status 200).
- On `themes.ErrDuplicateName`: same as validation error (treat name uniqueness as a field-level error on `name`).
- On `themes.ErrThemeNotFound` (update only): 302 to `/admin/themes?error=Theme+not+found`.
- On other errors: log at `slog.Error` and 302 to `/admin/themes?error=Could+not+...`.
- On success: 302 to `/admin/themes?msg=created` (or `updated`).

The delete handler:
- 302 to `/admin/themes?error=...` for `ErrCannotDeleteDefault` and `ErrThemeNotFound`.
- 302 to `/admin/themes?msg=deleted` on success.

The set-default handler:
- 302 to `/admin/themes?error=Theme+not+found` for `ErrThemeNotFound`.
- 302 to `/admin/themes?msg=set_default` on success.

#### views/themes.templ

```go
templ themesPage(
    themes []themes.Theme,
    currentUser *auth.User,
    csrfToken string,
    msg string,
    errMsg string,
) {
    @layout("Themes - screens") {
        <div class="hero">
            <h1>Theme Management</h1>
            <p class="description"><a href="/admin/">Back to Admin</a></p>
        </div>
        if msg != "" {
            <div class="card" role="status">
                <p>{ msgText(msg) }</p>
            </div>
        }
        if errMsg != "" {
            <div class="card" role="alert">
                <p>{ errMsg }</p>
            </div>
        }
        <section>
            <div class="card">
                <h2>Themes</h2>
                <table>
                    <thead>
                        <tr>
                            <th>Name</th>
                            <th>Default</th>
                            <th>Updated</th>
                            <th>Actions</th>
                        </tr>
                    </thead>
                    <tbody>
                        for _, t := range themes {
                            <tr>
                                <td>{ t.Name }</td>
                                <td>
                                    if t.IsDefault {
                                        <strong>default</strong>
                                    } else {
                                        <form method="POST" action={ templ.SafeURL("/admin/themes/" + t.ID + "/set-default") }>
                                            <input type="hidden" name="_csrf" value={ csrfToken }/>
                                            <button type="submit">Set default</button>
                                        </form>
                                    }
                                </td>
                                <td>{ t.UpdatedAt.Format("2006-01-02 15:04 MST") }</td>
                                <td>
                                    <a href={ templ.SafeURL("/admin/themes/" + t.ID + "/edit") }>Edit</a>
                                    if !t.IsDefault {
                                        <form method="POST" action={ templ.SafeURL("/admin/themes/" + t.ID + "/delete") } style="display:inline">
                                            <input type="hidden" name="_csrf" value={ csrfToken }/>
                                            <button type="submit">Delete</button>
                                        </form>
                                    }
                                </td>
                            </tr>
                        }
                    </tbody>
                </table>
            </div>
        </section>
        <section>
            <div class="card">
                <h2>New Theme</h2>
                @themeForm("/admin/themes", themes.Input{}, nil, csrfToken)
            </div>
        </section>
    }
}

// themeForm renders the create / edit form. errs maps field names to
// validation error messages, empty when no errors.
templ themeForm(action string, in themes.Input, errs map[string]string, csrfToken string) {
    <form method="POST" action={ templ.SafeURL(action) }>
        <input type="hidden" name="_csrf" value={ csrfToken }/>
        @field("name", "Name", in.Name, errs["name"])
        @field("color_bg", "Background color (hex)", in.ColorBg, errs["color_bg"])
        @field("color_surface", "Surface color (hex)", in.ColorSurface, errs["color_surface"])
        @field("color_border", "Border color (hex)", in.ColorBorder, errs["color_border"])
        @field("color_text", "Text color (hex)", in.ColorText, errs["color_text"])
        @field("color_text_muted", "Muted text color (hex)", in.ColorTextMuted, errs["color_text_muted"])
        @field("color_accent", "Accent color (hex)", in.ColorAccent, errs["color_accent"])
        @field("font_family", "Font family", in.FontFamily, errs["font_family"])
        @field("radius", "Border radius (e.g. 10px)", in.Radius, errs["radius"])
        <button type="submit">Save</button>
    </form>
}

templ field(name, label, value, errMsg string) {
    <label>{ label }
        <input type="text" name={ name } value={ value } required/>
    </label>
    if errMsg != "" {
        <p class="error">{ errMsg }</p>
    }
}
```

The edit page is its own templ (`themeEditPage`) that renders only the form (re-using `themeForm`) plus a "back to themes" link, so the URL `/admin/themes/{id}/edit` is a focused screen.

`msgText` is a small helper in `views/themes.go` mapping flash codes (`created`, `updated`, `deleted`, `set_default`) to user-visible messages, mirroring the existing pattern in `handleDeviceList` and `handleUserList`.

### Dependencies Between Components

```
main.go
  config.Load()                     -- ThemeConfig.DefaultName (NEW; lives on AuthConfig
                                       per the existing pattern, OR a new ThemeConfig
                                       sub-struct -- implementer's choice. Keeping it on
                                       AuthConfig is acceptable for v1; promoting it to a
                                       ThemeConfig sub-struct is also acceptable. Either
                                       way the field is single-source.)
  db.Open(...) / db.Migrate(...)    -- existing
  themesSvc := themes.NewService(...)
  themesSvc.EnsureDefault(ctx)      -- runs once per process boot; idempotent
  views.AddRoutes(mux, &views.Deps{
      ...,
      Themes: themesSvc,            -- NEW field on Deps
  })
```

`views.Deps` gains a `Themes *themes.Service` field. The new theme handlers receive it through the existing `deps` parameter just like devices receive `deps.Auth`.

### main.go Wiring Changes

```go
themesSvc := themes.NewService(sqlDB, themes.Config{
    DefaultName: cfg.Theme.DefaultName, // OR cfg.Auth.ThemeDefaultName -- see config note
})
if err := themesSvc.EnsureDefault(context.Background()); err != nil {
    db.Close(sqlDB)
    log.Fatalf("seed default theme: %v", err)
}

views.AddRoutes(mux, &views.Deps{
    Auth:             authSvc,
    Google:           googleClient,
    ClientID:         cfg.Auth.GoogleClientID,
    CookieName:       cfg.Auth.CookieName,
    DeviceCookieName: cfg.Auth.DeviceCookieName,
    DeviceLandingURL: cfg.Auth.DeviceLandingURL,
    SecureCookie:     !cfg.Log.DevMode,
    Themes:           themesSvc,        // NEW
})
```

The fail-on-error semantics for `EnsureDefault` mirrors the existing fail-on-error for `db.Migrate`: a startup that cannot establish the bare-minimum schema is a hard error.

### views/routes.go Wiring Changes

A new `themeMux` is registered the same way `deviceMux` is, inside the admin-only chain:

```go
// Theme management routes require admin role.
themeMux := http.NewServeMux()
themeMux.HandleFunc("GET  /admin/themes",                     handleThemeList(deps.Themes))
themeMux.HandleFunc("POST /admin/themes",                     handleThemeCreate(deps.Themes))
themeMux.HandleFunc("GET  /admin/themes/{id}/edit",           handleThemeEditForm(deps.Themes))
themeMux.HandleFunc("POST /admin/themes/{id}",                handleThemeUpdate(deps.Themes))
themeMux.HandleFunc("POST /admin/themes/{id}/delete",         handleThemeDelete(deps.Themes))
themeMux.HandleFunc("POST /admin/themes/{id}/set-default",    handleThemeSetDefault(deps.Themes))

adminMux.Handle("/admin/themes",  middleware.RequireRole(auth.RoleAdmin)(themeMux))
adminMux.Handle("/admin/themes/", middleware.RequireRole(auth.RoleAdmin)(themeMux))
```

The existing `RequireAuth` -> `RequireCSRF` chain wrapping `adminMux` covers theme routes as a side effect; no new outer wrapping is needed.

The admin landing page (`views/admin.templ`) gets one new line linking to `/admin/themes`, mirroring the existing links to `/admin/users` and `/admin/devices`.

## Storage

### sqlc Queries (internal/db/queries/themes.sql)

```sql
-- name: CreateTheme :exec
INSERT INTO themes (
    id, name, is_default,
    color_bg, color_surface, color_border, color_text, color_text_muted, color_accent,
    font_family, font_family_mono, radius
) VALUES (
    ?, ?, ?,
    ?, ?, ?, ?, ?, ?,
    ?, ?, ?
);

-- name: GetThemeByID :one
SELECT id, name, is_default, color_bg, color_surface, color_border, color_text, color_text_muted, color_accent, font_family, font_family_mono, radius, created_at, updated_at
FROM themes
WHERE id = ?;

-- name: GetThemeByName :one
SELECT id, name, is_default, color_bg, color_surface, color_border, color_text, color_text_muted, color_accent, font_family, font_family_mono, radius, created_at, updated_at
FROM themes
WHERE name = ?;

-- name: GetDefaultTheme :one
SELECT id, name, is_default, color_bg, color_surface, color_border, color_text, color_text_muted, color_accent, font_family, font_family_mono, radius, created_at, updated_at
FROM themes
WHERE is_default = 1
LIMIT 1;

-- name: ListThemes :many
SELECT id, name, is_default, color_bg, color_surface, color_border, color_text, color_text_muted, color_accent, font_family, font_family_mono, radius, created_at, updated_at
FROM themes
ORDER BY name;

-- name: UpdateTheme :exec
UPDATE themes
   SET name = ?,
       color_bg = ?, color_surface = ?, color_border = ?,
       color_text = ?, color_text_muted = ?, color_accent = ?,
       font_family = ?, font_family_mono = ?, radius = ?,
       updated_at = datetime('now')
 WHERE id = ?;

-- name: DeleteTheme :execresult
DELETE FROM themes WHERE id = ? AND is_default = 0;

-- name: ClearDefaultTheme :exec
UPDATE themes SET is_default = 0 WHERE is_default = 1;

-- name: SetDefaultTheme :execresult
UPDATE themes SET is_default = 1 WHERE id = ?;

-- name: CountDefaultThemes :one
SELECT COUNT(*) FROM themes WHERE is_default = 1;
```

`DeleteTheme :execresult` returns the result so the service can distinguish "row was deleted" (rowsAffected = 1) from "row exists but is the default" (rowsAffected = 0; the WHERE clause's `is_default = 0` prevented the delete). The service then double-checks via `GetThemeByID` to disambiguate "not found" from "is default".

`SetDefaultTheme :execresult` is paired with `ClearDefaultTheme :exec` in a single transaction inside `Service.SetDefault`:

```go
tx, err := s.sqlDB.BeginTx(ctx, nil)
if err != nil { ... }
defer tx.Rollback()
qtx := s.queries.WithTx(tx)
if err := qtx.ClearDefaultTheme(ctx); err != nil { ... }
res, err := qtx.SetDefaultTheme(ctx, id)
if err != nil { ... }
n, _ := res.RowsAffected()
if n == 0 { return ErrThemeNotFound }
return tx.Commit()
```

The transaction guarantees atomicity: either both rows update or neither does. The partial unique index `themes_one_default` belt-and-suspenders the invariant: if the transaction were ever interrupted in a way that left two `is_default = 1` rows, the index would have failed the second UPDATE first.

`EnsureDefault` checks `CountDefaultThemes`; if the count is zero, inserts a hard-coded default theme. The hard-coded values match the constants in `static/css/app.css`. The check + insert pair is wrapped in a transaction to avoid a race during simultaneous boots.

### Migration Numbering

Existing migrations: `001` through `005`. This spec adds `006_create-themes.sql`. Numbering remains monotonic and unique.

### sqlc Generation

After adding `themes.sql`, the implementer runs `sqlc generate`. The generator produces `internal/db/themes.sql.go` with method signatures matching the queries above, plus a `Theme` struct in `internal/db/models.go` with this shape:

```go
type Theme struct {
    ID             string
    Name           string
    IsDefault      int64
    ColorBg        string
    ColorSurface   string
    ColorBorder    string
    ColorText      string
    ColorTextMuted string
    ColorAccent    string
    FontFamily     string
    FontFamilyMono string
    Radius         string
    CreatedAt      string
    UpdatedAt      string
}
```

`themeFromRow` in `internal/themes/theme.go` translates `IsDefault int64` to `bool` and the timestamp strings to `time.Time` using the existing `2006-01-02 15:04:05` parse format.

## Configuration

A single new env-driven setting, parsed in `internal/config/config.go`:

```go
// Option A: extend the existing AuthConfig (ThemeDefaultName field).
// Option B: introduce a new ThemeConfig sub-struct.

// The architecture document recommends Option B (cleaner domain boundary):
type ThemeConfig struct {
    DefaultName string
}

type Config struct {
    HTTP  HTTPConfig
    Log   LogConfig
    DB    DBConfig
    Auth  AuthConfig
    Theme ThemeConfig // NEW
}
```

Parsing in `Load()`:

```go
Theme: ThemeConfig{
    DefaultName: env("THEME_DEFAULT_NAME", "default"),
},
```

Validation in `Validate()`:

```go
if c.Theme.DefaultName == "" {
    errs = append(errs, "THEME_DEFAULT_NAME must not be empty")
}
```

`Config.String()` includes `Theme{DefaultName:...}` -- not a secret, no redaction needed.

The README's configuration table gains one row.

## Security Considerations

### CSS Injection Defence

Theme field values flow into a `<style>` block on every device-rendered page. The standard HTML escape does not apply inside `<style>`, so the only thing keeping malicious CSS out of the response is the validators in `internal/themes/validate.go`. The validators are whitelist-based:

- Hex colors match `^#([0-9A-Fa-f]{3}|[0-9A-Fa-f]{6})$`. The character set is `[0-9A-Fa-f#]`, all of which are safe in a CSS declaration.
- Radius matches `^(0|[0-9]+px|[0-9]+(\.[0-9]+)?(rem|em))$`. The character set is `[0-9.pxremz]`.
- Font family rejects `;`, `{`, `}`, `<`, `>`, backslash, and any control / newline character. It does NOT enforce a positive-character whitelist because real font names contain commas, spaces, hyphens, apostrophes, and double quotes (e.g., `"SF Mono", "Fira Code", monospace`). The blacklist is sufficient because the dangerous characters are exactly the ones we reject.
- Name matches `^[A-Za-z0-9 _-]{1,64}$`. This is a positive whitelist; no other characters are accepted.

The Theme struct's `CSSVariables()` method writes only validated-and-normalised values, so the output cannot contain anything that would close the `<style>` element or terminate the declaration.

### Authentication and CSRF

All admin routes sit behind the existing chain. There is no public theme endpoint in this spec; downstream Screen Display will read themes from the database as part of an authenticated render path, not as a separate public endpoint.

### Token / Secret Handling

Theme rows contain no secrets. The `Config.String()` change does not need a redaction. No new logging that could leak sensitive data is introduced.

### Default Theme Invariant

The seed step + the partial unique index + the transactional `SetDefault` together make "zero defaults" or "two defaults" impossible to reach in production:

- Boot path: migrations create the table, `EnsureDefault` inserts the default row if missing.
- Update path: `SetDefault` runs `UPDATE...is_default=0` and `UPDATE...WHERE id=?...is_default=1` in a single transaction; the partial unique index would reject the second UPDATE if the first failed.
- Delete path: `Service.Delete` returns `ErrCannotDeleteDefault` before issuing the SQL; the `WHERE is_default=0` clause on the SQL is the second line of defence.

### Fail-Closed Behaviour

A database error in `EnsureDefault` halts startup with a clear log line. `Service.GetDefault` returning `ErrThemeNotFound` is a startup-invariant violation; downstream Screen Display logs and falls back to the static stylesheet defaults rather than serving unstyled content.

## Task Breakdown

This architecture decomposes into the following tasks. Numbering continues from TASK-015 (the last task in Phase 1).

1. **TASK-016**: Theme config, migration, and sqlc queries -- (prerequisite: none).
2. **TASK-017**: Theme service (`internal/themes/`) with CRUD methods, default-seeding, and the `CSSVariables()` rendering helper -- (prerequisite: TASK-016).
3. **TASK-018**: Theme admin views (list, create, edit, update, delete, set-default), templ components, route wiring, and main.go integration -- (prerequisite: TASK-017).

### Task Dependency Graph

```
TASK-016 (config + migration + sqlc queries)
    |
    v
TASK-017 (themes.Service: CRUD + EnsureDefault + Theme.CSSVariables())
    |
    v
TASK-018 (admin views + route wiring + main.go integration)
```

The dependency chain is strictly linear because each task depends on the previous task's exported surface area:
- TASK-016 produces the schema, sqlc-generated `db.Theme` struct, and the `THEME_DEFAULT_NAME` config field.
- TASK-017 produces `themes.Service`, the validation logic, and the `Theme.CSSVariables()` helper -- everything the views (TASK-018) and downstream Screen Display will call.
- TASK-018 wires the admin UI on top of TASK-017 and finishes the feature.

The split keeps each task focused: TASK-016 is pure data layer, TASK-017 is pure domain logic plus tests for both validation and CSS rendering, TASK-018 is pure HTTP / template work. Each task is reviewable in isolation.

## Alternatives Considered

See ADR-004 for the full design rationale. Architectural alternatives evaluated during this design pass:

- **Storing themes in static files**: rejected. Forces a redeploy for every color tweak; loses parity with how every other admin-managed entity (users, devices, invitations) is handled.
- **Dynamic CSS endpoint (`GET /themes/{id}.css`)**: rejected. Adds a second round-trip on every page render and a flash of unstyled content while the stylesheet loads. A wall display reloads on every page rotation; an extra request per rotation is wasteful and introduces ordering issues.
- **Permissive CSS color parser**: rejected. Real CSS color syntax has grown to include `oklch()`, `color-mix()`, named colors, hex with alpha, and more. A permissive parser is a large attack surface; strict hex is one regex and is provably safe to embed in a `<style>` block. New color forms can be added in additive fields when there is a use case.
- **Bundling themes as a `<body class="theme-X">` + a static stylesheet that ships every theme**: rejected. Pre-bakes every theme into the binary and requires a recompile to add or change one. Loses the "edit through the admin UI" property.
- **Putting themes in `internal/auth/` alongside users / devices**: rejected. Themes are not an authentication concern. The existing `auth.Service` already handles two domains; adding a third would cement an "auth.go is the dumping ground" pattern. A new `internal/themes/` package keeps the boundary clean.
- **Storing colors as structured RGB triples (three integer columns per color)**: rejected. Triples the column count, complicates the admin UI (three inputs per color), and forces a custom serialisation format. Hex is the lowest-friction format for both human admins (most palette pickers output hex) and machines (deterministic equality and easy validation).
- **Using a `system_settings` table to store the default theme ID**: rejected. Adds a second table for one column and forces every theme read to JOIN. The per-row `is_default` boolean reuses the same shape as `users.active`.
- **Skipping the partial unique index and relying only on transactional `SetDefault`**: rejected. The index is a single line of SQL and turns a class of bugs (two defaults at once due to a missed transaction) into a hard SQL error rather than silent data corruption. SQLite supports partial indexes natively; the cost is zero.
- **Skipping the seed step and letting Screen Display handle "no theme" gracefully**: rejected. Every downstream caller would have to handle the empty case. Seeding once at startup means Screen Display can `themesSvc.GetDefault(ctx)` without checking for `ErrThemeNotFound`.
- **Versioning themes (each edit creates a new revision row)**: rejected. Triples the storage cost for a low-volume entity, complicates the admin UI ("which version is active?"), and addresses a problem we do not have. If an admin wants to keep the old palette, they duplicate the theme before editing.
- **Surfacing `font_family_mono` as a separate admin input in v1**: deferred. The default theme seeds it; the admin form does not expose it. A later spec adds the input when widgets actually consume it. Carrying the column in the schema from day one is cheaper than a future migration.
- **Live-preview rendering (the admin types and sees colors apply immediately)**: out of scope here. Spec'd separately as Theme Preview (p1). This spec ships the CSS-rendering helper that the live-preview spec will reuse.
