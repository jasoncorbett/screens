package widget

import "sync"

// defaultRegistry is the process-wide singleton populated by init()
// functions in widget packages.
var (
	defaultOnce sync.Once
	defaultReg  *Registry
)

// Default returns the process-wide widget registry. Widget packages
// call Default().Register(...) (or [MustRegister]) from init().
// Application code (main.go, Screen Display) reads from it. Tests
// SHOULD use [NewRegistry] instead so they do not mutate global state.
func Default() *Registry {
	defaultOnce.Do(func() {
		defaultReg = NewRegistry()
	})
	return defaultReg
}

// MustRegister registers reg with the default registry, panicking on
// error. Intended to be called from widget package init() functions
// where a duplicate-type or missing-field error is a build-time bug.
// Plain Register on the registry returned by [Default] is also fine;
// this is sugar.
func MustRegister(reg Registration) {
	if err := Default().Register(reg); err != nil {
		panic(err)
	}
}
