// Package widget defines the contract every widget type implements: a
// single-method [Widget] interface that produces a renderable HTML
// component, plus a [Registration] metadata struct that carries the
// widget's identifier, factory, default-config, and per-instance JSON
// validator. The [Registry] maps widget-type identifiers to their
// registrations and exposes the lookup, validate, and render entry
// points used by Screen Model (write path) and Screen Display (render
// path).
//
// Concrete widget types live in subpackages (e.g. internal/widget/text/)
// and self-register from init() functions via [MustRegister]. Importing
// a widget package is what wires the widget into the process-wide
// [Default] registry, mirroring the database/sql driver-registration
// idiom. Tests build isolated registries via [NewRegistry] and never
// mutate the global.
package widget

import (
	"context"

	"github.com/a-h/templ"
	"github.com/jasoncorbett/screens/internal/themes"
)

// Widget is the contract every widget type implements. It is intentionally
// minimal: a single method that produces a renderable HTML component from
// a validated per-instance configuration plus the active theme.
//
// Widget implementations MUST be safe for concurrent calls to Render. The
// typical implementation is a stateless struct, so this is free.
type Widget interface {
	// Render produces the HTML component for this widget instance. The
	// instance carries the validated configuration; the theme is the
	// active theme for the screen the widget is rendering on.
	// Implementations MUST NOT panic, MUST NOT block on external I/O
	// without honouring ctx, and MUST inherit color/font styling from
	// the theme's CSS variables rather than baking colors into the
	// markup.
	Render(ctx context.Context, instance Instance, theme themes.Theme) templ.Component
}

// Instance is one concrete placement of a widget on a page. Screen Model
// owns instance creation; this package never generates IDs.
type Instance struct {
	// ID is set by Screen Model when the row is created. Empty in tests
	// is acceptable -- nothing in this package depends on the value.
	ID string

	// Type matches Registration.Type. The registry's Render method
	// guarantees this field is populated before delegating to a
	// Widget's Render, even if a validator left it blank.
	Type string

	// Config is the typed-but-erased payload returned by
	// Registration.ValidateConfig. The underlying concrete type is
	// private to each widget package; the widget's Render method
	// type-asserts it back to the concrete type.
	Config any
}
