package widget_test

import (
	"testing"

	"github.com/jasoncorbett/screens/internal/widget"
)

func TestDefault_ReturnsSameRegistry(t *testing.T) {
	a := widget.Default()
	b := widget.Default()
	if a != b {
		t.Errorf("Default() returned different pointers across calls: %p vs %p", a, b)
	}
}

func TestMustRegister_PanicsOnDuplicate(t *testing.T) {
	// Use a clearly-test-only type name so that even if this leaks into
	// the global registry it cannot collide with a real widget.
	const typeName = "__widget_test_dup__"

	reg := widget.Registration{
		Type:           typeName,
		DisplayName:    typeName,
		Description:    "test widget",
		New:            func() widget.Widget { return &fakeWidget{} },
		DefaultConfig:  func() []byte { return []byte(`{}`) },
		ValidateConfig: func(raw []byte) (widget.Instance, error) { return widget.Instance{Type: typeName}, nil },
	}

	// First registration via plain Register so we get an explicit error
	// if some earlier test (or future widget package import) has
	// already claimed this type name. Skip rather than fail in that
	// case -- this test owns the global only opportunistically.
	if err := widget.Default().Register(reg); err != nil {
		t.Skipf("registry already contains %q (test pollution?): %v", typeName, err)
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("MustRegister did not panic on duplicate registration")
		}
	}()

	widget.MustRegister(reg)
}
