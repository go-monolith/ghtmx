// Package echoauth is the echo glue for the ghtmx auth package: secure
// cookie session authentication and CSRF middleware as echo middleware.
// It delegates to the auth package's engine functions through echo's
// response and request, so cookie handling, htmx-aware login redirects,
// and token comparison are identical to the net/http middleware by
// construction. Session policy lives in auth.Config; see that package
// for the security model. No core package depends on this one
// (constitution A5).
package echoauth

import (
	"net/http"

	echofw "github.com/labstack/echo/v4"

	"github.com/go-monolith/ghtmx/auth"
)

// New returns the session middleware: it authenticates the session
// cookie and either installs the identity and per-session CSRF token in
// the request context — read them with [IdentityFrom] and
// auth.CSRFTokenFrom — or answers the request itself (login redirect,
// cookie clear, or 500; auth.Require documents the outcomes) and ends
// the chain.
//
// New panics if cfg is invalid — see auth.Config.Validate.
func New[ID any](cfg auth.Config[ID]) echofw.MiddlewareFunc {
	if err := cfg.Validate(); err != nil {
		panic(err)
	}
	return func(next echofw.HandlerFunc) echofw.HandlerFunc {
		return func(c echofw.Context) error {
			r2, ok := auth.Require(cfg, c.Response(), c.Request())
			if !ok {
				// The response is already written; returning an error
				// would make echo's error handler write a second one.
				return nil
			}
			c.SetRequest(r2)
			return next(c)
		}
	}
}

// CSRF returns the CSRF middleware. Install it after [New]: the methods
// auth.DefaultSafeMethods names — GET, HEAD, OPTIONS, and QUERY — pass
// through; every other method must carry the session's CSRF token in
// the ghtmx.DefaultCSRFHeaderName header or the
// auth.DefaultCSRFFormField form field, or the request is rejected with
// a 403. Without [New] ahead of it, every unsafe request is rejected —
// the layer fails closed.
//
// The options are the core package's, so one auth.WithOnReject hook —
// or one auth.WithSafeMethods list, which is how an application
// declines the QUERY exemption — works here and behind every other
// adapter unchanged.
func CSRF(opts ...auth.CSRFOption) echofw.MiddlewareFunc {
	o := auth.NewCSRFOptions(opts...)
	return func(next echofw.HandlerFunc) echofw.HandlerFunc {
		return func(c echofw.Context) error {
			if o.SafeMethod(c.Request().Method) {
				return next(c)
			}
			if err := auth.VerifyCSRF(c.Request()); err != nil {
				r := c.Request()
				o.Report(r.Context(), auth.CSRFRejection{Method: r.Method, Path: r.URL.Path, Err: err})
				return echofw.NewHTTPError(http.StatusForbidden)
			}
			return next(c)
		}
	}
}

// IdentityFrom returns the identity [New] stored for this request. ok
// is false when the middleware is not installed ahead of the caller or
// ID is not the Authenticator's type — both wiring bugs; refuse the
// request and log why, never treat it as anonymous.
func IdentityFrom[ID any](c echofw.Context) (ID, bool) {
	return auth.IdentityFrom[ID](c.Request().Context())
}

// SetSessionCookie sets the session cookie after a successful sign-in.
func SetSessionCookie[ID any](c echofw.Context, cfg auth.Config[ID], token string) {
	auth.SetSessionCookie(c.Response(), cfg, token)
}

// ClearSessionCookie deletes the session cookie — sign-out, or a dead
// session.
func ClearSessionCookie[ID any](c echofw.Context, cfg auth.Config[ID]) {
	auth.ClearSessionCookie(c.Response(), cfg)
}

// SetLoginCSRFCookie mints the login form's pre-session double-submit
// token and returns the value the form embeds in a hidden field; see
// auth.SetLoginCSRFCookie for why the login form needs its own token.
func SetLoginCSRFCookie[ID any](c echofw.Context, cfg auth.Config[ID]) (string, error) {
	return auth.SetLoginCSRFCookie(c.Response(), cfg)
}

// ValidLoginCSRF reports whether the login form submission carries the
// token [SetLoginCSRFCookie] set, in constant time.
func ValidLoginCSRF[ID any](c echofw.Context, cfg auth.Config[ID], submitted string) bool {
	return auth.ValidLoginCSRF(c.Request(), cfg, submitted)
}
