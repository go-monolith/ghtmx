package auth

import (
	"context"
	"errors"
)

// ctxKey keys this package's context values. A single unexported key
// type means every framework's glue stores and reads identity through
// the same slots — there are no cross-package keys to collide.
type ctxKey int

const (
	identityKey ctxKey = iota
	csrfKey
)

// ContextWithIdentity returns a context carrying identity, as the
// session middleware installs it after a successful Authenticate. It is
// exported so handler tests can build an authenticated context without
// running the middleware.
func ContextWithIdentity(ctx context.Context, identity any) context.Context {
	return context.WithValue(ctx, identityKey, identity)
}

// ContextWithCSRFToken returns a context carrying the per-session CSRF
// token, for handler tests that exercise [VerifyCSRFToken] paths.
func ContextWithCSRFToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, csrfKey, token)
}

// IdentityFrom returns the identity the session middleware stored for
// this request. ok is false when the middleware is not installed ahead
// of the caller or ID is not the type the Authenticator returned —
// both are wiring bugs, and downstream guards must refuse the request
// (and say why in the log), never treat it as anonymous.
func IdentityFrom[ID any](ctx context.Context) (ID, bool) {
	id, ok := ctx.Value(identityKey).(ID)
	return id, ok
}

// CSRFTokenFrom returns the per-session CSRF token the session
// middleware derived for this request — the value templates embed via
// ghtmx.CSRFHeader or a hidden form field. ok is false when the session
// middleware is not installed ahead of the caller.
func CSRFTokenFrom(ctx context.Context) (string, bool) {
	tok, ok := ctx.Value(csrfKey).(string)
	return tok, ok
}

// AuthenticateToken is the framework-neutral session step shared by
// [Middleware] and the fiber glue: it authenticates the raw cookie
// token and, on success, returns a context carrying the identity and
// the derived per-session CSRF token. The error is the Authenticator's,
// unwrapped — callers branch on [ErrUnauthorized].
func AuthenticateToken[ID any](ctx context.Context, cfg Config[ID], token string) (context.Context, ID, error) {
	var zero ID
	if cfg.Authenticator == nil {
		return ctx, zero, errors.New("auth: Config.Authenticator is nil")
	}
	id, err := cfg.Authenticator.Authenticate(ctx, token)
	if err != nil {
		return ctx, zero, err
	}
	ctx = ContextWithIdentity(ctx, id)
	ctx = ContextWithCSRFToken(ctx, csrfTokenForSession(token))
	return ctx, id, nil
}
