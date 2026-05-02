package db

import (
	"context"
	"strings"
	"testing"
)

// insertTestTheme is a small helper for the schema tests. It inserts a theme
// with placeholder color/font values so the test only has to specify what it
// cares about (id / name / is_default).
func insertTestTheme(ctx context.Context, t *testing.T, q *Queries, id, name string, isDefault int64) {
	t.Helper()
	if err := q.CreateTheme(ctx, CreateThemeParams{
		ID:             id,
		Name:           name,
		IsDefault:      isDefault,
		ColorBg:        "#000000",
		ColorSurface:   "#111111",
		ColorBorder:    "#222222",
		ColorText:      "#ffffff",
		ColorTextMuted: "#cccccc",
		ColorAccent:    "#7b93ff",
		FontFamily:     "system-ui",
		FontFamilyMono: "",
		Radius:         "10px",
	}); err != nil {
		t.Fatalf("CreateTheme(id=%s, name=%s, is_default=%d): %v", id, name, isDefault, err)
	}
}

// TestThemesTable_ExistsAfterMigration verifies that migration 006 creates the
// themes table and the partial unique index that enforces the "exactly one
// default theme" invariant. The partial index is the lynchpin of the default-
// theme correctness story; without it, two rows could silently co-exist with
// is_default = 1.
func TestThemesTable_ExistsAfterMigration(t *testing.T) {
	database := OpenTestDB(t)

	var name string
	if err := database.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='themes'",
	).Scan(&name); err != nil {
		t.Fatalf("themes table not created by migration: %v", err)
	}
	if name != "themes" {
		t.Errorf("table name = %q, want %q", name, "themes")
	}

	var idxName string
	if err := database.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='index' AND name='themes_one_default'",
	).Scan(&idxName); err != nil {
		t.Fatalf("themes_one_default index not created by migration: %v", err)
	}
	if idxName != "themes_one_default" {
		t.Errorf("index name = %q, want %q", idxName, "themes_one_default")
	}
}

// TestThemesTable_PartialUniqueDefaultRejectsSecond verifies that inserting two
// rows with is_default = 1 fails on the second INSERT due to the partial
// unique index. This is the schema-level guarantee that the "exactly one
// default theme" invariant cannot be violated even if a buggy caller skips the
// transactional SetDefault flow.
func TestThemesTable_PartialUniqueDefaultRejectsSecond(t *testing.T) {
	database := OpenTestDB(t)

	insert := `INSERT INTO themes
		(id, name, is_default, color_bg, color_surface, color_border, color_text, color_text_muted, color_accent, font_family, font_family_mono, radius)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	// First default theme: succeeds.
	if _, err := database.Exec(insert,
		"theme-default-1", "default-1", 1,
		"#000000", "#111111", "#222222", "#ffffff", "#cccccc", "#7b93ff",
		"system-ui", "", "10px",
	); err != nil {
		t.Fatalf("first default theme insert failed: %v", err)
	}

	// Second default theme with a different id and name: must fail because of
	// the partial unique index on (is_default) WHERE is_default = 1.
	_, err := database.Exec(insert,
		"theme-default-2", "default-2", 1,
		"#000000", "#111111", "#222222", "#ffffff", "#cccccc", "#7b93ff",
		"system-ui", "", "10px",
	)
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation for second is_default=1 row, got nil")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
		t.Errorf("expected error to mention UNIQUE constraint, got: %v", err)
	}
}

// TestThemesTable_NonDefaultRowsCoexist verifies the partial-unique-index
// design: any number of is_default = 0 rows are allowed; only is_default = 1
// is the constrained value. Together with the previous test, this confirms
// the index is *partial* (filtered) and not a plain unique index that would
// force is_default to be globally unique.
func TestThemesTable_NonDefaultRowsCoexist(t *testing.T) {
	database := OpenTestDB(t)

	insert := `INSERT INTO themes
		(id, name, is_default, color_bg, color_surface, color_border, color_text, color_text_muted, color_accent, font_family, font_family_mono, radius)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	// One default + one non-default: both succeed.
	if _, err := database.Exec(insert,
		"theme-d", "the-default", 1,
		"#000000", "#111111", "#222222", "#ffffff", "#cccccc", "#7b93ff",
		"system-ui", "", "10px",
	); err != nil {
		t.Fatalf("default theme insert failed: %v", err)
	}
	if _, err := database.Exec(insert,
		"theme-a", "alt-a", 0,
		"#101010", "#202020", "#303030", "#fafafa", "#bbbbbb", "#ffaa00",
		"system-ui", "", "8px",
	); err != nil {
		t.Fatalf("first non-default theme insert failed: %v", err)
	}
	// A second non-default also succeeds: the partial index does not constrain
	// is_default = 0 rows.
	if _, err := database.Exec(insert,
		"theme-b", "alt-b", 0,
		"#101010", "#202020", "#303030", "#fafafa", "#bbbbbb", "#00aaff",
		"system-ui", "", "8px",
	); err != nil {
		t.Fatalf("second non-default theme insert failed: %v", err)
	}
}

// TestUpdateTheme_PreservesIsDefault verifies the architectural contract that
// UpdateTheme MUST NOT touch is_default. The default flag is changed only via
// SetDefault / ClearDefault; allowing UpdateTheme to mutate it would let any
// admin save action silently demote the system default.
func TestUpdateTheme_PreservesIsDefault(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "the-default", "the-default", 1)

	if err := q.UpdateTheme(ctx, UpdateThemeParams{
		ID:             "the-default",
		Name:           "renamed",
		ColorBg:        "#aaaaaa",
		ColorSurface:   "#bbbbbb",
		ColorBorder:    "#cccccc",
		ColorText:      "#dddddd",
		ColorTextMuted: "#eeeeee",
		ColorAccent:    "#ffffff",
		FontFamily:     "Arial",
		FontFamilyMono: "Courier",
		Radius:         "5px",
	}); err != nil {
		t.Fatalf("UpdateTheme: %v", err)
	}

	got, err := q.GetThemeByID(ctx, "the-default")
	if err != nil {
		t.Fatalf("GetThemeByID after update: %v", err)
	}
	if got.IsDefault != 1 {
		t.Errorf("UpdateTheme silently changed is_default to %d; want 1", got.IsDefault)
	}
	// Sanity: other fields did update.
	if got.Name != "renamed" || got.ColorBg != "#aaaaaa" || got.Radius != "5px" {
		t.Errorf("UpdateTheme did not apply other fields: name=%q color_bg=%q radius=%q", got.Name, got.ColorBg, got.Radius)
	}
}

// TestDeleteTheme_RowsAffectedDistinguishesDefault verifies the contract that
// DeleteTheme returns RowsAffected=0 when the target is the current default
// (so the service can return ErrCannotDeleteDefault) and RowsAffected=1 for a
// successful non-default delete. This is the only way the service can tell
// "row was the default" apart from "row didn't exist" without a follow-up
// SELECT, so the contract has to hold.
func TestDeleteTheme_RowsAffectedDistinguishesDefault(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "is-default", "is-default", 1)
	insertTestTheme(ctx, t, q, "non-default", "non-default", 0)

	// Delete the default: WHERE is_default = 0 prevents it; rowsAffected = 0.
	res, err := q.DeleteTheme(ctx, "is-default")
	if err != nil {
		t.Fatalf("DeleteTheme(default): %v", err)
	}
	n, _ := res.RowsAffected()
	if n != 0 {
		t.Errorf("DeleteTheme(default) RowsAffected = %d, want 0", n)
	}
	// Default row must still exist.
	if _, err := q.GetThemeByID(ctx, "is-default"); err != nil {
		t.Errorf("default row was unexpectedly deleted: %v", err)
	}

	// Delete the non-default: rowsAffected = 1.
	res, err = q.DeleteTheme(ctx, "non-default")
	if err != nil {
		t.Fatalf("DeleteTheme(non-default): %v", err)
	}
	n, _ = res.RowsAffected()
	if n != 1 {
		t.Errorf("DeleteTheme(non-default) RowsAffected = %d, want 1", n)
	}
	if _, err := q.GetThemeByID(ctx, "non-default"); err == nil {
		t.Error("non-default row still exists after delete")
	}

	// Delete a nonexistent id: rowsAffected = 0, no error.
	res, err = q.DeleteTheme(ctx, "no-such-id")
	if err != nil {
		t.Fatalf("DeleteTheme(missing): %v", err)
	}
	n, _ = res.RowsAffected()
	if n != 0 {
		t.Errorf("DeleteTheme(missing) RowsAffected = %d, want 0", n)
	}
}

// TestSetDefaultTheme_NoExistingDefaultRowsAffectedZero verifies the contract
// that SetDefaultTheme returns RowsAffected=0 when no row matches the given
// id. This is how the service detects "id not found" without a separate
// SELECT.
func TestSetDefaultTheme_NoSuchID(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "real", "real", 0)

	res, err := q.SetDefaultTheme(ctx, "doesnotexist")
	if err != nil {
		t.Fatalf("SetDefaultTheme(missing): %v", err)
	}
	n, _ := res.RowsAffected()
	if n != 0 {
		t.Errorf("SetDefaultTheme(missing) RowsAffected = %d, want 0", n)
	}
}

// TestSetDefaultTheme_WithoutClearViolatesIndex documents the architectural
// contract that SetDefaultTheme alone is not enough to swap the default --
// the partial unique index forces callers to go through the
// ClearDefaultTheme + SetDefaultTheme pair (in a transaction; that wrapping
// is the service-layer concern in TASK-017). If a caller skips ClearDefault,
// the index turns the bug into a hard constraint failure rather than letting
// two defaults silently coexist.
func TestSetDefaultTheme_WithoutClearViolatesIndex(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "first", "first", 1)
	insertTestTheme(ctx, t, q, "second", "second", 0)

	_, err := q.SetDefaultTheme(ctx, "second")
	if err == nil {
		t.Fatal("expected SetDefaultTheme to violate the partial unique index when a default already exists, got nil")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
		t.Errorf("expected UNIQUE constraint error, got: %v", err)
	}
}

// TestClearAndSetDefault_HappyPathSwap verifies that the documented "clear
// then set" flow correctly swaps the default when wrapped in the same
// connection. (The transaction wrapping the pair is TASK-017; here we only
// verify the query shapes work together.)
func TestClearAndSetDefault_HappyPathSwap(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "old", "old", 1)
	insertTestTheme(ctx, t, q, "new", "new", 0)

	if err := q.ClearDefaultTheme(ctx); err != nil {
		t.Fatalf("ClearDefaultTheme: %v", err)
	}
	res, err := q.SetDefaultTheme(ctx, "new")
	if err != nil {
		t.Fatalf("SetDefaultTheme: %v", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		t.Errorf("SetDefaultTheme RowsAffected = %d, want 1", n)
	}

	old, _ := q.GetThemeByID(ctx, "old")
	if old.IsDefault != 0 {
		t.Errorf("old.IsDefault = %d after swap, want 0", old.IsDefault)
	}
	newRow, _ := q.GetThemeByID(ctx, "new")
	if newRow.IsDefault != 1 {
		t.Errorf("new.IsDefault = %d after swap, want 1", newRow.IsDefault)
	}

	count, _ := q.CountDefaultThemes(ctx)
	if count != 1 {
		t.Errorf("CountDefaultThemes after swap = %d, want exactly 1", count)
	}
}

// TestCreateTheme_RejectsSQLMetacharactersAsLiteralValues verifies that sqlc's
// parameterised queries store SQL metacharacter sequences as literal text
// (no injection) and the table is not mutated by the inserted name. This is
// confirmation that the sqlc-generated code uses bound parameters, not
// string concatenation.
func TestCreateTheme_RejectsSQLMetacharactersAsLiteralValues(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	evil := "ev'il); DROP TABLE themes;--"
	insertTestTheme(ctx, t, q, "sql1", evil, 0)

	got, err := q.GetThemeByName(ctx, evil)
	if err != nil {
		t.Fatalf("GetThemeByName: %v", err)
	}
	if got.Name != evil {
		t.Errorf("name round-trip lost the metacharacters: got %q want %q", got.Name, evil)
	}

	// Table should still exist; CountDefaultThemes proves the table was not
	// dropped by an injection.
	if _, err := q.CountDefaultThemes(ctx); err != nil {
		t.Errorf("CountDefaultThemes after metachar insert: %v -- table may have been dropped", err)
	}
}

// TestCreateTheme_LongName documents that the schema does not impose a
// length limit on theme names at the database layer. The application-layer
// validators (added in TASK-017) are the source of truth for length caps.
// This test pins the current behaviour: a 1MB name is accepted by SQLite,
// so any future length enforcement must live in Go.
func TestCreateTheme_LongName(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	long := strings.Repeat("x", 1024*1024)
	insertTestTheme(ctx, t, q, "long", long, 0)

	got, err := q.GetThemeByName(ctx, long)
	if err != nil {
		t.Fatalf("GetThemeByName(long): %v", err)
	}
	if len(got.Name) != len(long) {
		t.Errorf("name length round-trip: got %d, want %d", len(got.Name), len(long))
	}
}

// TestCreateTheme_NotNullIsDefault verifies that an explicit NULL is_default
// value is rejected by the NOT NULL constraint. This is the second line of
// defence on top of the partial unique index: it ensures every row has a
// definite 0 or 1 in is_default rather than NULL (which would not be covered
// by the partial unique index's `WHERE is_default = 1` predicate).
func TestCreateTheme_NotNullIsDefault(t *testing.T) {
	database := OpenTestDB(t)

	_, err := database.Exec(`INSERT INTO themes
		(id, name, is_default, color_bg, color_surface, color_border, color_text, color_text_muted, color_accent, font_family, font_family_mono, radius)
		VALUES ('null-d', 'null-d-test', NULL, '#000', '#000', '#000', '#000', '#000', '#000', 'x', '', '0')`)
	if err == nil {
		t.Fatal("expected NOT NULL constraint violation for is_default=NULL, got nil")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "NOT NULL") {
		t.Errorf("expected NOT NULL constraint error, got: %v", err)
	}
}

// TestThemesTable_DefaultColumnDefaults verifies that the documented column
// defaults work: is_default defaults to 0, font_family_mono defaults to ”,
// and created_at / updated_at default to a non-empty timestamp. The seed
// path in TASK-017 relies on these defaults to keep the INSERT params list
// short.
func TestThemesTable_DefaultColumnDefaults(t *testing.T) {
	database := OpenTestDB(t)

	// Insert with only the strictly-required columns; everything else
	// falls back to its DEFAULT.
	if _, err := database.Exec(`INSERT INTO themes
		(id, name, color_bg, color_surface, color_border, color_text, color_text_muted, color_accent, font_family, radius)
		VALUES ('defaults', 'defaults-row', '#000', '#000', '#000', '#000', '#000', '#000', 'x', '0')`); err != nil {
		t.Fatalf("INSERT relying on defaults: %v", err)
	}

	var (
		isDefault      int64
		fontFamilyMono string
		createdAt      string
		updatedAt      string
	)
	err := database.QueryRow(
		`SELECT is_default, font_family_mono, created_at, updated_at FROM themes WHERE id = 'defaults'`,
	).Scan(&isDefault, &fontFamilyMono, &createdAt, &updatedAt)
	if err != nil {
		t.Fatalf("SELECT defaults: %v", err)
	}
	if isDefault != 0 {
		t.Errorf("is_default default = %d, want 0", isDefault)
	}
	if fontFamilyMono != "" {
		t.Errorf("font_family_mono default = %q, want \"\"", fontFamilyMono)
	}
	if createdAt == "" {
		t.Errorf("created_at default is empty; expected a timestamp")
	}
	if updatedAt == "" {
		t.Errorf("updated_at default is empty; expected a timestamp")
	}
}
