package auth

import "context"

type userKey struct{}
type sessionKey struct{}

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
