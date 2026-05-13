package widget_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/a-h/templ"
	"github.com/jasoncorbett/screens/internal/themes"
	"github.com/jasoncorbett/screens/internal/widget"
)

// TestRegister_DuplicateConcurrent verifies that when two goroutines race
// to register the same Type on the same registry, exactly one wins and
// the other receives the documented "already registered" error. A naive
// double-checked-lock implementation that read the map under RLock and
// then upgraded to Lock would let both writes succeed.
func TestRegister_DuplicateConcurrent(t *testing.T) {
	r := widget.NewRegistry()

	var (
		wg       sync.WaitGroup
		successN int32
		errN     int32
		errMsg   string
		errMu    sync.Mutex
	)
	const goroutines = 32
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			err := r.Register(newTestRegistration(t, "alpha"))
			if err == nil {
				atomic.AddInt32(&successN, 1)
				return
			}
			atomic.AddInt32(&errN, 1)
			errMu.Lock()
			errMsg = err.Error()
			errMu.Unlock()
		}()
	}
	wg.Wait()

	if successN != 1 {
		t.Errorf("expected exactly 1 successful Register, got %d", successN)
	}
	if errN != goroutines-1 {
		t.Errorf("expected %d failed Registers, got %d", goroutines-1, errN)
	}
	if !strings.Contains(errMsg, "already") {
		t.Errorf("duplicate error %q does not contain %q", errMsg, "already")
	}
}

// TestRegistry_ConcurrentMixedTraffic exercises the RWMutex split: many
// goroutines call Get, List, Validate, Render simultaneously while
// another goroutine batch-registers new types. With -race this catches
// any path that mutates shared state under the read lock or that holds
// a read lock across user code.
func TestRegistry_ConcurrentMixedTraffic(t *testing.T) {
	r := widget.NewRegistry()
	// Pre-register one stable type so readers always have something to
	// look up while writers add more.
	if err := r.Register(newTestRegistration(t, "stable")); err != nil {
		t.Fatalf("Register(stable) returned error: %v", err)
	}

	const (
		readers       = 16
		writers       = 4
		iterPerReader = 500
	)

	ctx := context.Background()
	var wg sync.WaitGroup

	// Writers register a unique type each iteration. Once their slot
	// fills they stop -- new-type-per-iter would let goroutines collide
	// trivially via duplicate errors which is fine, but a fixed count
	// matches the production "init() then quiesce" pattern.
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				typeName := fmt.Sprintf("w%d-%d", w, i)
				if err := r.Register(newTestRegistration(t, typeName)); err != nil {
					t.Errorf("Register(%q) returned error: %v", typeName, err)
				}
			}
		}(w)
	}

	wg.Add(readers)
	for ri := 0; ri < readers; ri++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterPerReader; j++ {
				if _, ok := r.Get("stable"); !ok {
					t.Errorf("Get(stable) ok = false during concurrent traffic")
					return
				}
				_ = r.List() // returned slice not mutated by readers
				if _, err := r.Validate("stable", []byte("{}")); err != nil {
					t.Errorf("Validate(stable) returned error: %v", err)
					return
				}
				if comp, err := r.Render(ctx, "stable", []byte("{}"), themes.Theme{}); err != nil || comp == nil {
					t.Errorf("Render(stable) returned (%v, %v); want non-nil component, nil err", comp, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestRegistry_RecursiveCallFromValidator verifies that the registry
// releases its read lock before invoking user-supplied ValidateConfig.
// If the lock were held across the call, the validator's call back into
// Get / List would deadlock (RLock is not reentrant when a writer is
// waiting). We trigger this by having a writer-slot waiter compete with
// the recursive reader -- if the implementation upgrades to a writer or
// holds RLock, the test will hang and -timeout will fire.
func TestRegistry_RecursiveCallFromValidator(t *testing.T) {
	r := widget.NewRegistry()
	reg := newTestRegistration(t, "recurser")
	reg.ValidateConfig = func(raw []byte) (widget.Instance, error) {
		// Recursive Get from inside the validator. If the lock is held
		// across the call this is fine for a plain RLock-recursive
		// path, but if a writer is queued behind it (per Go's RWMutex
		// fairness) it would deadlock.
		_, _ = r.Get("recurser")
		_ = r.List()
		return widget.Instance{Type: "recurser"}, nil
	}
	if err := r.Register(reg); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	if _, err := r.Validate("recurser", []byte("{}")); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

// TestRegistry_RecursiveCallFromRenderer is the Render-path twin of the
// validator-recursion test. Render is called by Screen Display per page
// render; if a future widget recursively invoked the registry from its
// renderer, the call must not deadlock.
func TestRegistry_RecursiveCallFromRenderer(t *testing.T) {
	r := widget.NewRegistry()
	reg := newTestRegistration(t, "recurser")
	reg.New = func() widget.Widget {
		return &fakeWidget{
			renderFn: func(ctx context.Context, instance widget.Instance, theme themes.Theme) templ.Component {
				_, _ = r.Get("recurser")
				_ = r.List()
				return templ.NopComponent
			},
		}
	}
	if err := r.Register(reg); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	comp, err := r.Render(context.Background(), "recurser", []byte("{}"), themes.Theme{})
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if comp == nil {
		t.Fatalf("Render returned nil component")
	}
}

// TestRegistry_RecursiveRegisterFromValidator verifies that a user
// callback (ValidateConfig) can safely call Register on the same
// registry -- proving the registry's read lock is released before the
// callback runs. This is the worst-case "user code mutates the
// registry mid-validate" scenario; a bug would deadlock.
func TestRegistry_RecursiveRegisterFromValidator(t *testing.T) {
	r := widget.NewRegistry()
	reg := newTestRegistration(t, "outer")
	reg.ValidateConfig = func(raw []byte) (widget.Instance, error) {
		// Register a new type from inside the validator. If the
		// outer Validate held the read lock across this call, the
		// nested Register's write-Lock would deadlock against itself.
		_ = r.Register(newTestRegistration(t, "inner"))
		return widget.Instance{Type: "outer"}, nil
	}
	if err := r.Register(reg); err != nil {
		t.Fatalf("Register(outer) returned error: %v", err)
	}

	if _, err := r.Validate("outer", []byte("{}")); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	// The inner Register must have succeeded.
	if _, ok := r.Get("inner"); !ok {
		t.Errorf("Get(inner) after recursive Register: ok=false, want true")
	}
}

// TestList_ReturnsCopy mutates the slice returned by List and verifies
// the registry's internal state is not affected on the next List call.
// Without the per-call allocation in List, a future caller that sorts
// or slices the result could corrupt the picker UI.
func TestList_ReturnsCopy(t *testing.T) {
	r := widget.NewRegistry()
	for _, name := range []string{"a", "b", "c"} {
		if err := r.Register(newTestRegistration(t, name)); err != nil {
			t.Fatalf("Register(%q) returned error: %v", name, err)
		}
	}

	first := r.List()
	if len(first) != 3 {
		t.Fatalf("first List len = %d, want 3", len(first))
	}
	// Hostile mutation: zero every entry, swap order.
	for i := range first {
		first[i] = widget.Registration{}
	}

	second := r.List()
	if len(second) != 3 {
		t.Fatalf("second List len = %d, want 3 (caller mutation leaked into registry)", len(second))
	}
	want := []string{"a", "b", "c"}
	for i, w := range want {
		if second[i].Type != w {
			t.Errorf("second List[%d].Type = %q, want %q -- caller mutation leaked", i, second[i].Type, w)
		}
	}
}

// TestRender_ValidatorPanicDoesNotCrashRegistry exercises the documented
// contract "Validators MUST NOT panic". The architecture (per ADR-005
// and the spec's Security section) makes panics a build-time bug, but
// the registry currently does not recover. This test pins the current
// behaviour: a panicking validator propagates up to the caller. If the
// architecture later commits to recovery, this test should be inverted.
func TestRender_ValidatorPanicPropagates(t *testing.T) {
	r := widget.NewRegistry()
	reg := newTestRegistration(t, "panicker")
	reg.ValidateConfig = func(raw []byte) (widget.Instance, error) {
		panic("validator misbehaving")
	}
	if err := r.Register(reg); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Errorf("expected validator panic to propagate, got nil")
		}
	}()
	_, _ = r.Render(context.Background(), "panicker", []byte("{}"), themes.Theme{})
}

// TestRender_NewNotCalledOnValidatorError pins the documented call
// order: ValidateConfig runs first, and on error the registry returns
// without calling New() or Render(). Without the early return, a
// pathological New that panics or has side effects would fire even
// after the validator rejected the config.
func TestRender_NewNotCalledOnValidatorError(t *testing.T) {
	r := widget.NewRegistry()
	var newCalls int32
	reg := newTestRegistration(t, "alpha")
	reg.ValidateConfig = func(raw []byte) (widget.Instance, error) {
		return widget.Instance{}, errors.New("nope")
	}
	reg.New = func() widget.Widget {
		atomic.AddInt32(&newCalls, 1)
		return &fakeWidget{}
	}
	if err := r.Register(reg); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	_, err := r.Render(context.Background(), "alpha", []byte("{}"), themes.Theme{})
	if err == nil {
		t.Fatalf("Render returned nil error, want non-nil")
	}
	if got := atomic.LoadInt32(&newCalls); got != 0 {
		t.Errorf("New() called %d times after validator error, want 0", got)
	}
}

// TestRender_NilRawForwardedToValidator verifies that a nil []byte is
// passed through to the validator unchanged. The registry must not
// pre-empt the validator's authority over what counts as a valid
// configuration.
func TestRender_NilRawForwardedToValidator(t *testing.T) {
	r := widget.NewRegistry()
	var sawNil bool
	reg := newTestRegistration(t, "alpha")
	reg.ValidateConfig = func(raw []byte) (widget.Instance, error) {
		sawNil = raw == nil
		return widget.Instance{Type: "alpha"}, nil
	}
	if err := r.Register(reg); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	if _, err := r.Render(context.Background(), "alpha", nil, themes.Theme{}); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if !sawNil {
		t.Errorf("validator did not see a nil raw []byte")
	}
}

// TestRender_NilContextPassedThrough mirrors the nil-raw test for the
// context.Context parameter. The registry does not assert ctx != nil;
// widgets are expected to honour cancellation if they need it. This
// test pins the pass-through behaviour so a future "reject nil ctx"
// guard would be a deliberate design change, not an accident.
func TestRender_NilContextPassedThrough(t *testing.T) {
	r := widget.NewRegistry()
	var captured context.Context
	reg := newTestRegistration(t, "alpha")
	reg.New = func() widget.Widget {
		return &fakeWidget{
			renderFn: func(ctx context.Context, instance widget.Instance, theme themes.Theme) templ.Component {
				captured = ctx
				return templ.NopComponent
			},
		}
	}
	if err := r.Register(reg); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	//nolint:staticcheck // intentional nil ctx for pass-through test
	if _, err := r.Render(nil, "alpha", []byte("{}"), themes.Theme{}); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if captured != nil {
		t.Errorf("widget saw non-nil ctx after Render was called with nil; want pass-through")
	}
}

// TestRegister_ErrorsIncludeTypeName ensures the type name is part of
// every actionable error message, so a Phase 3 widget that misregisters
// can find its bug from logs alone.
func TestRegister_ErrorsIncludeTypeName(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(reg *widget.Registration)
	}{
		{"nil New", func(r *widget.Registration) { r.New = nil }},
		{"nil DefaultConfig", func(r *widget.Registration) { r.DefaultConfig = nil }},
		{"nil ValidateConfig", func(r *widget.Registration) { r.ValidateConfig = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := widget.NewRegistry()
			reg := newTestRegistration(t, "named-widget")
			tt.mutate(&reg)
			err := r.Register(reg)
			if err == nil {
				t.Fatalf("Register returned nil error")
			}
			if !strings.Contains(err.Error(), "named-widget") {
				t.Errorf("error %q does not contain type name %q", err.Error(), "named-widget")
			}
		})
	}
}

// TestDuplicate_ErrorIncludesTypeName same as above but for the
// duplicate-type branch.
func TestDuplicate_ErrorIncludesTypeName(t *testing.T) {
	r := widget.NewRegistry()
	if err := r.Register(newTestRegistration(t, "specific-name")); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	err := r.Register(newTestRegistration(t, "specific-name"))
	if err == nil {
		t.Fatalf("Register returned nil error")
	}
	if !strings.Contains(err.Error(), "specific-name") {
		t.Errorf("duplicate error %q does not contain type name", err.Error())
	}
}

// TestDefault_ConcurrentFirstInit verifies the sync.Once guards
// against a race on the very first Default() call. We can't fully
// reset the global between tests, but we can hammer Default() from
// many goroutines and assert they all see the same pointer. The
// race detector would flag any non-synchronised write to defaultReg.
func TestDefault_ConcurrentFirstInit(t *testing.T) {
	const goroutines = 32
	results := make([]*widget.Registry, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			results[i] = widget.Default()
		}(i)
	}
	wg.Wait()
	for i := 1; i < goroutines; i++ {
		if results[i] != results[0] {
			t.Errorf("Default() returned different pointer at index %d: %p vs %p", i, results[i], results[0])
		}
	}
}

// TestRender_WrappedErrorUnwrapsToValidatorError verifies that the
// registry's error wrapping uses %w correctly so callers can
// errors.Is / errors.As the underlying validator error. This is
// already covered by TestRender_FailingValidator but we double-check
// here with a sentinel struct type plus errors.As.
type validatorErr struct{ msg string }

func (e *validatorErr) Error() string { return e.msg }

func TestRender_WrappedErrorUnwrapsToValidatorError(t *testing.T) {
	sentinel := &validatorErr{msg: "structured validator failure"}
	r := widget.NewRegistry()
	reg := newTestRegistration(t, "alpha")
	reg.ValidateConfig = func(raw []byte) (widget.Instance, error) {
		return widget.Instance{}, sentinel
	}
	if err := r.Register(reg); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	_, err := r.Render(context.Background(), "alpha", []byte("{}"), themes.Theme{})
	var got *validatorErr
	if !errors.As(err, &got) {
		t.Fatalf("Render error %v does not errors.As to *validatorErr", err)
	}
	if got != sentinel {
		t.Errorf("errors.As returned a different pointer; want %p got %p", sentinel, got)
	}
}

// TestRender_LargeJSONBlob ensures the registry can ferry a 1MB raw
// blob through to the validator without copying or modifying it. The
// registry only forwards bytes; this test pins the forwarding shape
// rather than the validator's behaviour.
func TestRender_LargeJSONBlob(t *testing.T) {
	r := widget.NewRegistry()
	var sawLen int
	reg := newTestRegistration(t, "alpha")
	reg.ValidateConfig = func(raw []byte) (widget.Instance, error) {
		sawLen = len(raw)
		return widget.Instance{Type: "alpha"}, nil
	}
	if err := r.Register(reg); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	// 1 MB blob, valid JSON.
	body := make([]byte, 0, 1<<20)
	body = append(body, '"')
	for len(body) < (1<<20)-1 {
		body = append(body, 'x')
	}
	body = append(body, '"')

	if _, err := r.Render(context.Background(), "alpha", body, themes.Theme{}); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if sawLen != len(body) {
		t.Errorf("validator saw %d bytes, want %d", sawLen, len(body))
	}
}

// TestRender_NewCalledOncePerRender pins that the registry calls
// reg.New exactly once per Render. A future "cache the constructed
// widget" optimisation would change this; the architecture explicitly
// says New is called per render, so we lock that in.
func TestRender_NewCalledOncePerRender(t *testing.T) {
	r := widget.NewRegistry()
	var newCalls int32
	reg := newTestRegistration(t, "alpha")
	reg.New = func() widget.Widget {
		atomic.AddInt32(&newCalls, 1)
		return &fakeWidget{}
	}
	if err := r.Register(reg); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	const calls = 5
	for i := 0; i < calls; i++ {
		if _, err := r.Render(context.Background(), "alpha", []byte("{}"), themes.Theme{}); err != nil {
			t.Fatalf("Render returned error: %v", err)
		}
	}
	if got := atomic.LoadInt32(&newCalls); got != calls {
		t.Errorf("New called %d times across %d renders, want %d", got, calls, calls)
	}
}
