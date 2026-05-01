package auth

import "context"

type userKey struct{}
type sessionKey struct{}
type identityKey struct{}
type deviceKey struct{}

// ContextWithUser returns a context carrying the authenticated user.
func ContextWithUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

// UserFromContext extracts the user from the context. Returns nil if absent.
func UserFromContext(ctx context.Context) *User {
	u, _ := ctx.Value(userKey{}).(*User)
	return u
}

// ContextWithSession returns a context carrying the session.
func ContextWithSession(ctx context.Context, session *Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, session)
}

// SessionFromContext extracts the session from the context. Returns nil if absent.
func SessionFromContext(ctx context.Context) *Session {
	s, _ := ctx.Value(sessionKey{}).(*Session)
	return s
}

// ContextWithIdentity returns a context carrying the unified Identity value.
func ContextWithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFromContext extracts the Identity from the context. Returns nil if
// absent.
func IdentityFromContext(ctx context.Context) *Identity {
	i, _ := ctx.Value(identityKey{}).(*Identity)
	return i
}

// ContextWithDevice returns a context carrying the authenticated device.
func ContextWithDevice(ctx context.Context, d *Device) context.Context {
	return context.WithValue(ctx, deviceKey{}, d)
}

// DeviceFromContext extracts the device from the context. Returns nil if
// absent.
func DeviceFromContext(ctx context.Context) *Device {
	d, _ := ctx.Value(deviceKey{}).(*Device)
	return d
}
