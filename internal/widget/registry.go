package widget

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/a-h/templ"
	"github.com/jasoncorbett/screens/internal/themes"
)

// Registry maps widget-type identifiers to Registrations. Production
// code uses the singleton returned by [Default]; tests build a fresh
// Registry via [NewRegistry] to avoid global state.
//
// The Registry is read-mostly: writes (registrations) happen
// exclusively from init() functions in widget packages. After init(),
// reads are safe across goroutines without external synchronisation.
// The internal mutex is a defensive guard against accidental late
// registrations -- the expected pattern is "everything registers in
// init(), then nothing registers ever again".
type Registry struct {
	mu    sync.RWMutex
	items map[string]Registration
}

// NewRegistry returns an empty Registry suitable for tests. Each test
// that touches a widget should build its own registry, register the
// widget(s) under test, and exercise the registry methods directly.
// Tests MUST NOT mutate the global singleton returned by [Default].
func NewRegistry() *Registry {
	return &Registry{items: map[string]Registration{}}
}

// Register adds reg to the registry. Returns a non-nil error if any
// required field of reg is missing, or if a widget with the same Type
// is already registered. The error is descriptive enough to be panicked
// by the calling init() function -- a duplicate type or missing field
// is a build-time bug, not a runtime condition.
func (r *Registry) Register(reg Registration) error {
	if reg.Type == "" {
		return fmt.Errorf("widget registration: Type must not be empty")
	}
	if reg.New == nil {
		return fmt.Errorf("widget registration %q: New must not be nil", reg.Type)
	}
	if reg.DefaultConfig == nil {
		return fmt.Errorf("widget registration %q: DefaultConfig must not be nil", reg.Type)
	}
	if reg.ValidateConfig == nil {
		return fmt.Errorf("widget registration %q: ValidateConfig must not be nil", reg.Type)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[reg.Type]; exists {
		return fmt.Errorf("widget registration: type %q already registered", reg.Type)
	}
	r.items[reg.Type] = reg
	return nil
}

// Get returns the Registration for the given type, or the zero
// Registration and false if no such type is registered. Safe for
// concurrent use.
func (r *Registry) Get(typeName string) (Registration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	reg, ok := r.items[typeName]
	return reg, ok
}

// List returns every registered widget, deterministically ordered by
// Type. The picker UI iterates this slice. Returns an empty slice
// (never nil) when the registry is empty.
func (r *Registry) List() []Registration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Registration, 0, len(r.items))
	for _, reg := range r.items {
		out = append(out, reg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// Validate is a convenience wrapper that resolves the type, runs its
// ValidateConfig, and returns the resulting Instance. Returns a
// wrapping error if the type is unknown. The internal lookup releases
// the read lock before invoking user-supplied validator code.
func (r *Registry) Validate(typeName string, raw []byte) (Instance, error) {
	reg, ok := r.Get(typeName)
	if !ok {
		return Instance{}, fmt.Errorf("widget: unknown type %q", typeName)
	}
	return reg.ValidateConfig(raw)
}

// Render is the call Screen Display will make per widget instance per
// page render. It resolves the type, validates the config, constructs
// a Widget via Registration.New, and delegates to the Widget's Render
// method. Returns a nil component plus a wrapping error if the type is
// unknown or the config fails validation. The lookup releases the read
// lock before invoking user-supplied validator and render code, so
// those callbacks may safely call back into the registry without
// deadlocking.
func (r *Registry) Render(ctx context.Context, typeName string, raw []byte, theme themes.Theme) (templ.Component, error) {
	reg, ok := r.Get(typeName)
	if !ok {
		return nil, fmt.Errorf("widget: unknown type %q", typeName)
	}
	inst, err := reg.ValidateConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("widget %q: validate config: %w", typeName, err)
	}
	inst.Type = typeName
	return reg.New().Render(ctx, inst, theme), nil
}
