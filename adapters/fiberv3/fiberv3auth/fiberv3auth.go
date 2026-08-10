// Package fiberv3auth is the fiber v3 glue for the ghtmx auth package:
// secure cookie session authentication and CSRF middleware as fiber
// handlers. fiber runs on fasthttp, so unlike the gin and echo glue it
// does not delegate through net/http shapes: cookies, headers, and
// redirects use fiber's native API, while authentication, token
// comparison, and cookie policy delegate to the auth package's engine
// functions (auth.AuthenticateToken, auth.VerifyCSRFToken,
// auth.Config's cookie constructors), so behavior matches the net/http
// middleware by construction. Session policy lives in auth.Config; see
// that package for the security model. No core package depends on this
// one (constitution A5).
//
// It differs from adapters/fiber/fiberauth only where v3 changed the
// surface: fiber.Ctx is an interface rather than *fiber.Ctx, the
// request-scoped context accessors are Context/SetContext (v2's
// UserContext/SetUserContext), redirects go through c.Redirect(), and
// v3 serializes cookies via net/http, so deletion is the standard
// MaxAge -1 (v2 needs a past Expires). Keep the two files
// line-comparable, the way fiberv3.go mirrors fiber.go.
package fiberv3auth

import (
	"errors"
	"net/http"
	"strings"

	fiberfw "github.com/gofiber/fiber/v3"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/auth"
)

// New returns the session middleware: it authenticates the session
// cookie and either installs the identity and per-session CSRF token in
// the request's context — read them with [IdentityFrom] and
// auth.CSRFTokenFrom — or answers the request itself the way
// auth.Require documents (login redirect, cookie clear, or 500).
//
// New panics if cfg is invalid — see auth.Config.Validate.
func New[ID any](cfg auth.Config[ID]) fiberfw.Handler {
	if err := cfg.Validate(); err != nil {
		panic(err)
	}
	return func(c fiberfw.Ctx) error {
		// c.Cookies returns fiber's zero-copy string, aliasing the
		// fasthttp request buffer. The token crosses into the
		// application's Authenticator, whose framework-neutral contract
		// allows retaining it — clone so an Authenticator that caches
		// or logs the token never reads a recycled buffer.
		token := strings.Clone(c.Cookies(cfg.EffectiveCookieName()))
		if token == "" {
			return redirectToLogin(c, cfg.LoginURL)
		}
		ctx, _, err := auth.AuthenticateToken(c.Context(), cfg, token)
		if err != nil {
			if errors.Is(err, auth.ErrUnauthorized) {
				ClearSessionCookie(c, cfg)
				return redirectToLogin(c, cfg.LoginURL)
			}
			return c.SendStatus(fiberfw.StatusInternalServerError)
		}
		c.SetContext(ctx)
		return c.Next()
	}
}

// CSRF returns the CSRF middleware. Install it after [New]: GET, HEAD,
// and OPTIONS pass through; every other method must carry the session's
// CSRF token in the ghtmx.DefaultCSRFHeaderName header or the
// auth.DefaultCSRFFormField form field, or the request is rejected with
// a 403. Without [New] ahead of it, every unsafe request is rejected —
// the layer fails closed.
func CSRF() fiberfw.Handler {
	return func(c fiberfw.Ctx) error {
		if auth.SafeMethod(c.Method()) {
			return c.Next()
		}
		if err := auth.VerifyCSRFToken(c.Context(), submittedCSRF(c)); err != nil {
			return c.SendStatus(fiberfw.StatusForbidden)
		}
		return c.Next()
	}
}

// submittedCSRF extracts the CSRF token the request carries: header
// first (the stronger channel — browsers won't attach it cross-origin
// without a CORS preflight), then the form field. It deliberately never
// uses c.FormValue, whose fasthttp implementation consults the query
// string first — tokens must not ride in URLs, so only the POST body
// (urlencoded via PostArgs, multipart via MultipartForm) is read.
func submittedCSRF(c fiberfw.Ctx) string {
	if v := c.Get(ghtmx.DefaultCSRFHeaderName); v != "" {
		return v
	}
	if v := c.Request().PostArgs().Peek(auth.DefaultCSRFFormField); len(v) > 0 {
		return string(v)
	}
	if form, err := c.MultipartForm(); err == nil && form != nil {
		if vs := form.Value[auth.DefaultCSRFFormField]; len(vs) > 0 {
			return vs[0]
		}
	}
	return ""
}

// redirectToLogin mirrors auth.RedirectToLogin on fiber's native API:
// browser navigations get a 303, htmx requests get HX-Redirect with a
// 204 so htmx performs a full page load instead of swapping the login
// page into the target element.
func redirectToLogin(c fiberfw.Ctx, loginURL string) error {
	if c.Get("HX-Request") == "true" {
		c.Set("HX-Redirect", loginURL)
		return c.SendStatus(fiberfw.StatusNoContent)
	}
	return c.Redirect().Status(fiberfw.StatusSeeOther).To(loginURL)
}

// IdentityFrom returns the identity [New] stored for this request. ok
// is false when the middleware is not installed ahead of the caller or
// ID is not the Authenticator's type — both wiring bugs; refuse the
// request and log why, never treat it as anonymous.
func IdentityFrom[ID any](c fiberfw.Ctx) (ID, bool) {
	return auth.IdentityFrom[ID](c.Context())
}

// SetSessionCookie sets the session cookie after a successful sign-in.
func SetSessionCookie[ID any](c fiberfw.Ctx, cfg auth.Config[ID], token string) {
	c.Cookie(fiberCookie(cfg.SessionCookie(token)))
}

// ClearSessionCookie deletes the session cookie — sign-out, or a dead
// session. fiber v3 serializes cookies through net/http, so MaxAge -1
// becomes Max-Age=0 (RFC 6265 "expire immediately") under the exact
// name and attributes of the live cookie — no Expires needed, unlike
// v2.
func ClearSessionCookie[ID any](c fiberfw.Ctx, cfg auth.Config[ID]) {
	c.Cookie(fiberCookie(cfg.SessionClearingCookie()))
}

// SetLoginCSRFCookie mints the login form's pre-session double-submit
// token and returns the value the form embeds in a hidden field; see
// auth.SetLoginCSRFCookie for why the login form needs its own token.
func SetLoginCSRFCookie[ID any](c fiberfw.Ctx, cfg auth.Config[ID]) (string, error) {
	tok, err := auth.NewCSRFToken()
	if err != nil {
		return "", err
	}
	c.Cookie(fiberCookie(cfg.LoginCSRFCookie(tok)))
	return tok, nil
}

// ValidLoginCSRF reports whether the login form submission carries the
// token [SetLoginCSRFCookie] set, in constant time.
func ValidLoginCSRF[ID any](c fiberfw.Ctx, cfg auth.Config[ID], submitted string) bool {
	return auth.EqualTokens(c.Cookies(cfg.LoginCSRFCookieName()), submitted)
}

// fiberCookie translates the auth package's cookie constructors into
// fiber's cookie type, so attribute policy (HttpOnly, Secure, SameSite,
// host-only) has a single source of truth in auth.Config. fiber's
// SameSite switch defaults to Lax for unknown strings, so the two
// allowed modes are mapped explicitly.
func fiberCookie(src *http.Cookie) *fiberfw.Cookie {
	ck := &fiberfw.Cookie{
		Name:     src.Name,
		Value:    src.Value,
		Path:     src.Path,
		MaxAge:   src.MaxAge,
		Secure:   src.Secure,
		HTTPOnly: src.HttpOnly,
		SameSite: fiberfw.CookieSameSiteStrictMode,
	}
	if src.SameSite == http.SameSiteLaxMode {
		ck.SameSite = fiberfw.CookieSameSiteLaxMode
	}
	return ck
}
