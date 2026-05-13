package widget

// Registration is the metadata + behaviour bundle for one widget type.
// Every widget package builds a Registration in its init() function and
// passes it to Default().Register or [MustRegister]. Tests build a fresh
// registry via [NewRegistry] and call its Register method directly.
//
// Forward-compatibility note: this struct may gain new optional fields in
// later specs (e.g. FontRole when Typography Roles ships). New fields
// MUST be optional with sensible zero-value semantics so existing widgets
// keep working.
type Registration struct {
	// Type is the stable identifier used as the JSON discriminator and
	// the database column value. Lowercase ASCII, no spaces. Examples:
	// "text", "time", "weather". Must be unique within a registry.
	Type string

	// DisplayName is the human-readable label shown in the picker UI.
	// Example: "Text".
	DisplayName string

	// Description is a one-sentence explanation shown alongside the
	// picker entry. Example: "Display a configurable block of text.".
	Description string

	// New returns a Widget implementation. For stateless widget types
	// this typically returns a pointer to a singleton; widgets that
	// hold per-instance state MAY return a fresh value per call. The
	// registry calls New each time it needs to render, so the call
	// MUST be cheap.
	New func() Widget

	// DefaultConfig returns the JSON bytes representing a sensible
	// starting configuration. Used by the picker UI to pre-populate a
	// new instance form. The returned bytes MUST satisfy ValidateConfig
	// without error -- a default that fails validation is a build-time
	// bug.
	DefaultConfig func() []byte

	// ValidateConfig parses and validates the per-instance JSON config.
	// On success it returns an Instance whose Config field carries the
	// typed, validated payload (the concrete type is private to the
	// widget's own package). On failure it returns the zero Instance
	// and a non-nil error. ValidateConfig MUST NOT panic.
	ValidateConfig func(raw []byte) (Instance, error)
}
