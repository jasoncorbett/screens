package auth

// IdentityKind distinguishes between the two ways a request can be
// authenticated.
type IdentityKind int

const (
	// IdentityNone indicates the request is unauthenticated.
	IdentityNone IdentityKind = iota
	// IdentityAdmin indicates the request was authenticated via an admin
	// session cookie.
	IdentityAdmin
	// IdentityDevice indicates the request was authenticated via a device
	// bearer token (header or cookie).
	IdentityDevice
)

// Identity is the unified authentication value injected into the request
// context by RequireAuth. Exactly one of User or Device is non-nil whenever
// Kind != IdentityNone.
type Identity struct {
	Kind   IdentityKind
	User   *User
	Device *Device
}

// ID returns a stable string identifier for the caller, regardless of kind.
// Returns "" when the identity is incomplete (Kind unknown, or the matching
// pointer is nil).
func (i Identity) ID() string {
	switch i.Kind {
	case IdentityAdmin:
		if i.User != nil {
			return "user:" + i.User.ID
		}
	case IdentityDevice:
		if i.Device != nil {
			return "device:" + i.Device.ID
		}
	}
	return ""
}

// IsAdmin reports whether this identity represents an admin user.
func (i Identity) IsAdmin() bool { return i.Kind == IdentityAdmin }

// IsDevice reports whether this identity represents a device.
func (i Identity) IsDevice() bool { return i.Kind == IdentityDevice }
