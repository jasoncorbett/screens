// Package screens manages the dashboard data model: screens, their pages,
// and the widget instances placed on those pages. It exposes a typed service
// over the sqlc-generated query layer, validates screen input, and assembles
// the fully-hydrated ScreenFull tree that Screen Display renders.
package screens

import (
	"time"

	"github.com/jasoncorbett/screens/internal/db"
	"github.com/jasoncorbett/screens/internal/themes"
)

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
// metadata (theme name and page count) that the list template doesn't need
// to issue extra queries.
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
// either re-parse or hand it to widget.Registry.Render directly).
type WidgetInstance struct {
	ID        string
	PageID    string
	Type      string
	Config    []byte // validated JSON; safe to feed into widget.Registry.Render
	Position  int    // 1-indexed
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PageWithWidgets pairs a Page with its widget instances in position order.
type PageWithWidgets struct {
	Page    Page
	Widgets []WidgetInstance
}

// ScreenFull is the fully-hydrated tree Screen Display renders. Pages and
// widget instances are pre-fetched in position order; the theme is looked up
// once via themes.Service.GetByID and copied in by value.
type ScreenFull struct {
	Screen Screen
	Theme  themes.Theme
	Pages  []PageWithWidgets
}

// ScreenInput collects the user-supplied fields needed to create or update a
// Screen. The service validates and normalises this struct before persisting.
type ScreenInput struct {
	Name                    string
	ThemeID                 string
	RotationIntervalSeconds int
}

// timestampLayout is the TEXT format SQLite stores datetime('now') values in.
const timestampLayout = "2006-01-02 15:04:05"

// screenFromRow translates the sqlc-generated row into the domain type.
func screenFromRow(row db.Screen) (Screen, error) {
	createdAt, err := time.Parse(timestampLayout, row.CreatedAt)
	if err != nil {
		return Screen{}, err
	}
	updatedAt, err := time.Parse(timestampLayout, row.UpdatedAt)
	if err != nil {
		return Screen{}, err
	}
	return Screen{
		ID:                      row.ID,
		Name:                    row.Name,
		ThemeID:                 row.ThemeID,
		RotationIntervalSeconds: int(row.RotationIntervalSeconds),
		CreatedAt:               createdAt,
		UpdatedAt:               updatedAt,
	}, nil
}

// pageFromRow translates the sqlc-generated row into the domain type.
func pageFromRow(row db.Page) (Page, error) {
	createdAt, err := time.Parse(timestampLayout, row.CreatedAt)
	if err != nil {
		return Page{}, err
	}
	updatedAt, err := time.Parse(timestampLayout, row.UpdatedAt)
	if err != nil {
		return Page{}, err
	}
	return Page{
		ID:        row.ID,
		ScreenID:  row.ScreenID,
		Name:      row.Name,
		Position:  int(row.Position),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

// widgetFromRow translates the sqlc-generated row into the domain type.
func widgetFromRow(row db.WidgetInstance) (WidgetInstance, error) {
	createdAt, err := time.Parse(timestampLayout, row.CreatedAt)
	if err != nil {
		return WidgetInstance{}, err
	}
	updatedAt, err := time.Parse(timestampLayout, row.UpdatedAt)
	if err != nil {
		return WidgetInstance{}, err
	}
	return WidgetInstance{
		ID:        row.ID,
		PageID:    row.PageID,
		Type:      row.Type,
		Config:    []byte(row.Config),
		Position:  int(row.Position),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}
