---
id: ARCH-006
title: "Screen Model"
spec: SPEC-006
status: draft
created: 2026-05-13
author: architect
---

# Screen Model Architecture

## Overview

Screen Model adds the dashboard data model that Phase 2 has been building up to. Three new tables -- `screens`, `pages`, `widget_instances` -- live in the existing SQLite database alongside `themes`, `widget_instances`'s FK chain (widget instance → page → screen) is enforced at the DB layer (CASCADE for child cleanup, RESTRICT for theme references), the per-instance config blob is validated through `widget.Default().Validate` before INSERT, and a new `internal/screens/` package exposes a typed service that the admin UI and Screen Display will consume.

The admin UI follows the established `/admin/<entity>` pattern with two new templ pages (`/admin/screens` list and `/admin/screens/{id}/edit`) plus a per-page edit pane (`/admin/screens/{id}/pages/{pageID}/edit`). Eleven new admin routes wire the CRUD: screens × 5, pages × 5, widget instances × 4. All routes inherit the existing `RequireAuth` → `RequireRole(RoleAdmin)` → `RequireCSRF` middleware chain.

One Theme-System change ships alongside: `themes.Service.Delete` learns a new error case (`ErrThemeInUse`) when SQLite raises the FK violation from a RESTRICT-protected `screens.theme_id`. The Theme admin handler translates that to a user-visible flash. No other existing code paths change.

## References

- Spec: `docs/plans/specs/phase-2-display/spec-screen-model.md`
- Related ADRs: ADR-006 (1-D layout, RESTRICT theme FK, CASCADE child deletes), ADR-001 (Storage Engine), ADR-004 (Theme System), ADR-005 (Widget Interface)
- Prerequisite architecture: ARCH-001 (Storage Engine), ARCH-002 (Admin Auth), ARCH-004 (Theme System), ARCH-005 (Widget Interface)

## Data Model

### Database Schema

```sql
-- 007_create-screens.sql
-- +up
CREATE TABLE IF NOT EXISTS screens (
    id                         TEXT PRIMARY KEY,
    name                       TEXT NOT NULL UNIQUE,
    theme_id                   TEXT NOT NULL REFERENCES themes(id) ON DELETE RESTRICT,
    rotation_interval_seconds  INTEGER NOT NULL DEFAULT 30,
    created_at                 TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at                 TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_screens_theme_id ON screens(theme_id);

-- +down
DROP INDEX IF EXISTS idx_screens_theme_id;
DROP TABLE IF EXISTS screens;
```

```sql
-- 008_create-pages.sql
-- +up
CREATE TABLE IF NOT EXISTS pages (
    id          TEXT PRIMARY KEY,
    screen_id   TEXT NOT NULL REFERENCES screens(id) ON DELETE CASCADE,
    name        TEXT NOT NULL DEFAULT '',
    position    INTEGER NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX idx_pages_screen_position ON pages(screen_id, position);
CREATE INDEX idx_pages_screen_id ON pages(screen_id);

-- +down
DROP INDEX IF EXISTS idx_pages_screen_id;
DROP INDEX IF EXISTS idx_pages_screen_position;
DROP TABLE IF EXISTS pages;
```

```sql
-- 009_create-widget-instances.sql
-- +up
CREATE TABLE IF NOT EXISTS widget_instances (
    id          TEXT PRIMARY KEY,
    page_id     TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    type        TEXT NOT NULL,
    config      TEXT NOT NULL,
    position    INTEGER NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX idx_widget_instances_page_position ON widget_instances(page_id, position);
CREATE INDEX idx_widget_instances_page_id ON widget_instances(page_id);

-- +down
DROP INDEX IF EXISTS idx_widget_instances_page_id;
DROP INDEX IF EXISTS idx_widget_instances_page_position;
DROP TABLE IF EXISTS widget_instances;
```

Notes on the schema:

- All IDs are 32-char hex (16 bytes of entropy), generated via `auth.GenerateToken[:32]`. Matches the existing pattern in `themes`, `devices`, `users`, `sessions`, `invitations`.
- `screens.theme_id` is `NOT NULL` -- every Screen MUST have a theme. The Theme System seeds a default theme at startup, so the admin always has at least one valid theme ID to assign.
- `screens.theme_id` is `ON DELETE RESTRICT` -- attempts to delete a referenced theme fail at the DB layer. The Theme System's `Service.Delete` is extended to detect this and return `ErrThemeInUse`.
- `pages.screen_id` and `widget_instances.page_id` are `ON DELETE CASCADE` -- atomic child cleanup at the DB layer.
- `screens.name` is `UNIQUE` -- mirror the `themes.name` constraint.
- `pages.name` is NOT unique; the admin may have empty or duplicate page names within a screen (page identity is by ID).
- `(screen_id, position)` on `pages` and `(page_id, position)` on `widget_instances` are `UNIQUE` -- enforces "no two siblings share a position" at the DB layer. Reorder operations use a temporary-negative-position swap trick (see "Storage > Reorder Implementation" below) to avoid violating the constraint between the two UPDATEs in a swap.
- All timestamps follow the project's TEXT-as-ISO8601 convention.
- `widget_instances.config` is TEXT (JSON bytes). The application validates with `widget.Default().Validate` before INSERT/UPDATE; the DB column type is opaque.
- The FK behaviour requires `PRAGMA foreign_keys = ON` on every SQLite connection. The existing `db.Open` already sets this; we assume it.

### Go Domain Types

```go
// internal/screens/screen.go
package screens

import "time"

// Screen is the domain type returned by the service. Validation and
// normalisation already ran by the time a Screen reaches a caller.
type Screen struct {
    ID                      string
    Name                    string
    ThemeID                 string
    RotationIntervalSeconds int
    CreatedAt               time.Time
    UpdatedAt               time.Time
}

// ScreenSummary is the list-page row shape: a Screen plus enough joined
// metadata (theme name and page count) that the list template doesn't
// need to issue extra queries.
type ScreenSummary struct {
    Screen
    ThemeName string
    PageCount int
}

// Page is a single page within a screen.
type Page struct {
    ID        string
    ScreenID  string
    Name      string // may be empty
    Position  int    // 1-indexed
    CreatedAt time.Time
    UpdatedAt time.Time
}

// WidgetInstance is one placement of a registered widget type on a page.
// Config is the validated raw JSON bytes (kept as []byte so callers can
// either re-parse or hand to widget.Registry.Render directly).
type WidgetInstance struct {
    ID        string
    PageID    string
    Type      string
    Config    []byte // validated JSON; safe to feed into widget.Registry.Render
    Position  int    // 1-indexed
    CreatedAt time.Time
    UpdatedAt time.Time
}

// ScreenFull is the fully-hydrated tree Screen Display will render. Pages
// and widget instances are pre-fetched in position order; the theme is
// looked up once via themes.Service.GetByID and copied in by value.
type ScreenFull struct {
    Screen Screen
    Theme  themes.Theme
    Pages  []PageWithWidgets
}

type PageWithWidgets struct {
    Page    Page
    Widgets []WidgetInstance
}

// ScreenInput collects the user-supplied fields needed to create or
// update a Screen. The service validates and normalises this struct.
type ScreenInput struct {
    Name                    string
    ThemeID                 string
    RotationIntervalSeconds int
}
```

The `ScreenFull` shape is the contract Screen Display will consume. It is deliberately denormalised so the renderer never needs to call back into a service.

### Theme System changes (additive)

```go
// internal/themes/service.go (existing file, additive change)

// ErrThemeInUse is returned by Delete when the target theme is referenced
// by at least one row in a foreign-key constrained table (currently:
// screens.theme_id). The admin UI surfaces this as
// "Cannot delete a theme in use by a screen".
var ErrThemeInUse = errors.New("theme in use by one or more screens")

// (Delete is modified to detect the FK violation and return ErrThemeInUse.
//  See "Storage > Theme.Delete change" below for the exact code shape.)
```

No other Theme System changes.

## API Contract

### Endpoints

| Method | Path                                                              | Request Body                                                 | Response                                          | Auth  |
|--------|-------------------------------------------------------------------|--------------------------------------------------------------|---------------------------------------------------|-------|
| GET    | `/admin/screens`                                                  | -                                                            | HTML screen list                                  | admin |
| POST   | `/admin/screens`                                                  | form: `name`, `theme_id`, `rotation_interval_seconds`, `_csrf` | 302 → `/admin/screens?msg=created`                | admin |
| GET    | `/admin/screens/{id}/edit`                                        | -                                                            | HTML screen edit (with page list)                 | admin |
| POST   | `/admin/screens/{id}`                                             | same as create                                               | 302 → `/admin/screens?msg=updated`                | admin |
| POST   | `/admin/screens/{id}/delete`                                      | `_csrf`                                                      | 302 → `/admin/screens?msg=deleted`                | admin |
| POST   | `/admin/screens/{id}/pages`                                       | `name`, `_csrf`                                              | 302 → `/admin/screens/{id}/edit?msg=page_created` | admin |
| GET    | `/admin/screens/{id}/pages/{pageID}/edit`                         | -                                                            | HTML page edit (with widget instance list)         | admin |
| POST   | `/admin/screens/{id}/pages/{pageID}`                              | `name`, `_csrf`                                              | 302 → `/admin/screens/{id}/edit?msg=page_updated` | admin |
| POST   | `/admin/screens/{id}/pages/{pageID}/delete`                       | `_csrf`                                                      | 302 → `/admin/screens/{id}/edit?msg=page_deleted` | admin |
| POST   | `/admin/screens/{id}/pages/{pageID}/move-up`                      | `_csrf`                                                      | 302 → `/admin/screens/{id}/edit?msg=page_moved`   | admin |
| POST   | `/admin/screens/{id}/pages/{pageID}/move-down`                    | `_csrf`                                                      | 302 → `/admin/screens/{id}/edit?msg=page_moved`   | admin |
| POST   | `/admin/screens/{id}/pages/{pageID}/widgets`                      | `type`, `_csrf`                                              | 302 → `/admin/screens/{id}/pages/{pageID}/edit?msg=widget_added` | admin |
| POST   | `/admin/screens/{id}/pages/{pageID}/widgets/{widgetID}/delete`    | `_csrf`                                                      | 302 → `/admin/screens/{id}/pages/{pageID}/edit?msg=widget_deleted` | admin |
| POST   | `/admin/screens/{id}/pages/{pageID}/widgets/{widgetID}/move-up`   | `_csrf`                                                      | 302 → `/admin/screens/{id}/pages/{pageID}/edit?msg=widget_moved` | admin |
| POST   | `/admin/screens/{id}/pages/{pageID}/widgets/{widgetID}/move-down` | `_csrf`                                                      | 302 → `/admin/screens/{id}/pages/{pageID}/edit?msg=widget_moved` | admin |

All endpoints sit behind the existing `RequireAuth` → `RequireRole(RoleAdmin)` → `RequireCSRF` chain wrapping `adminMux`. Device identities receive 403 from `RequireRole`.

### Validation errors

Per ADR-006 / SPEC-006 R30, validation errors are surfaced via the `?error=...` flash pattern rather than re-rendering the form inline. Screens / pages / widgets have few input fields; the flash pattern is plenty.

### Request/Response examples

A successful create:

```http
POST /admin/screens HTTP/1.1
Content-Type: application/x-www-form-urlencoded

name=kitchen&theme_id=abcdef1234567890abcdef1234567890&rotation_interval_seconds=30&_csrf=...

HTTP/1.1 302 Found
Location: /admin/screens?msg=created
```

An invalid create:

```http
POST /admin/screens HTTP/1.1
Content-Type: application/x-www-form-urlencoded

name=&theme_id=abcdef1234567890abcdef1234567890&rotation_interval_seconds=30&_csrf=...

HTTP/1.1 302 Found
Location: /admin/screens?error=Name+is+required
```

A theme-delete that fails the RESTRICT check:

```http
POST /admin/themes/abc123/delete HTTP/1.1
Content-Type: application/x-www-form-urlencoded

_csrf=...

HTTP/1.1 302 Found
Location: /admin/themes?error=Cannot+delete+a+theme+in+use+by+a+screen
```

The `GetScreenFull` Go call (consumed by Screen Display in a future spec):

```go
full, err := screensSvc.GetScreenFull(ctx, screenID)
// full.Screen, full.Theme, full.Pages[i].Page, full.Pages[i].Widgets[j]
```

## Component Design

### Package Layout

```
internal/
  screens/
    screen.go               -- NEW: Screen, Page, WidgetInstance, ScreenFull, ScreenInput types
    service.go              -- NEW: Service struct, NewService, all CRUD + reorder + GetScreenFull
    validate.go             -- NEW: validateScreenInput, name regex (reused with themes regex if reasonable)
    service_test.go         -- NEW: service-level tests (CRUD, reorder, cascades, GetScreenFull)
    adversarial_test.go     -- NEW: edge cases (race-y reorder, FK violations, unknown widget type)
  themes/
    service.go              -- MODIFY: add ErrThemeInUse; Delete detects FK violation and returns it
    service_test.go         -- MODIFY: add ErrThemeInUse-rejection test (uses a screen row to trigger)
  db/
    migrations/
      007_create-screens.sql           -- NEW
      008_create-pages.sql             -- NEW
      009_create-widget-instances.sql  -- NEW
    queries/
      screens.sql                      -- NEW
      pages.sql                        -- NEW
      widget_instances.sql             -- NEW
    screens.sql.go                     -- NEW (sqlc-generated)
    pages.sql.go                       -- NEW (sqlc-generated)
    widget_instances.sql.go            -- NEW (sqlc-generated)
    models.go                          -- MODIFY: gains Screen, Page, WidgetInstance structs from sqlc
views/
  screens.go                -- NEW: handlers for /admin/screens, page/widget management
  screens.templ             -- NEW: list + edit + page edit pages
  routes.go                 -- MODIFY: register /admin/screens routes inside admin sub-mux; add Screens *screens.Service to Deps
  admin.templ               -- MODIFY: add /admin/screens link
  screens_test.go           -- NEW: handler tests
  screens_adversarial_test.go -- NEW: handler edge cases
main.go                     -- MODIFY: construct screens.Service and pass into Deps
```

The split between `internal/screens/` (domain + service) and `views/screens.go` (HTTP handlers + templ) mirrors the established theme pattern.

### Key Interfaces and Functions

#### internal/screens/service.go

```go
package screens

import (
    "context"
    "database/sql"
    "errors"

    "github.com/jasoncorbett/screens/internal/db"
    "github.com/jasoncorbett/screens/internal/themes"
    "github.com/jasoncorbett/screens/internal/widget"
)

// ErrScreenNotFound is returned when an operation targets a screen id that
// does not exist.
var ErrScreenNotFound = errors.New("screen not found")

// ErrPageNotFound is returned when a page id does not exist (or does not
// belong to the named screen).
var ErrPageNotFound = errors.New("page not found")

// ErrWidgetNotFound is returned when a widget instance id does not exist
// (or does not belong to the named page).
var ErrWidgetNotFound = errors.New("widget instance not found")

// ErrUnknownWidgetType is returned by AddWidget when the requested type
// has no registration in the widget registry.
var ErrUnknownWidgetType = errors.New("unknown widget type")

// ErrThemeNotFound is returned by Create/Update when the requested
// theme_id has no row in the themes table. (Distinct from the Theme
// System's own ErrThemeNotFound to keep package boundaries clean; the
// admin UI translates both to a "theme not found" error message.)
var ErrThemeNotFound = errors.New("theme not found")

// ErrDuplicateName is returned when create / update would produce a
// duplicate Screen name.
var ErrDuplicateName = errors.New("screen name already in use")

// Service orchestrates screen / page / widget-instance operations.
type Service struct {
    sqlDB   *sql.DB
    queries *db.Queries
    themes  *themes.Service
    widgets *widget.Registry
}

// NewService constructs a Service. The themes service and widget
// registry are explicit dependencies because the screens service must
// validate theme references (via themes.Service.GetByID) and widget
// types (via widgets.Get) before persisting.
func NewService(sqlDB *sql.DB, themesSvc *themes.Service, widgets *widget.Registry) *Service {
    return &Service{
        sqlDB:   sqlDB,
        queries: db.New(sqlDB),
        themes:  themesSvc,
        widgets: widgets,
    }
}

// CreateScreen validates input and inserts a new Screen row. Returns
// *ValidationError on input failures, ErrDuplicateName on name
// collisions, ErrThemeNotFound if the theme_id does not exist.
func (s *Service) CreateScreen(ctx context.Context, in ScreenInput) (Screen, error) { /* ... */ }

// GetScreenByID returns a Screen by ID. Returns ErrScreenNotFound when
// no row matches.
func (s *Service) GetScreenByID(ctx context.Context, id string) (Screen, error) { /* ... */ }

// ListScreens returns every screen as a summary (with theme name + page
// count) ordered by name.
func (s *Service) ListScreens(ctx context.Context) ([]ScreenSummary, error) { /* ... */ }

// UpdateScreen mutates an existing Screen. Validation matches Create.
// Returns ErrScreenNotFound, ErrDuplicateName, or ErrThemeNotFound as
// appropriate.
func (s *Service) UpdateScreen(ctx context.Context, id string, in ScreenInput) (Screen, error) { /* ... */ }

// DeleteScreen removes a Screen and (via CASCADE) its pages and widget
// instances. Returns ErrScreenNotFound when no row matches.
func (s *Service) DeleteScreen(ctx context.Context, id string) error { /* ... */ }

// CreatePage creates a new page on the named Screen with auto-assigned
// position (= max+1 within the screen, or 1 if no pages exist).
func (s *Service) CreatePage(ctx context.Context, screenID, name string) (Page, error) { /* ... */ }

// GetPageByID returns a page by id. The screenID parameter is required
// for defence-in-depth: the URL contains the screen ID, and the service
// rejects a (screen, page) pair that does not match. Returns
// ErrPageNotFound on a missing or mismatched row.
func (s *Service) GetPageByID(ctx context.Context, screenID, pageID string) (Page, error) { /* ... */ }

// UpdatePage mutates an existing page's name.
func (s *Service) UpdatePage(ctx context.Context, screenID, pageID, name string) (Page, error) { /* ... */ }

// DeletePage removes a page and (via CASCADE) its widget instances.
// Returns ErrPageNotFound when no row matches.
func (s *Service) DeletePage(ctx context.Context, screenID, pageID string) error { /* ... */ }

// MovePageUp swaps the given page with the one at position-1 within the
// same screen. Returns nil (no-op) if the page is already at position 1.
// Returns ErrPageNotFound when no row matches.
func (s *Service) MovePageUp(ctx context.Context, screenID, pageID string) error { /* ... */ }

// MovePageDown swaps the given page with the one at position+1.
// Returns nil (no-op) if the page is already at the bottom.
func (s *Service) MovePageDown(ctx context.Context, screenID, pageID string) error { /* ... */ }

// AddWidget creates a widget instance on the named page using the
// widget type's default config. Returns ErrUnknownWidgetType if the
// type is not registered, ErrPageNotFound if the page is missing.
// The widget type's DefaultConfig() is round-tripped through
// ValidateConfig before persistence to enforce the "default must
// validate" property at write time.
func (s *Service) AddWidget(ctx context.Context, screenID, pageID, widgetType string) (WidgetInstance, error) { /* ... */ }

// DeleteWidget removes a widget instance. Returns ErrWidgetNotFound
// when no row matches.
func (s *Service) DeleteWidget(ctx context.Context, screenID, pageID, widgetID string) error { /* ... */ }

// MoveWidgetUp swaps the given widget instance with the one at
// position-1 within the same page. No-op at position 1.
func (s *Service) MoveWidgetUp(ctx context.Context, screenID, pageID, widgetID string) error { /* ... */ }

// MoveWidgetDown swaps the given widget instance with the one at
// position+1. No-op at the bottom.
func (s *Service) MoveWidgetDown(ctx context.Context, screenID, pageID, widgetID string) error { /* ... */ }

// GetScreenFull returns the screen, its theme, its pages in order, and
// each page's widget instances in order. Used by Screen Display.
// Implementation: one query for the screen (joined with themes), one for
// the pages, one for the widget instances filtered by page IDs.
// Returns ErrScreenNotFound when the screen id has no row.
func (s *Service) GetScreenFull(ctx context.Context, id string) (ScreenFull, error) { /* ... */ }
```

#### internal/screens/validate.go

```go
package screens

import (
    "errors"
    "fmt"
    "regexp"
    "sort"
    "strings"
)

// ValidationError carries per-field validation messages. Mirrors the
// themes.ValidationError shape.
type ValidationError struct {
    Fields map[string]string
}

func (v *ValidationError) Error() string {
    keys := make([]string, 0, len(v.Fields))
    for k := range v.Fields {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    parts := make([]string, 0, len(keys))
    for _, k := range keys {
        parts = append(parts, fmt.Sprintf("%s: %s", k, v.Fields[k]))
    }
    return "screen validation failed: " + strings.Join(parts, "; ")
}

func IsValidationError(err error) bool {
    var ve *ValidationError
    return errors.As(err, &ve)
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9 _-]{1,64}$`)

const (
    minRotationSeconds = 5
    maxRotationSeconds = 3600
)

// validateScreenInput validates and normalises the Screen create/update
// payload. Returns a non-nil *ValidationError if any field fails.
func validateScreenInput(in ScreenInput) (ScreenInput, error) {
    out := ScreenInput{
        Name:                    strings.TrimSpace(in.Name),
        ThemeID:                 strings.TrimSpace(in.ThemeID),
        RotationIntervalSeconds: in.RotationIntervalSeconds,
    }
    fields := map[string]string{}

    if !nameRe.MatchString(out.Name) {
        fields["name"] = "name must be 1-64 characters using letters, digits, spaces, hyphens, or underscores"
    }
    if out.ThemeID == "" {
        fields["theme_id"] = "theme is required"
    }
    if out.RotationIntervalSeconds < minRotationSeconds || out.RotationIntervalSeconds > maxRotationSeconds {
        fields["rotation_interval_seconds"] = fmt.Sprintf("must be between %d and %d", minRotationSeconds, maxRotationSeconds)
    }

    if len(fields) > 0 {
        return ScreenInput{}, &ValidationError{Fields: fields}
    }
    return out, nil
}

// validatePageName allows empty (used for unnamed pages) and otherwise
// applies the same regex as screen names.
func validatePageName(v string) (string, error) {
    v = strings.TrimSpace(v)
    if v == "" {
        return "", nil
    }
    if !nameRe.MatchString(v) {
        return "", fmt.Errorf("name must be 1-64 characters using letters, digits, spaces, hyphens, or underscores (or empty)")
    }
    return v, nil
}
```

#### views/screens.go

Handler factories follow the established `/admin/devices` flash pattern (no inline form re-render). Each takes `*screens.Service` (and, for the page-edit handler, also `*widget.Registry` to populate the widget-type `<select>` and `*themes.Service` for breadcrumbs). The page-edit handler's signature is wider but all are factories returning `http.HandlerFunc`.

```go
func handleScreenList(svc *screens.Service) http.HandlerFunc
func handleScreenCreate(svc *screens.Service) http.HandlerFunc
func handleScreenEditForm(svc *screens.Service, themesSvc *themes.Service) http.HandlerFunc
func handleScreenUpdate(svc *screens.Service) http.HandlerFunc
func handleScreenDelete(svc *screens.Service) http.HandlerFunc

func handlePageCreate(svc *screens.Service) http.HandlerFunc
func handlePageEditForm(svc *screens.Service, registry *widget.Registry) http.HandlerFunc
func handlePageUpdate(svc *screens.Service) http.HandlerFunc
func handlePageDelete(svc *screens.Service) http.HandlerFunc
func handlePageMoveUp(svc *screens.Service) http.HandlerFunc
func handlePageMoveDown(svc *screens.Service) http.HandlerFunc

func handleWidgetCreate(svc *screens.Service) http.HandlerFunc
func handleWidgetDelete(svc *screens.Service) http.HandlerFunc
func handleWidgetMoveUp(svc *screens.Service) http.HandlerFunc
func handleWidgetMoveDown(svc *screens.Service) http.HandlerFunc
```

A small flash-text helper maps codes to messages, mirroring `themeMsgText`.

```go
func screenMsgText(code string) string {
    switch code {
    case "created":         return "Screen created."
    case "updated":         return "Screen updated."
    case "deleted":         return "Screen deleted."
    case "page_created":    return "Page added."
    case "page_updated":    return "Page updated."
    case "page_deleted":    return "Page deleted."
    case "page_moved":      return "Page reordered."
    case "widget_added":    return "Widget added."
    case "widget_deleted":  return "Widget removed."
    case "widget_moved":    return "Widget reordered."
    default:                return ""
    }
}
```

#### views/screens.templ

```go
templ screensListPage(summaries []screens.ScreenSummary, currentUser *auth.User, csrfToken string, msg, errMsg string, themesList []themes.Theme) {
    @layout("Screens - screens") {
        // hero, status / error cards, table of screens, new-screen form
        // The new-screen form has a <select name="theme_id"> populated
        // from themesList. Mirrors devices.templ in shape.
    }
}

templ screenEditPage(screen screens.Screen, pages []screens.Page, currentUser *auth.User, csrfToken string, msg, errMsg string, themesList []themes.Theme) {
    @layout("Edit Screen - screens") {
        // hero with screen name + back link
        // section 1: edit-screen form (name, theme_id <select>, rotation_interval_seconds)
        // section 2: pages table with position, name, edit link, move up/down, delete
        // section 3: new-page form
    }
}

templ pageEditPage(screen screens.Screen, page screens.Page, widgets []screens.WidgetInstance, registrations []widget.Registration, currentUser *auth.User, csrfToken string, msg, errMsg string) {
    @layout("Edit Page - screens") {
        // hero with page label + back link
        // section 1: edit-page form (name)
        // section 2: widget instances table with position, type, config (pre-formatted JSON), move up/down, delete
        // section 3: add-widget form with <select name="type"> populated from registrations
    }
}
```

The "config JSON" column on the page-edit template is read-only, rendered inside a `<pre><code>...</code></pre>` block. Editing per-instance config lives in Widget Selection UI.

### Dependencies Between Components

```
main.go
  config.Load()                          -- existing
  db.Open(...) / db.Migrate(...)         -- migrations 007 / 008 / 009 apply on first start
  auth.NewService(...)                   -- existing
  themesSvc := themes.NewService(...)
  themesSvc.EnsureDefault(ctx)
  screensSvc := screens.NewService(sqlDB, themesSvc, widget.Default())
  views.AddRoutes(mux, &views.Deps{
      ...,
      Themes:  themesSvc,
      Widgets: widget.Default(),
      Screens: screensSvc,             -- NEW field on Deps
  })
```

`views.Deps` gains a `Screens *screens.Service` field. Handlers receive it through the existing `deps` parameter pattern.

### main.go Wiring Changes

```go
screensSvc := screens.NewService(sqlDB, themesSvc, widget.Default())

views.AddRoutes(mux, &views.Deps{
    Auth:             authSvc,
    Google:           googleClient,
    ClientID:         cfg.Auth.GoogleClientID,
    CookieName:       cfg.Auth.CookieName,
    DeviceCookieName: cfg.Auth.DeviceCookieName,
    DeviceLandingURL: cfg.Auth.DeviceLandingURL,
    SecureCookie:     !cfg.Log.DevMode,
    Themes:           themesSvc,
    Widgets:          widget.Default(),
    Screens:          screensSvc,
})
```

No new error-on-startup paths beyond construction. `EnsureDefault` is not needed for screens (no seeding).

### views/routes.go Wiring Changes

A new `screenMux` is registered inside the admin sub-mux:

```go
// Screen management routes require admin role.
screenMux := http.NewServeMux()
screenMux.HandleFunc("GET  /admin/screens",                                        handleScreenList(deps.Screens))
screenMux.HandleFunc("POST /admin/screens",                                        handleScreenCreate(deps.Screens))
screenMux.HandleFunc("GET  /admin/screens/{id}/edit",                              handleScreenEditForm(deps.Screens, deps.Themes))
screenMux.HandleFunc("POST /admin/screens/{id}",                                   handleScreenUpdate(deps.Screens))
screenMux.HandleFunc("POST /admin/screens/{id}/delete",                            handleScreenDelete(deps.Screens))
screenMux.HandleFunc("POST /admin/screens/{id}/pages",                             handlePageCreate(deps.Screens))
screenMux.HandleFunc("GET  /admin/screens/{id}/pages/{pageID}/edit",               handlePageEditForm(deps.Screens, deps.Widgets))
screenMux.HandleFunc("POST /admin/screens/{id}/pages/{pageID}",                    handlePageUpdate(deps.Screens))
screenMux.HandleFunc("POST /admin/screens/{id}/pages/{pageID}/delete",             handlePageDelete(deps.Screens))
screenMux.HandleFunc("POST /admin/screens/{id}/pages/{pageID}/move-up",            handlePageMoveUp(deps.Screens))
screenMux.HandleFunc("POST /admin/screens/{id}/pages/{pageID}/move-down",          handlePageMoveDown(deps.Screens))
screenMux.HandleFunc("POST /admin/screens/{id}/pages/{pageID}/widgets",            handleWidgetCreate(deps.Screens))
screenMux.HandleFunc("POST /admin/screens/{id}/pages/{pageID}/widgets/{widgetID}/delete",    handleWidgetDelete(deps.Screens))
screenMux.HandleFunc("POST /admin/screens/{id}/pages/{pageID}/widgets/{widgetID}/move-up",   handleWidgetMoveUp(deps.Screens))
screenMux.HandleFunc("POST /admin/screens/{id}/pages/{pageID}/widgets/{widgetID}/move-down", handleWidgetMoveDown(deps.Screens))

adminMux.Handle("/admin/screens",  middleware.RequireRole(auth.RoleAdmin)(screenMux))
adminMux.Handle("/admin/screens/", middleware.RequireRole(auth.RoleAdmin)(screenMux))
```

The existing `RequireAuth` → `RequireCSRF` chain wrapping `adminMux` covers screen routes automatically.

The admin landing page gains one new line linking to `/admin/screens`.

## Storage

### sqlc Queries

#### internal/db/queries/screens.sql

```sql
-- name: CreateScreen :exec
INSERT INTO screens (id, name, theme_id, rotation_interval_seconds)
VALUES (?, ?, ?, ?);

-- name: GetScreenByID :one
SELECT id, name, theme_id, rotation_interval_seconds, created_at, updated_at
FROM screens
WHERE id = ?;

-- name: GetScreenByName :one
SELECT id, name, theme_id, rotation_interval_seconds, created_at, updated_at
FROM screens
WHERE name = ?;

-- name: ListScreens :many
SELECT id, name, theme_id, rotation_interval_seconds, created_at, updated_at
FROM screens
ORDER BY name;

-- name: ListScreenSummaries :many
SELECT
    s.id,
    s.name,
    s.theme_id,
    s.rotation_interval_seconds,
    s.created_at,
    s.updated_at,
    t.name AS theme_name,
    (SELECT COUNT(*) FROM pages p WHERE p.screen_id = s.id) AS page_count
FROM screens s
JOIN themes t ON t.id = s.theme_id
ORDER BY s.name;

-- name: UpdateScreen :exec
UPDATE screens
   SET name = ?,
       theme_id = ?,
       rotation_interval_seconds = ?,
       updated_at = datetime('now')
 WHERE id = ?;

-- name: DeleteScreen :execresult
DELETE FROM screens WHERE id = ?;

-- name: CountScreensUsingTheme :one
SELECT COUNT(*) FROM screens WHERE theme_id = ?;
```

#### internal/db/queries/pages.sql

```sql
-- name: CreatePage :exec
INSERT INTO pages (id, screen_id, name, position)
VALUES (?, ?, ?, ?);

-- name: GetPageByID :one
SELECT id, screen_id, name, position, created_at, updated_at
FROM pages
WHERE id = ? AND screen_id = ?;

-- name: ListPagesByScreen :many
SELECT id, screen_id, name, position, created_at, updated_at
FROM pages
WHERE screen_id = ?
ORDER BY position;

-- name: MaxPagePosition :one
SELECT COALESCE(MAX(position), 0) FROM pages WHERE screen_id = ?;

-- name: UpdatePage :exec
UPDATE pages SET name = ?, updated_at = datetime('now')
WHERE id = ? AND screen_id = ?;

-- name: DeletePage :execresult
DELETE FROM pages WHERE id = ? AND screen_id = ?;

-- name: GetPageNeighbor :one
-- Returns the page immediately above or below the target. Used by the
-- swap-position reorder operation. Direction is encoded by the caller:
-- pass position-1 for "the page above" or position+1 for "the page
-- below". Returns sql.ErrNoRows when no neighbour exists (top / bottom).
SELECT id, screen_id, name, position, created_at, updated_at
FROM pages
WHERE screen_id = ? AND position = ?;

-- name: SetPagePosition :exec
UPDATE pages SET position = ?, updated_at = datetime('now')
WHERE id = ? AND screen_id = ?;
```

#### internal/db/queries/widget_instances.sql

```sql
-- name: CreateWidgetInstance :exec
INSERT INTO widget_instances (id, page_id, type, config, position)
VALUES (?, ?, ?, ?, ?);

-- name: GetWidgetInstanceByID :one
SELECT id, page_id, type, config, position, created_at, updated_at
FROM widget_instances
WHERE id = ? AND page_id = ?;

-- name: ListWidgetInstancesByPage :many
SELECT id, page_id, type, config, position, created_at, updated_at
FROM widget_instances
WHERE page_id = ?
ORDER BY position;

-- name: ListWidgetInstancesByPageIDs :many
SELECT id, page_id, type, config, position, created_at, updated_at
FROM widget_instances
WHERE page_id IN (sqlc.slice('page_ids'))
ORDER BY page_id, position;

-- name: MaxWidgetPosition :one
SELECT COALESCE(MAX(position), 0) FROM widget_instances WHERE page_id = ?;

-- name: DeleteWidgetInstance :execresult
DELETE FROM widget_instances WHERE id = ? AND page_id = ?;

-- name: GetWidgetNeighbor :one
SELECT id, page_id, type, config, position, created_at, updated_at
FROM widget_instances
WHERE page_id = ? AND position = ?;

-- name: SetWidgetPosition :exec
UPDATE widget_instances SET position = ?, updated_at = datetime('now')
WHERE id = ? AND page_id = ?;
```

### Reorder Implementation (transactional swap)

The reorder operation swaps two rows' `position` values. Naive two-UPDATE swaps violate the `UNIQUE(parent_id, position)` constraint mid-transaction (SQLite checks uniqueness per-statement). The "negative-position trick" sidesteps it:

```go
// pseudocode for MovePageDown(screenID, pageID)
tx, _ := s.sqlDB.BeginTx(ctx, nil)
defer tx.Rollback()
qtx := s.queries.WithTx(tx)

target, _ := qtx.GetPageByID(ctx, db.GetPageByIDParams{ID: pageID, ScreenID: screenID})
neighbor, err := qtx.GetPageNeighbor(ctx, db.GetPageNeighborParams{ScreenID: screenID, Position: target.Position + 1})
if errors.Is(err, sql.ErrNoRows) {
    // already at the bottom; no-op
    return tx.Commit()
}

// Step 1: park the target's position outside the valid range
qtx.SetPagePosition(ctx, db.SetPagePositionParams{Position: -target.Position, ID: target.ID, ScreenID: screenID})
// Step 2: move the neighbor up
qtx.SetPagePosition(ctx, db.SetPagePositionParams{Position: target.Position, ID: neighbor.ID, ScreenID: screenID})
// Step 3: park the target at the neighbor's old position
qtx.SetPagePosition(ctx, db.SetPagePositionParams{Position: neighbor.Position, ID: target.ID, ScreenID: screenID})

return tx.Commit()
```

The same pattern applies to widget instances. The `-target.Position` value never collides with a real `position` (which is always positive). The window where the row has a negative position is entirely inside the transaction; concurrent readers (which do not exist in practice, since reorder is admin-driven and rare) would block on the write lock.

Three UPDATEs is acceptable; the alternative (rebuild the entire `position` sequence after every swap) is N UPDATEs.

### GetScreenFull Query Plan

```go
// Step 1: load the screen + theme name in one query (using ListScreenSummaries-like JOIN, but for a single row)
screen, _ := s.queries.GetScreenByID(ctx, id)
theme, _ := s.themes.GetByID(ctx, screen.ThemeID)

// Step 2: load the pages in position order
pages, _ := s.queries.ListPagesByScreen(ctx, id)

// Step 3: load the widget instances in one query for ALL pages, then partition in Go
pageIDs := make([]string, len(pages))
for i, p := range pages { pageIDs[i] = p.ID }
widgets, _ := s.queries.ListWidgetInstancesByPageIDs(ctx, pageIDs)

// In Go: group widgets by page_id; emit the ScreenFull tree.
```

Three queries plus an in-Go group-by. No N+1.

### Theme.Delete change

The existing `themes.Service.Delete` issues `DELETE FROM themes WHERE id = ? AND is_default = 0`. After this spec, the `screens.theme_id REFERENCES themes(id) ON DELETE RESTRICT` clause causes SQLite to raise a FK-constraint failure when the theme is referenced. The modernc.org/sqlite driver surfaces this as an error whose string contains `FOREIGN KEY constraint failed`. We detect that substring (same idiom as the existing `isUniqueNameViolation`) and convert it to `ErrThemeInUse`.

```go
// in internal/themes/service.go
var ErrThemeInUse = errors.New("theme in use by one or more screens")

// in Delete:
res, err := s.queries.DeleteTheme(ctx, id)
if err != nil {
    if isForeignKeyViolation(err) {
        return ErrThemeInUse
    }
    return fmt.Errorf("delete theme: %w", err)
}
// ... (existing RowsAffected handling)

// new helper:
func isForeignKeyViolation(err error) bool {
    if err == nil {
        return false
    }
    return strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}
```

The existing default-theme protection (`is_default = 0` in the WHERE clause) and the new FK-violation detection compose cleanly: the order is "check default first, then issue DELETE, then translate FK error". An admin who tries to delete the default theme that is also in use sees `ErrCannotDeleteDefault` (the default check fires first); an admin who tries to delete a non-default theme in use sees `ErrThemeInUse`.

The Theme admin handler (`handleThemeDelete`) gains one new branch:

```go
if errors.Is(err, themes.ErrThemeInUse) {
    http.Redirect(w, r, "/admin/themes?error=Cannot+delete+a+theme+in+use+by+a+screen", http.StatusFound)
    return
}
```

### Migration Numbering

Existing migrations: 001-006. This spec adds 007, 008, 009. Numbering remains monotonic.

### sqlc Generation

After adding the three query files, the implementer runs `sqlc generate`. The generator produces `internal/db/screens.sql.go`, `pages.sql.go`, `widget_instances.sql.go` and three new model structs in `models.go`:

```go
type Screen struct {
    ID                      string
    Name                    string
    ThemeID                 string
    RotationIntervalSeconds int64
    CreatedAt               string
    UpdatedAt               string
}

type Page struct {
    ID        string
    ScreenID  string
    Name      string
    Position  int64
    CreatedAt string
    UpdatedAt string
}

type WidgetInstance struct {
    ID        string
    PageID    string
    Type      string
    Config    string
    Position  int64
    CreatedAt string
    UpdatedAt string
}
```

The service translates these int64 / string time values into the domain types via small `screenFromRow` / `pageFromRow` / `widgetFromRow` helpers.

## Configuration

No new environment-driven configuration. Layout choices, rotation min/max, and similar are constants in `internal/screens/validate.go`. If a future spec needs to make them configurable, the `config.Config` struct extension is straightforward.

## Security Considerations

### Authentication and CSRF

All routes sit behind the existing chain. There are no public screen / page / widget endpoints in this spec. Screen Display will introduce a device-facing render endpoint in its own spec; that will be auth-gated against the device cookie / token.

### Widget Config Validation

The service round-trips the JSON config through `widget.Default().Validate(type, raw)` before writing. This is the same chokepoint SPEC-005 established. Hand-edited bad rows (someone editing the SQLite file directly) cannot bypass the check because Screen Display will re-validate at render time (per ADR-005's two-layer rule).

### Theme FK Defence

`ON DELETE RESTRICT` is the DB-layer protection against orphaning a screen's theme reference. The application-layer `themes.Service.Delete` translates the FK violation into the typed `ErrThemeInUse`. The admin UI translates that into a user-visible flash. Three layers; the chain is fail-closed.

### Page Name Looseness

Page names are non-unique within a screen and may be empty. We accept this trade-off (see ADR-006 / Open Question Q7). The name is admin-side metadata only; the page is addressed by ID everywhere.

### No SQL Injection

All queries are sqlc-generated; all parameters use `?` placeholders. The widget config column accepts raw JSON bytes, but the bytes never go through SQL string concatenation. The reorder swap uses parameterised UPDATEs.

### Authorisation

Member users (non-admin) are blocked by `RequireRole(RoleAdmin)`. Device identities receive the same 403. There is no per-screen ACL in v1 -- admins are admins for the whole system.

## Task Breakdown

This architecture decomposes into the following tasks. Numbering continues from TASK-020 (the last task in SPEC-005).

1. **TASK-021**: Schema migrations + sqlc query files for screens, pages, widget_instances. -- (prerequisite: none)
2. **TASK-022**: `internal/screens/` service: domain types, validation, CRUD for Screens, GetScreenFull, plus the `themes.ErrThemeInUse` extension to the Theme System. -- (prerequisite: TASK-021)
3. **TASK-023**: `internal/screens/` service: Page CRUD plus reorder operations (MovePageUp/Down) using the transactional swap. -- (prerequisite: TASK-022)
4. **TASK-024**: `internal/screens/` service: Widget instance CRUD plus reorder operations, including widget-registry validation on Add. -- (prerequisite: TASK-022; parallel with TASK-023)
5. **TASK-025**: Screen admin views: `views/screens.go` + `views/screens.templ` for list / edit pages, route registration in `views/routes.go`, `Deps.Screens` field, admin landing link, main.go wiring. -- (prerequisite: TASK-023, TASK-024)
6. **TASK-026**: Page admin views and Widget admin views: per-page edit page with widget instance management, plus the move-up / move-down / delete actions for pages and widgets. (Could be merged with TASK-025 if TASK-025 turns out small enough; the architecture keeps them separate to maximise reviewability.) -- (prerequisite: TASK-025)

### Task Dependency Graph

```
TASK-021 (migrations + sqlc)
    |
    v
TASK-022 (screens.Service: types, validation, Screen CRUD, GetScreenFull, themes.ErrThemeInUse)
    |
    +-----------------+-----------------+
    v                                   v
TASK-023 (Page CRUD              TASK-024 (Widget instance CRUD
            + reorder)                   + reorder + registry validate)
    |                                   |
    +----------------+------------------+
                     v
            TASK-025 (Screen admin views + main.go wiring)
                     |
                     v
            TASK-026 (Page edit + widget edit views)
```

### Sizing Notes

- TASK-021 is a focused data-layer task: three migrations + three query files + sqlc generation. Single coding session.
- TASK-022 is the biggest single task: domain types, validation, Screen CRUD, GetScreenFull (which touches all three tables), AND the additive change to `themes.Service.Delete`. Within "single coding session" if scoped tightly.
- TASK-023 and TASK-024 are deliberately parallel. Both depend on TASK-022's domain types and service skeleton; neither depends on the other. Reorder logic appears in both, but the patterns are isolated to each.
- TASK-025 is the biggest views task: the list page, the edit page, the route wiring, and main.go integration. It is bigger than the corresponding theme view task (TASK-018) because there is more state to render.
- TASK-026 is the per-page editor: the widget instance list, the move/delete actions, and the widget-type picker `<select>`.

A six-task split is intentional: it splits the spec along three review boundaries (data layer, domain service, HTTP / admin UI), with the domain service further split into Screen, Page, Widget threads so the two child-entity threads can be reviewed (and even implemented) in parallel.

## Alternatives Considered

See ADR-006 for the full design rationale. Architectural alternatives evaluated during this design pass:

- **One single migration combining screens + pages + widget_instances**: rejected. Three migrations are independently meaningful, the down migration of each is small, and migration numbering remains atomic per entity. A single combined migration would be uglier in the down path.
- **Per-widget-type subtables (revisit ADR-005)**: rejected (again). Confirmed by this spec's experience: a single `widget_instances` table with a JSON config column means Screen Model touches one table, not seven (and counting). Schema-stable as widgets evolve.
- **Page layout as a row/column grid in v1**: rejected. See ADR-006 / Q1. The 1-D model is the smallest design that solves the v1 problem and has a clean migration path.
- **`themes.theme_id` SET NULL on delete**: rejected. See ADR-006 / Q2. Forces every render path to handle a "no theme" branch.
- **Application-layer cascade for child deletes**: rejected. The DB-layer cascade is automatic, atomic, and requires no service code to know about every child table.
- **Skip GetScreenFull; let Screen Display build it**: rejected. Pre-shipping it here consolidates the query plan and keeps Screen Display's diff additive. Also lets us write tests for the aggregated shape before the renderer exists.
- **Inline form re-render for validation errors (mirror Theme System)**: rejected. Screens / pages / widgets have few input fields; the `?error=...` flash is simpler. Theme System renders inline because hex colors benefit from per-field error placement; here the per-field benefit is much smaller.
- **One generic "reorder" service method (`Reorder(parentID, childID, newPosition)`)**: rejected. The "move up / move down" UX is what the admin UI surfaces; a generic reorder makes both the API and the test surface wider. v1 keeps it focused.
- **Service depends only on `*sql.DB` (no `themes.Service` or `widget.Registry`)**: rejected. Threading the explicit dependencies makes validation and testing cleaner. The constructor signature is wider but the test path is obvious.
- **Per-page theme override (`pages.theme_id` nullable)**: rejected for v1. A page inherits the screen's theme; if an admin wants different looks, they make multiple screens. Adding the column later is additive.
- **Drag-and-drop reorder UI**: deferred to a future spec. The "move up / move down" UX is the smallest UX that works; drag-and-drop sits on the same `position` column.
- **Editable per-instance widget config in this spec**: rejected. Widget Selection UI (p1, later in this phase) owns that. v1 ships add/delete/reorder + default config.
- **Soft delete (mark `deleted_at` instead of dropping rows)**: rejected. The CASCADE semantics on the FK chain are simpler than maintaining a "soft delete" predicate in every query. If a future spec needs undelete, it can layer it on; the current shape doesn't preclude it.
- **JSON Schema column on `widget_instances` to enable cross-widget querying**: rejected. The application never queries across widget config shapes; YAGNI.
- **A `screen_groups` entity for organising many screens**: rejected. No use case; the screens list is short enough that grouping is premature.
