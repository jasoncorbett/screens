package widget_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/a-h/templ"
	"github.com/jasoncorbett/screens/internal/themes"
	"github.com/jasoncorbett/screens/internal/widget"
)

// fakeWidget is a stub Widget implementation used in registry tests. Its
// Render returns templ.NopComponent and optionally records the Instance
// it was passed via captured fields on a per-test basis through the
// renderFn closure.
type fakeWidget struct {
	renderFn func(ctx context.Context, instance widget.Instance, theme themes.Theme) templ.Component
}

func (f *fakeWidget) Render(ctx context.Context, instance widget.Instance, theme themes.Theme) templ.Component {
	if f.renderFn != nil {
		return f.renderFn(ctx, instance, theme)
	}
	return templ.NopComponent
}

// newTestRegistration returns a minimal valid Registration for tests.
// Callers may override fields after the call.
func newTestRegistration(t *testing.T, typeName string) widget.Registration {
	t.Helper()
	return widget.Registration{
		Type:          typeName,
		DisplayName:   typeName,
		Description:   "test widget",
		New:           func() widget.Widget { return &fakeWidget{} },
		DefaultConfig: func() []byte { return []byte(`{}`) },
		ValidateConfig: func(raw []byte) (widget.Instance, error) {
			return widget.Instance{Type: typeName}, nil
		},
	}
}

func TestRegister_ThenGet(t *testing.T) {
	r := widget.NewRegistry()
	if err := r.Register(newTestRegistration(t, "alpha")); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	reg, ok := r.Get("alpha")
	if !ok {
		t.Fatalf("Get(\"alpha\") ok = false, want true")
	}
	if reg.Type != "alpha" {
		t.Errorf("Get(\"alpha\").Type = %q, want %q", reg.Type, "alpha")
	}
}

func TestGet_UnknownType(t *testing.T) {
	r := widget.NewRegistry()

	reg, ok := r.Get("nonexistent")
	if ok {
		t.Errorf("Get(\"nonexistent\") ok = true, want false")
	}
	if reg.Type != "" || reg.DisplayName != "" || reg.New != nil ||
		reg.DefaultConfig != nil || reg.ValidateConfig != nil {
		t.Errorf("Get on unknown type returned non-zero Registration: %+v", reg)
	}
}

func TestRegister_DuplicateRejected(t *testing.T) {
	r := widget.NewRegistry()
	if err := r.Register(newTestRegistration(t, "alpha")); err != nil {
		t.Fatalf("first Register returned error: %v", err)
	}

	err := r.Register(newTestRegistration(t, "alpha"))
	if err == nil {
		t.Fatalf("second Register returned nil error, want non-nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "alpha") {
		t.Errorf("error %q does not contain %q", msg, "alpha")
	}
	if !strings.Contains(msg, "already") {
		t.Errorf("error %q does not contain %q", msg, "already")
	}
}

func TestRegister_RejectsMissingFields(t *testing.T) {
	tests := []struct {
		name             string
		mutate           func(reg *widget.Registration)
		wantErrSubstring string
	}{
		{
			name: "empty Type",
			mutate: func(reg *widget.Registration) {
				reg.Type = ""
			},
			wantErrSubstring: "Type",
		},
		{
			name: "nil New",
			mutate: func(reg *widget.Registration) {
				reg.New = nil
			},
			wantErrSubstring: "New",
		},
		{
			name: "nil DefaultConfig",
			mutate: func(reg *widget.Registration) {
				reg.DefaultConfig = nil
			},
			wantErrSubstring: "DefaultConfig",
		},
		{
			name: "nil ValidateConfig",
			mutate: func(reg *widget.Registration) {
				reg.ValidateConfig = nil
			},
			wantErrSubstring: "ValidateConfig",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := widget.NewRegistry()
			reg := newTestRegistration(t, "alpha")
			tt.mutate(&reg)

			err := r.Register(reg)
			if err == nil {
				t.Fatalf("Register returned nil error, want non-nil")
			}
			if !strings.Contains(err.Error(), tt.wantErrSubstring) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrSubstring)
			}
		})
	}
}

func TestList_Ordering(t *testing.T) {
	r := widget.NewRegistry()
	for _, name := range []string{"c", "a", "b"} {
		if err := r.Register(newTestRegistration(t, name)); err != nil {
			t.Fatalf("Register(%q) returned error: %v", name, err)
		}
	}

	got := r.List()
	if len(got) != 3 {
		t.Fatalf("List() len = %d, want 3", len(got))
	}
	want := []string{"a", "b", "c"}
	for i, w := range want {
		if got[i].Type != w {
			t.Errorf("List()[%d].Type = %q, want %q", i, got[i].Type, w)
		}
	}
}

func TestList_EmptyRegistry(t *testing.T) {
	r := widget.NewRegistry()

	got := r.List()
	if got == nil {
		t.Fatalf("List() = nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("List() len = %d, want 0", len(got))
	}
}

func TestValidate_UnknownType(t *testing.T) {
	r := widget.NewRegistry()

	_, err := r.Validate("nope", []byte("{}"))
	if err == nil {
		t.Fatalf("Validate returned nil error, want non-nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "unknown type") {
		t.Errorf("error %q does not contain %q", msg, "unknown type")
	}
	if !strings.Contains(msg, "nope") {
		t.Errorf("error %q does not contain %q", msg, "nope")
	}
}

func TestValidate_ForwardsErrors(t *testing.T) {
	sentinel := errors.New("boom")
	r := widget.NewRegistry()
	reg := newTestRegistration(t, "alpha")
	reg.ValidateConfig = func(raw []byte) (widget.Instance, error) {
		return widget.Instance{}, sentinel
	}
	if err := r.Register(reg); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	_, err := r.Validate("alpha", []byte("{}"))
	if !errors.Is(err, sentinel) {
		t.Errorf("Validate err = %v, want errors.Is sentinel %v", err, sentinel)
	}
}

func TestRender_UnknownType(t *testing.T) {
	r := widget.NewRegistry()

	component, err := r.Render(context.Background(), "nope", []byte("{}"), themes.Theme{})
	if component != nil {
		t.Errorf("Render returned non-nil component, want nil")
	}
	if err == nil {
		t.Fatalf("Render returned nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("error %q does not contain %q", err.Error(), "unknown type")
	}
}

func TestRender_FailingValidator(t *testing.T) {
	sentinel := errors.New("bad config")
	r := widget.NewRegistry()
	reg := newTestRegistration(t, "alpha")
	reg.ValidateConfig = func(raw []byte) (widget.Instance, error) {
		return widget.Instance{}, sentinel
	}
	if err := r.Register(reg); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	component, err := r.Render(context.Background(), "alpha", []byte("{}"), themes.Theme{})
	if component != nil {
		t.Errorf("Render returned non-nil component, want nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("Render err = %v, want errors.Is sentinel %v", err, sentinel)
	}
}

func TestRender_HappyPath(t *testing.T) {
	r := widget.NewRegistry()
	if err := r.Register(newTestRegistration(t, "alpha")); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	component, err := r.Render(context.Background(), "alpha", []byte("{}"), themes.Theme{})
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if component == nil {
		t.Errorf("Render returned nil component, want non-nil")
	}
}

func TestRender_SetsInstanceType(t *testing.T) {
	var captured string
	r := widget.NewRegistry()
	reg := newTestRegistration(t, "alpha")
	reg.ValidateConfig = func(raw []byte) (widget.Instance, error) {
		// Deliberately return Instance.Type == "" to verify the
		// registry overwrites it with the lookup key.
		return widget.Instance{Type: ""}, nil
	}
	reg.New = func() widget.Widget {
		return &fakeWidget{
			renderFn: func(ctx context.Context, instance widget.Instance, theme themes.Theme) templ.Component {
				captured = instance.Type
				return templ.NopComponent
			},
		}
	}
	if err := r.Register(reg); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	if _, err := r.Render(context.Background(), "alpha", []byte("{}"), themes.Theme{}); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if captured != "alpha" {
		t.Errorf("widget saw Instance.Type = %q, want %q", captured, "alpha")
	}
}

func TestGet_ConcurrentReadsAreRaceFree(t *testing.T) {
	r := widget.NewRegistry()
	if err := r.Register(newTestRegistration(t, "alpha")); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	const goroutines = 64
	const iterations = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if _, ok := r.Get("alpha"); !ok {
					t.Errorf("Get(\"alpha\") ok = false during concurrent read")
					return
				}
			}
		}()
	}
	wg.Wait()
}
