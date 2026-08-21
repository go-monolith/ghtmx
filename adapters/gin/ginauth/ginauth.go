// Package ginauth is the gin glue for the ghtmx auth package: secure
// cookie session authentication and CSRF middleware as gin handlers.
// It delegates to the auth package's engine functions through gin's
// writer and request, so cookie handling, htmx-aware login redirects,
// and token comparison are identical to the net/http middleware by
// construction. Session policy lives in auth.Config; see that package
// for the security model. No core package depends on this one
// (constitution A5).
package ginauth

import (
	"net/http"

	ginfw "github.com/gin-gonic/gin"

	"github.com/go-monolith/ghtmx/auth"
)

// New returns the session middleware: it authenticates the session
// cookie and either installs the identity and per-session CSRF token in
// the request context — read them with [IdentityFrom] and
// auth.CSRFTokenFrom — or answers the request itself (login redirect,
// cookie clear, or 500; auth.Require documents the outcomes) and aborts
// the chain.
//
// New panics if cfg is invalid — see auth.Config.Validate.
func New[ID any](cfg auth.Config[ID]) ginfw.HandlerFunc {
	if err := cfg.Validate(); err != nil {
		panic(err)
	}
	return func(c *ginfw.Context) {
		r2, ok := auth.Require(cfg, c.Writer, c.Request)
		if !ok {
			c.Abort()
			return
		}
		c.Request = r2
	}
}

// CSRF returns the CSRF middleware. Install it after [New]: GET, HEAD,
// and OPTIONS pass through; every other method must carry the session's
// CSRF token in the ghtmx.DefaultCSRFHeaderName header or the
// auth.DefaultCSRFFormField form field, or the request is rejected with
// a 403. Without [New] ahead of it, every unsafe request is rejected —
// the layer fails closed.
//
// The options are the core package's, so one auth.WithOnReject hook
// works here and behind every other adapter unchanged.
func CSRF(opts ...auth.CSRFOption) ginfw.HandlerFunc {
	o := auth.NewCSRFOptions(opts...)
	return func(c *ginfw.Context) {
		if auth.SafeMethod(c.Request.Method) {
			return
		}
		if err := auth.VerifyCSRF(c.Request); err != nil {
			r := c.Request
			o.Report(r.Context(), auth.CSRFRejection{Method: r.Method, Path: r.URL.Path, Err: err})
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
	}
}

// IdentityFrom returns the identity [New] stored for this request. ok
// is false when the middleware is not installed ahead of the caller or
// ID is not the Authenticator's type — both wiring bugs; refuse the
// request and log why, never treat it as anonymous.
func IdentityFrom[ID any](c *ginfw.Context) (ID, bool) {
	return auth.IdentityFrom[ID](c.Request.Context())
}

// SetSessionCookie sets the session cookie after a successful sign-in.
func SetSessionCookie[ID any](c *ginfw.Context, cfg auth.Config[ID], token string) {
	auth.SetSessionCookie(c.Writer, cfg, token)
}

// ClearSessionCookie deletes the session cookie — sign-out, or a dead
// session.
func ClearSessionCookie[ID any](c *ginfw.Context, cfg auth.Config[ID]) {
	auth.ClearSessionCookie(c.Writer, cfg)
}

// SetLoginCSRFCookie mints the login form's pre-session double-submit
// token and returns the value the form embeds in a hidden field; see
// auth.SetLoginCSRFCookie for why the login form needs its own token.
func SetLoginCSRFCookie[ID any](c *ginfw.Context, cfg auth.Config[ID]) (string, error) {
	return auth.SetLoginCSRFCookie(c.Writer, cfg)
}

// ValidLoginCSRF reports whether the login form submission carries the
// token [SetLoginCSRFCookie] set, in constant time.
func ValidLoginCSRF[ID any](c *ginfw.Context, cfg auth.Config[ID], submitted string) bool {
	return auth.ValidLoginCSRF(c.Request, cfg, submitted)
}
