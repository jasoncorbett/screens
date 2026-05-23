package screens

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jasoncorbett/screens/internal/auth"
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

// ErrWidgetNotFound is returned when a widget instance id does not exist (or
// does not belong to the named page).
var ErrWidgetNotFound = errors.New("widget instance not found")

// ErrUnknownWidgetType is returned by AddWidget when the requested type has no
// registration in the widget registry.
var ErrUnknownWidgetType = errors.New("unknown widget type")

// ErrThemeNotFound is returned by CreateScreen / UpdateScreen when the
// requested theme_id has no row in the themes table. It is intentionally
// distinct from themes.ErrThemeNotFound to keep package boundaries clean; the
// admin UI translates both to a "theme not found" message.
var ErrThemeNotFound = errors.New("theme not found")

// ErrDuplicateName is returned when create / update would produce a duplicate
// Screen name.
var ErrDuplicateName = errors.New("screen name already in use")

// Service orchestrates screen / page / widget-instance operations. Construct
// via NewService.
type Service struct {
	sqlDB   *sql.DB
	queries *db.Queries
	themes  *themes.Service
	widgets *widget.Registry
}

// NewService constructs a Service. The themes service and widget registry are
// explicit dependencies because the screens service must validate theme
// references (via themes.Service.GetByID) and widget types (via the registry)
// before persisting.
func NewService(sqlDB *sql.DB, themesSvc *themes.Service, widgets *widget.Registry) *Service {
	return &Service{
		sqlDB:   sqlDB,
		queries: db.New(sqlDB),
		themes:  themesSvc,
		widgets: widgets,
	}
}

// CreateScreen validates input and inserts a new Screen row. Returns
// *ValidationError on input failures, ErrThemeNotFound if the theme_id does
// not exist, and ErrDuplicateName on a name collision.
func (s *Service) CreateScreen(ctx context.Context, in ScreenInput) (Screen, error) {
	clean, err := validateScreenInput(in)
	if err != nil {
		return Screen{}, err
	}

	if _, err := s.themes.GetByID(ctx, clean.ThemeID); err != nil {
		if errors.Is(err, themes.ErrThemeNotFound) {
			return Screen{}, ErrThemeNotFound
		}
		return Screen{}, fmt.Errorf("lookup theme: %w", err)
	}

	id, err := generateID()
	if err != nil {
		return Screen{}, fmt.Errorf("generate screen id: %w", err)
	}

	if err := s.queries.CreateScreen(ctx, db.CreateScreenParams{
		ID:                      id,
		Name:                    clean.Name,
		ThemeID:                 clean.ThemeID,
		RotationIntervalSeconds: int64(clean.RotationIntervalSeconds),
	}); err != nil {
		if isUniqueNameViolation(err) {
			return Screen{}, ErrDuplicateName
		}
		return Screen{}, fmt.Errorf("create screen: %w", err)
	}

	return s.GetScreenByID(ctx, id)
}

// GetScreenByID returns a Screen by ID. Returns ErrScreenNotFound when no row
// matches.
func (s *Service) GetScreenByID(ctx context.Context, id string) (Screen, error) {
	row, err := s.queries.GetScreenByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Screen{}, ErrScreenNotFound
		}
		return Screen{}, fmt.Errorf("get screen: %w", err)
	}
	screen, err := screenFromRow(row)
	if err != nil {
		return Screen{}, fmt.Errorf("convert screen: %w", err)
	}
	return screen, nil
}

// ListScreens returns every screen as a summary (with theme name and page
// count) ordered by name. The returned slice is non-nil (empty on no rows).
func (s *Service) ListScreens(ctx context.Context) ([]ScreenSummary, error) {
	rows, err := s.queries.ListScreenSummaries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list screens: %w", err)
	}
	out := make([]ScreenSummary, 0, len(rows))
	for _, row := range rows {
		screen, err := screenFromRow(db.Screen{
			ID:                      row.ID,
			Name:                    row.Name,
			ThemeID:                 row.ThemeID,
			RotationIntervalSeconds: row.RotationIntervalSeconds,
			CreatedAt:               row.CreatedAt,
			UpdatedAt:               row.UpdatedAt,
		})
		if err != nil {
			return nil, fmt.Errorf("convert screen: %w", err)
		}
		out = append(out, ScreenSummary{
			Screen:    screen,
			ThemeName: row.ThemeName,
			PageCount: int(row.PageCount),
		})
	}
	return out, nil
}

// UpdateScreen validates input and mutates an existing Screen. Returns
// *ValidationError on input failures, ErrThemeNotFound if the theme_id does
// not exist, ErrScreenNotFound when no row matches, and ErrDuplicateName on a
// name collision.
func (s *Service) UpdateScreen(ctx context.Context, id string, in ScreenInput) (Screen, error) {
	clean, err := validateScreenInput(in)
	if err != nil {
		return Screen{}, err
	}

	if _, err := s.themes.GetByID(ctx, clean.ThemeID); err != nil {
		if errors.Is(err, themes.ErrThemeNotFound) {
			return Screen{}, ErrThemeNotFound
		}
		return Screen{}, fmt.Errorf("lookup theme: %w", err)
	}

	if _, err := s.queries.GetScreenByID(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Screen{}, ErrScreenNotFound
		}
		return Screen{}, fmt.Errorf("lookup screen: %w", err)
	}

	if err := s.queries.UpdateScreen(ctx, db.UpdateScreenParams{
		ID:                      id,
		Name:                    clean.Name,
		ThemeID:                 clean.ThemeID,
		RotationIntervalSeconds: int64(clean.RotationIntervalSeconds),
	}); err != nil {
		if isUniqueNameViolation(err) {
			return Screen{}, ErrDuplicateName
		}
		return Screen{}, fmt.Errorf("update screen: %w", err)
	}

	return s.GetScreenByID(ctx, id)
}

// DeleteScreen removes a Screen and (via the DB-layer CASCADE) its pages and
// widget instances. Returns ErrScreenNotFound when no row matches.
func (s *Service) DeleteScreen(ctx context.Context, id string) error {
	res, err := s.queries.DeleteScreen(ctx, id)
	if err != nil {
		return fmt.Errorf("delete screen: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete screen rows: %w", err)
	}
	if n == 0 {
		return ErrScreenNotFound
	}
	return nil
}

// CreatePage validates the name and inserts a new page row on the named
// screen. The new page's position is max(existing positions)+1, or 1 if no
// pages exist on the screen. Returns *ValidationError on a malformed name,
// ErrScreenNotFound if the screen does not exist.
func (s *Service) CreatePage(ctx context.Context, screenID, name string) (Page, error) {
	cleanName, err := validatePageName(name)
	if err != nil {
		return Page{}, &ValidationError{Fields: map[string]string{"name": err.Error()}}
	}

	if _, err := s.queries.GetScreenByID(ctx, screenID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Page{}, ErrScreenNotFound
		}
		return Page{}, fmt.Errorf("lookup screen: %w", err)
	}

	maxPos, err := s.queries.MaxPagePosition(ctx, screenID)
	if err != nil {
		return Page{}, fmt.Errorf("max page position: %w", err)
	}

	id, err := generateID()
	if err != nil {
		return Page{}, fmt.Errorf("generate page id: %w", err)
	}

	if err := s.queries.CreatePage(ctx, db.CreatePageParams{
		ID:       id,
		ScreenID: screenID,
		Name:     cleanName,
		Position: maxPos + 1,
	}); err != nil {
		return Page{}, fmt.Errorf("create page: %w", err)
	}

	return s.GetPageByID(ctx, screenID, id)
}

// GetPageByID returns the page identified by (screenID, pageID). The screen
// ID is required for defence-in-depth: the URL contains the screen ID, and
// the service rejects a (screen, page) pair that does not match. Returns
// ErrPageNotFound when no row matches.
func (s *Service) GetPageByID(ctx context.Context, screenID, pageID string) (Page, error) {
	row, err := s.queries.GetPageByID(ctx, db.GetPageByIDParams{ID: pageID, ScreenID: screenID})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Page{}, ErrPageNotFound
		}
		return Page{}, fmt.Errorf("get page: %w", err)
	}
	page, err := pageFromRow(row)
	if err != nil {
		return Page{}, fmt.Errorf("convert page: %w", err)
	}
	return page, nil
}

// UpdatePage validates the name and updates the existing page row. Returns
// *ValidationError on a malformed name, ErrPageNotFound when no row matches.
func (s *Service) UpdatePage(ctx context.Context, screenID, pageID, name string) (Page, error) {
	cleanName, err := validatePageName(name)
	if err != nil {
		return Page{}, &ValidationError{Fields: map[string]string{"name": err.Error()}}
	}

	if _, err := s.GetPageByID(ctx, screenID, pageID); err != nil {
		return Page{}, err
	}

	if err := s.queries.UpdatePage(ctx, db.UpdatePageParams{
		Name:     cleanName,
		ID:       pageID,
		ScreenID: screenID,
	}); err != nil {
		return Page{}, fmt.Errorf("update page: %w", err)
	}

	return s.GetPageByID(ctx, screenID, pageID)
}

// DeletePage removes a page and (via DB-layer CASCADE) its widget instances.
// Returns ErrPageNotFound when no row matches.
func (s *Service) DeletePage(ctx context.Context, screenID, pageID string) error {
	res, err := s.queries.DeletePage(ctx, db.DeletePageParams{ID: pageID, ScreenID: screenID})
	if err != nil {
		return fmt.Errorf("delete page: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete page rows: %w", err)
	}
	if n == 0 {
		return ErrPageNotFound
	}
	return nil
}

// MovePageUp swaps the named page with the page immediately above it within
// the same screen. Returns nil (no-op) when the page is already at the top.
// Returns ErrPageNotFound when no row matches. The swap runs inside a single
// transaction using the negative-position idiom to avoid violating the
// UNIQUE(screen_id, position) constraint mid-swap.
func (s *Service) MovePageUp(ctx context.Context, screenID, pageID string) error {
	return s.swapPage(ctx, screenID, pageID, -1)
}

// MovePageDown swaps the named page with the page immediately below it
// within the same screen. Returns nil (no-op) when the page is already at
// the bottom. Returns ErrPageNotFound when no row matches.
func (s *Service) MovePageDown(ctx context.Context, screenID, pageID string) error {
	return s.swapPage(ctx, screenID, pageID, +1)
}

// swapPage implements MovePageUp / MovePageDown. direction is -1 to swap
// with the neighbour above, +1 to swap with the neighbour below.
func (s *Service) swapPage(ctx context.Context, screenID, pageID string, direction int) error {
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reorder tx: %w", err)
	}
	defer tx.Rollback()

	qtx := s.queries.WithTx(tx)

	target, err := qtx.GetPageByID(ctx, db.GetPageByIDParams{ID: pageID, ScreenID: screenID})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPageNotFound
		}
		return fmt.Errorf("lookup page: %w", err)
	}

	neighbor, err := qtx.GetPageNeighbor(ctx, db.GetPageNeighborParams{
		ScreenID: screenID,
		Position: target.Position + int64(direction),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No neighbour in that direction: already at the edge. Commit
			// the (empty) transaction so the connection's lock releases
			// cleanly and return nil.
			return tx.Commit()
		}
		return fmt.Errorf("lookup neighbour: %w", err)
	}

	// Step 1: park target at -target.Position (guaranteed unique, since real
	// positions are always positive).
	if err := qtx.SetPagePosition(ctx, db.SetPagePositionParams{
		Position: -target.Position,
		ID:       target.ID,
		ScreenID: screenID,
	}); err != nil {
		return fmt.Errorf("park target position: %w", err)
	}
	// Step 2: move the neighbour into the target's old position.
	if err := qtx.SetPagePosition(ctx, db.SetPagePositionParams{
		Position: target.Position,
		ID:       neighbor.ID,
		ScreenID: screenID,
	}); err != nil {
		return fmt.Errorf("move neighbour: %w", err)
	}
	// Step 3: move the target into the neighbour's old position.
	if err := qtx.SetPagePosition(ctx, db.SetPagePositionParams{
		Position: neighbor.Position,
		ID:       target.ID,
		ScreenID: screenID,
	}); err != nil {
		return fmt.Errorf("move target: %w", err)
	}

	return tx.Commit()
}

// GetScreenFull returns the screen, its theme, its pages in position order,
// and each page's widget instances in position order. Used by Screen Display.
// Returns ErrScreenNotFound when the screen id has no row.
func (s *Service) GetScreenFull(ctx context.Context, id string) (ScreenFull, error) {
	screen, err := s.GetScreenByID(ctx, id)
	if err != nil {
		return ScreenFull{}, err
	}

	theme, err := s.themes.GetByID(ctx, screen.ThemeID)
	if err != nil {
		if errors.Is(err, themes.ErrThemeNotFound) {
			// Unreachable given the RESTRICT FK on screens.theme_id: a screen
			// cannot reference a theme that does not exist. Surface it as a
			// startup-invariant violation rather than silently degrading.
			return ScreenFull{}, fmt.Errorf("screen %s references missing theme %s: %w", id, screen.ThemeID, err)
		}
		return ScreenFull{}, fmt.Errorf("get theme: %w", err)
	}

	pageRows, err := s.queries.ListPagesByScreen(ctx, id)
	if err != nil {
		return ScreenFull{}, fmt.Errorf("list pages: %w", err)
	}

	pages := make([]Page, 0, len(pageRows))
	for _, row := range pageRows {
		page, err := pageFromRow(row)
		if err != nil {
			return ScreenFull{}, fmt.Errorf("convert page: %w", err)
		}
		pages = append(pages, page)
	}

	if len(pages) == 0 {
		return ScreenFull{
			Screen: screen,
			Theme:  theme,
			Pages:  []PageWithWidgets{},
		}, nil
	}

	pageIDs := make([]string, len(pages))
	for i, p := range pages {
		pageIDs[i] = p.ID
	}

	widgetRows, err := s.queries.ListWidgetInstancesByPageIDs(ctx, pageIDs)
	if err != nil {
		return ScreenFull{}, fmt.Errorf("list widget instances: %w", err)
	}

	byPage := make(map[string][]WidgetInstance, len(pages))
	for _, row := range widgetRows {
		w, err := widgetFromRow(row)
		if err != nil {
			return ScreenFull{}, fmt.Errorf("convert widget instance: %w", err)
		}
		byPage[w.PageID] = append(byPage[w.PageID], w)
	}

	full := ScreenFull{
		Screen: screen,
		Theme:  theme,
		Pages:  make([]PageWithWidgets, 0, len(pages)),
	}
	for _, p := range pages {
		widgets := byPage[p.ID]
		if widgets == nil {
			widgets = []WidgetInstance{}
		}
		full.Pages = append(full.Pages, PageWithWidgets{Page: p, Widgets: widgets})
	}
	return full, nil
}

// generateID returns a 32-character hex string (16 bytes of entropy),
// matching the existing internal/auth ID idiom so all primary keys in the
// database share a single format.
func generateID() (string, error) {
	token, err := auth.GenerateToken()
	if err != nil {
		return "", err
	}
	return token[:32], nil
}

// isUniqueNameViolation reports whether err is a SQLite UNIQUE-constraint
// failure on screens.name. Detected via substring match because the
// modernc.org/sqlite driver wraps the underlying error without exposing a
// stable error code via errors.Is.
func isUniqueNameViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed: screens.name")
}
