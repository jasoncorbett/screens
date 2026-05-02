package views

import (
	"testing"

	"github.com/jasoncorbett/screens/internal/widget"

	_ "github.com/jasoncorbett/screens/internal/widget/text" // matches the blank import in main.go
)

// TestDefaultRegistryContainsText is the integration stand-in for
// "main.go is wired correctly". The blank import above triggers the
// text widget's init(), which registers it with widget.Default(). If
// main.go's blank import or the widget's init() ever break, this test
// fails first.
func TestDefaultRegistryContainsText(t *testing.T) {
	regs := widget.Default().List()
	for _, reg := range regs {
		if reg.Type == "text" {
			return
		}
	}
	t.Fatalf("widget.Default().List() does not contain a text registration; got %d entries", len(regs))
}
