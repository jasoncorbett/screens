// Package text implements the placeholder "text" widget. It exists to
// validate the [widget.Widget] contract end-to-end: a configurable body
// of plain text rendered into a themed <div>. Importing this package is
// what registers the widget with the process-wide [widget.Default]
// registry; main.go blank-imports the package for that side effect.
package text

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/a-h/templ"
	"github.com/jasoncorbett/screens/internal/themes"
	"github.com/jasoncorbett/screens/internal/widget"
)

// Type is the widget-type identifier for the text widget. It is the
// stable JSON discriminator and database column value.
const Type = "text"

// MaxTextLength caps the size of a text widget's body. A single
// placeholder display block does not need more.
const MaxTextLength = 4096

// Config is the per-instance configuration shape for the text widget.
// The persisted JSON shape is {"text": "..."}.
type Config struct {
	Text string `json:"text"`
}

// widgetImpl satisfies [widget.Widget]. Stateless; one global value is
// enough.
type widgetImpl struct{}

// singleton is the one and only [widgetImpl] value the registry hands
// out. Stateless widgets do not need per-render allocation.
var singleton = &widgetImpl{}

// Render implements [widget.Widget]. The active theme's CSS variables
// drive styling at the page level, so this renderer embeds no inline
// colors. The theme parameter is intentionally named (not "_") so a
// future iteration that consults theme fields produces a clean diff.
func (w *widgetImpl) Render(ctx context.Context, instance widget.Instance, theme themes.Theme) templ.Component {
	cfg, _ := instance.Config.(Config) // validate guarantees the type
	return textComponent(cfg.Text)
}

// validate parses raw bytes and returns a [widget.Instance] carrying
// the validated [Config] payload. Trims whitespace from the body,
// rejects empty or whitespace-only input, and rejects bodies larger
// than [MaxTextLength].
func validate(raw []byte) (widget.Instance, error) {
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return widget.Instance{}, fmt.Errorf("text widget: invalid JSON: %w", err)
	}
	cfg.Text = strings.TrimSpace(cfg.Text)
	if cfg.Text == "" {
		return widget.Instance{}, fmt.Errorf("text widget: text must not be empty or whitespace-only")
	}
	if len(cfg.Text) > MaxTextLength {
		return widget.Instance{}, fmt.Errorf("text widget: text must be %d characters or fewer", MaxTextLength)
	}
	return widget.Instance{Type: Type, Config: cfg}, nil
}

// defaultConfig returns a JSON blob representing a sensible starting
// configuration. Marshalling a known-valid struct cannot fail; the
// error is intentionally discarded. Tests assert that the returned
// bytes pass [validate].
func defaultConfig() []byte {
	b, _ := json.Marshal(Config{Text: "Hello, screens"})
	return b
}

// Registration returns the [widget.Registration] for the text widget.
// Tests call this directly to register the widget on a fresh
// [widget.NewRegistry].
func Registration() widget.Registration {
	return widget.Registration{
		Type:           Type,
		DisplayName:    "Text",
		Description:    "Display a configurable block of text.",
		New:            func() widget.Widget { return singleton },
		DefaultConfig:  defaultConfig,
		ValidateConfig: validate,
	}
}

func init() {
	widget.MustRegister(Registration())
}
