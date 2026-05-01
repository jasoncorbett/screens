package db

import (
	"strings"
	"testing"
)

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
