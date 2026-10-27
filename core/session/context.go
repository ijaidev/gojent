package session

import "context"

// contextKey is unexported so session values cannot collide with other
// packages' context keys.
type contextKey struct{}

// NewContext returns a copy of ctx carrying s. The agent runner installs the
// run's session this way, which lets policies, tools, and provider header
// functions read the current conversation identity.
func NewContext(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, contextKey{}, s)
}

// FromContext returns the session carried by ctx, if any.
func FromContext(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(contextKey{}).(Session)
	return s, ok
}
