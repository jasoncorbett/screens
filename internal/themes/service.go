package themes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jasoncorbett/screens/internal/auth"
	"github.com/jasoncorbett/screens/internal/db"
)

// ErrThemeNotFound is returned when an operation targets a theme id that does
// not exist.
var ErrThemeNotFound = errors.New("theme not found")

// ErrCannotDeleteDefault is returned when an operation would delete the
// system-default theme.
var ErrCannotDeleteDefault = errors.New("cannot delete the default theme")

// ErrDuplicateName is returned when create / update would produce a duplicate
// theme name.
var ErrDuplicateName = errors.New("theme name already in use")

// Default-theme color and font constants. These mirror the values baked into
// static/css/app.css so that the seeded default theme matches the look that
// the application ships with.
const (
	defaultColorBg        = "#0b0d11"
	defaultColorSurface   = "#14171f"
	defaultColorBorder    = "#23273a"
	defaultColorText      = "#dfe2ed"
	defaultColorTextMuted = "#6b7084"
	defaultColorAccent    = "#7b93ff"
	defaultRadius         = "10px"
	defaultFontFamily     = `-apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif`
	defaultFontFamilyMono = `"SF Mono", "Fira Code", "Cascadia Code", monospace`
)

// Config holds theme-specific configuration. Mirrors the auth.Config shape:
// a small struct passed in at construction time.
type Config struct {
	// DefaultName is the name used for the auto-seeded default theme on
	// first startup.
	DefaultName string
}

// Service orchestrates theme operations. Construct via NewService.
type Service struct {
	sqlDB   *sql.DB
	queries *db.Queries
	config  Config
}

// NewService creates a theme service backed by the given database handle.
func NewService(sqlDB *sql.DB, cfg Config) *Service {
	return &Service{
		sqlDB:   sqlDB,
		queries: db.New(sqlDB),
		config:  cfg,
	}
}

// EnsureDefault inserts a theme row with is_default = 1 if and only if no
// such row currently exists. Idempotent. Intended to be called once from
// main.go after migrations have run. The check + insert pair runs inside a
// single transaction to close the boot-time race window between two
// processes starting against the same database file.
func (s *Service) EnsureDefault(ctx context.Context) error {
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin ensure-default tx: %w", err)
	}
	defer tx.Rollback()

	qtx := s.queries.WithTx(tx)
	count, err := qtx.CountDefaultThemes(ctx)
	if err != nil {
		return fmt.Errorf("count default themes: %w", err)
	}
	if count > 0 {
		// Idempotent no-op. Commit so the read-only transaction releases
		// any locks cleanly even though no rows were touched.
		return tx.Commit()
	}

	id, err := generateID()
	if err != nil {
		return fmt.Errorf("generate theme id: %w", err)
	}

	if err := qtx.CreateTheme(ctx, db.CreateThemeParams{
		ID:             id,
		Name:           s.config.DefaultName,
		IsDefault:      1,
		ColorBg:        defaultColorBg,
		ColorSurface:   defaultColorSurface,
		ColorBorder:    defaultColorBorder,
		ColorText:      defaultColorText,
		ColorTextMuted: defaultColorTextMuted,
		ColorAccent:    defaultColorAccent,
		FontFamily:     defaultFontFamily,
		FontFamilyMono: defaultFontFamilyMono,
		Radius:         defaultRadius,
	}); err != nil {
		return fmt.Errorf("seed default theme: %w", err)
	}
	return tx.Commit()
}

// Create validates the input and inserts a new non-default theme row.
// Returns *ValidationError on input failures, ErrDuplicateName when the
// chosen name collides with an existing theme, or other database errors.
func (s *Service) Create(ctx context.Context, in Input) (Theme, error) {
	clean, err := validateInput(in)
	if err != nil {
		return Theme{}, err
	}

	id, err := generateID()
	if err != nil {
		return Theme{}, fmt.Errorf("generate theme id: %w", err)
	}

	if err := s.queries.CreateTheme(ctx, db.CreateThemeParams{
		ID:             id,
		Name:           clean.Name,
		IsDefault:      0,
		ColorBg:        clean.ColorBg,
		ColorSurface:   clean.ColorSurface,
		ColorBorder:    clean.ColorBorder,
		ColorText:      clean.ColorText,
		ColorTextMuted: clean.ColorTextMuted,
		ColorAccent:    clean.ColorAccent,
		FontFamily:     clean.FontFamily,
		FontFamilyMono: clean.FontFamilyMono,
		Radius:         clean.Radius,
	}); err != nil {
		if isUniqueNameViolation(err) {
			return Theme{}, ErrDuplicateName
		}
		return Theme{}, fmt.Errorf("create theme: %w", err)
	}

	row, err := s.queries.GetThemeByID(ctx, id)
	if err != nil {
		return Theme{}, fmt.Errorf("fetch created theme: %w", err)
	}
	t, err := themeFromRow(row)
	if err != nil {
		return Theme{}, fmt.Errorf("convert theme: %w", err)
	}
	return t, nil
}

// GetByID returns a theme by ID. Returns ErrThemeNotFound when no row
// matches.
func (s *Service) GetByID(ctx context.Context, id string) (Theme, error) {
	row, err := s.queries.GetThemeByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Theme{}, ErrThemeNotFound
		}
		return Theme{}, fmt.Errorf("get theme: %w", err)
	}
	t, err := themeFromRow(row)
	if err != nil {
		return Theme{}, fmt.Errorf("convert theme: %w", err)
	}
	return t, nil
}

// List returns every theme ordered by name. The returned slice is non-nil
// (an empty slice on no rows).
func (s *Service) List(ctx context.Context) ([]Theme, error) {
	rows, err := s.queries.ListThemes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list themes: %w", err)
	}
	out := make([]Theme, 0, len(rows))
	for _, row := range rows {
		t, err := themeFromRow(row)
		if err != nil {
			return nil, fmt.Errorf("convert theme: %w", err)
		}
		out = append(out, t)
	}
	return out, nil
}

// GetDefault returns the system default theme. Returns ErrThemeNotFound if
// somehow no default exists -- callers should treat this as a startup
// invariant violation since EnsureDefault runs at boot.
func (s *Service) GetDefault(ctx context.Context) (Theme, error) {
	row, err := s.queries.GetDefaultTheme(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Theme{}, ErrThemeNotFound
		}
		return Theme{}, fmt.Errorf("get default theme: %w", err)
	}
	t, err := themeFromRow(row)
	if err != nil {
		return Theme{}, fmt.Errorf("convert theme: %w", err)
	}
	return t, nil
}

// Update validates the input and mutates the named theme. Returns
// *ValidationError on input failures, ErrThemeNotFound when no row matches,
// and ErrDuplicateName on a UNIQUE name collision. is_default is preserved
// across updates -- callers must use SetDefault to change it.
func (s *Service) Update(ctx context.Context, id string, in Input) (Theme, error) {
	clean, err := validateInput(in)
	if err != nil {
		return Theme{}, err
	}

	if _, err := s.queries.GetThemeByID(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Theme{}, ErrThemeNotFound
		}
		return Theme{}, fmt.Errorf("lookup theme: %w", err)
	}

	if err := s.queries.UpdateTheme(ctx, db.UpdateThemeParams{
		ID:             id,
		Name:           clean.Name,
		ColorBg:        clean.ColorBg,
		ColorSurface:   clean.ColorSurface,
		ColorBorder:    clean.ColorBorder,
		ColorText:      clean.ColorText,
		ColorTextMuted: clean.ColorTextMuted,
		ColorAccent:    clean.ColorAccent,
		FontFamily:     clean.FontFamily,
		FontFamilyMono: clean.FontFamilyMono,
		Radius:         clean.Radius,
	}); err != nil {
		if isUniqueNameViolation(err) {
			return Theme{}, ErrDuplicateName
		}
		return Theme{}, fmt.Errorf("update theme: %w", err)
	}

	row, err := s.queries.GetThemeByID(ctx, id)
	if err != nil {
		return Theme{}, fmt.Errorf("fetch updated theme: %w", err)
	}
	t, err := themeFromRow(row)
	if err != nil {
		return Theme{}, fmt.Errorf("convert theme: %w", err)
	}
	return t, nil
}

// Delete removes a non-default theme. Returns ErrCannotDeleteDefault if the
// theme is the current default and ErrThemeNotFound when no row matches.
// The application-layer is_default check is the primary defence; the SQL
// query's WHERE is_default = 0 clause is a belt-and-suspenders backstop.
func (s *Service) Delete(ctx context.Context, id string) error {
	row, err := s.queries.GetThemeByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrThemeNotFound
		}
		return fmt.Errorf("lookup theme: %w", err)
	}
	if row.IsDefault == 1 {
		return ErrCannotDeleteDefault
	}

	res, err := s.queries.DeleteTheme(ctx, id)
	if err != nil {
		return fmt.Errorf("delete theme: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete theme rows: %w", err)
	}
	if n == 0 {
		// Reachable only if the row mutated to is_default = 1 between the
		// lookup and the DELETE; in practice the SetDefault transaction
		// makes this impossible. Surface as a generic error rather than
		// silently succeeding.
		return fmt.Errorf("delete theme: no row removed")
	}
	return nil
}

// SetDefault marks the given theme as the system default. Atomically clears
// is_default on the previously-default theme and sets it on the target row.
// Returns ErrThemeNotFound if no row matches the id (or if the row was
// deleted between the existence check and the UPDATE).
func (s *Service) SetDefault(ctx context.Context, id string) error {
	if _, err := s.queries.GetThemeByID(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrThemeNotFound
		}
		return fmt.Errorf("lookup theme: %w", err)
	}

	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin set-default tx: %w", err)
	}
	defer tx.Rollback()

	qtx := s.queries.WithTx(tx)
	if err := qtx.ClearDefaultTheme(ctx); err != nil {
		return fmt.Errorf("clear default theme: %w", err)
	}
	res, err := qtx.SetDefaultTheme(ctx, id)
	if err != nil {
		return fmt.Errorf("set default theme: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set default theme rows: %w", err)
	}
	if n == 0 {
		// Row deleted between the lookup and the UPDATE. The deferred
		// Rollback restores the previous is_default state.
		return ErrThemeNotFound
	}
	return tx.Commit()
}

// generateID returns a 32-character hex string (16 bytes of entropy),
// matching the existing internal/auth.generateID idiom so all primary keys
// in the database share a single ID format.
func generateID() (string, error) {
	token, err := auth.GenerateToken()
	if err != nil {
		return "", err
	}
	return token[:32], nil
}

// isUniqueNameViolation reports whether err is a SQLite UNIQUE-constraint
// failure on themes.name. Detected via substring match because the
// modernc.org/sqlite driver wraps the underlying error in its own type
// without exposing a stable error code via errors.Is.
func isUniqueNameViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed: themes.name")
}
