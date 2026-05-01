// Package themes manages display theme records: their persistent storage,
// validation rules, and the CSS rendering helper that downstream Screen
// Display embeds in a <style> tag.
package themes

import (
	"time"

	"github.com/jasoncorbett/screens/internal/db"
)

// Theme is the validated, domain-level theme type returned by the Service.
// All string fields have already been normalised (hex values lower-cased,
// whitespace trimmed) so callers may render them into CSS without re-checking.
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
// Service.Create and Service.Update validate and normalise this struct before
// persisting it; validation failures are returned as *ValidationError.
type Input struct {
	Name           string
	ColorBg        string
	ColorSurface   string
	ColorBorder    string
	ColorText      string
	ColorTextMuted string
	ColorAccent    string
	FontFamily     string
	FontFamilyMono string
	Radius         string
}

// themeFromRow translates the sqlc-generated row into the domain type.
func themeFromRow(row db.Theme) (Theme, error) {
	createdAt, err := time.Parse("2006-01-02 15:04:05", row.CreatedAt)
	if err != nil {
		return Theme{}, err
	}
	updatedAt, err := time.Parse("2006-01-02 15:04:05", row.UpdatedAt)
	if err != nil {
		return Theme{}, err
	}
	return Theme{
		ID:             row.ID,
		Name:           row.Name,
		IsDefault:      row.IsDefault == 1,
		ColorBg:        row.ColorBg,
		ColorSurface:   row.ColorSurface,
		ColorBorder:    row.ColorBorder,
		ColorText:      row.ColorText,
		ColorTextMuted: row.ColorTextMuted,
		ColorAccent:    row.ColorAccent,
		FontFamily:     row.FontFamily,
		FontFamilyMono: row.FontFamilyMono,
		Radius:         row.Radius,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}, nil
}
