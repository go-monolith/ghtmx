package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// SameSite is the SameSite attribute of the session cookie. Only Strict
// and Lax exist: None would let the cookie ride on cross-site requests
// and is unrepresentable here by design. The zero value is Strict.
//
// The choice is UX, not security — the always-on CSRF token layer keeps
// both settings safe. Strict is right for portals and dashboards nothing
// links into; Lax is right for an app users arrive at via external
// links, where Strict would drop the cookie on that first navigation and
// land a signed-in user on the login page.
type SameSite int

const (
	// SameSiteStrict sends the cookie on same-site requests only. The
	// default.
	SameSiteStrict SameSite = iota
	// SameSiteLax additionally sends the cookie on top-level cross-site
	// navigations (following an external link), per the htmx security
	// essay's baseline.
	SameSiteLax
)

// DefaultCookieName is the session cookie name used when
// [Config.CookieName] is empty. The __Host- prefix may still be applied
// on top; [Config.EffectiveCookieName] reports the name actually used.
const DefaultCookieName = "ghtmx_session"

// loginCSRFSuffix distinguishes the login form's pre-session
// double-submit cookie from the session cookie itself.
const loginCSRFSuffix = "_login_csrf"

// Config configures the session middleware. The zero value of every
// optional field is the safe choice: cookies are Secure, SameSite
// Strict, named [DefaultCookieName], and scoped to "/" (which also
// activates the __Host- prefix). Authenticator and LoginURL are
// required; [Config.Validate] reports anything unusable, and the
// middleware constructors panic on an invalid Config rather than serve
// a single request with it.
type Config[ID any] struct {
	// Authenticator resolves session tokens to identities. Required.
	Authenticator Authenticator[ID]

	// CookieName is the session cookie's base name,
	// [DefaultCookieName] when empty. The __Host- prefix is applied
	// automatically when the cookie is site-wide and Secure; use
	// [Config.EffectiveCookieName] for the name on the wire.
	CookieName string

	// CookiePath scopes the cookie, "/" when empty. Setting it to a
	// sub-app path (e.g. "/admin") keeps the token off the rest of the
	// host's requests, but is mutually exclusive with the __Host-
	// prefix, which requires Path=/. The cookie is host-only either
	// way: no Domain attribute is ever set, so the session is never
	// shared with subdomains.
	CookiePath string

	// Insecure drops the cookie's Secure attribute — for local http
	// development only. The zero value keeps Secure on, which is why
	// this field is inverted: forgetting it must mean secure.
	Insecure bool

	// SameSite is Strict (zero value) or Lax; see [SameSite].
	SameSite SameSite

	// TTL is the cookie's Max-Age. Zero means a session cookie
	// (dropped when the browser closes). This only advises the
	// browser — real expiry is enforced server-side, inside
	// [Authenticator.Authenticate].
	TTL time.Duration

	// LoginURL is where unauthenticated requests are sent: browser
	// navigations with a 303, htmx requests with HX-Redirect and a
	// 204 (so htmx performs a full page load instead of swapping the
	// login page into the target element). Required.
	LoginURL string
}

// Validate reports whether the Config is usable. The middleware
// constructors call it and panic on error — a bad Config is a wiring
// bug, detectable at process start, and gin/echo/fiber middleware
// constructors have no error return. Applications preferring an error
// can call Validate themselves first.
func (c Config[ID]) Validate() error {
	if c.Authenticator == nil {
		return errors.New("auth: Config.Authenticator is required")
	}
	if c.LoginURL == "" {
		return errors.New("auth: Config.LoginURL is required")
	}
	if c.TTL < 0 {
		return fmt.Errorf("auth: Config.TTL is negative (%v)", c.TTL)
	}
	switch c.SameSite {
	case SameSiteStrict, SameSiteLax:
	default:
		return fmt.Errorf("auth: Config.SameSite value %d is not SameSiteStrict or SameSiteLax", c.SameSite)
	}
	d := c.withDefaults()
	if !strings.HasPrefix(d.CookiePath, "/") {
		return fmt.Errorf("auth: Config.CookiePath %q must begin with /", c.CookiePath)
	}
	if strings.HasPrefix(d.CookieName, "__") {
		return fmt.Errorf("auth: Config.CookieName %q must not carry a cookie name prefix — the __Host- prefix is applied automatically", c.CookieName)
	}
	if probe := (&http.Cookie{Name: d.CookieName, Value: "x"}); probe.Valid() != nil {
		return fmt.Errorf("auth: Config.CookieName %q is not a valid cookie name", c.CookieName)
	}
	// Probe the path separately so the error names the offending field.
	// Without this, net/http would silently strip invalid path bytes at
	// serialization time and the cookie would go out under a different
	// path than the one configured.
	if probe := (&http.Cookie{Name: "probe", Value: "x", Path: d.CookiePath}); probe.Valid() != nil {
		return fmt.Errorf("auth: Config.CookiePath %q is not a valid cookie path", c.CookiePath)
	}
	return nil
}

// withDefaults returns a copy with the optional fields defaulted, so
// every consumer of CookieName/CookiePath sees the same values.
func (c Config[ID]) withDefaults() Config[ID] {
	if c.CookieName == "" {
		c.CookieName = DefaultCookieName
	}
	if c.CookiePath == "" {
		c.CookiePath = "/"
	}
	return c
}

// hostPrefixed reports whether cookies from this Config qualify for the
// __Host- prefix: site-wide path and Secure. The prefix additionally
// forbids a Domain attribute, which this package never sets.
func (c Config[ID]) hostPrefixed() bool {
	d := c.withDefaults()
	return d.CookiePath == "/" && !d.Insecure
}

// effectiveName applies the __Host- prefix rule to a base cookie name.
// It is the single naming rule shared by set, clear, read, and the
// login-CSRF cookie, so they can never disagree.
func (c Config[ID]) effectiveName(base string) string {
	if c.hostPrefixed() {
		return "__Host-" + base
	}
	return base
}

// EffectiveCookieName is the session cookie name actually used on the
// wire: CookieName (or [DefaultCookieName]) with the __Host- prefix
// applied when the cookie is site-wide and Secure.
func (c Config[ID]) EffectiveCookieName() string {
	return c.effectiveName(c.withDefaults().CookieName)
}

// LoginCSRFCookieName is the name of the login form's pre-session
// double-submit cookie, derived from the session cookie name under the
// same prefix rule.
func (c Config[ID]) LoginCSRFCookieName() string {
	return c.effectiveName(c.withDefaults().CookieName + loginCSRFSuffix)
}

// httpSameSite maps the package's two-value SameSite onto net/http's.
func (s SameSite) httpSameSite() http.SameSite {
	if s == SameSiteLax {
		return http.SameSiteLaxMode
	}
	return http.SameSiteStrictMode
}

// baseCookie carries the attributes every cookie this package writes
// shares: HttpOnly always (there is no good reason to switch it off for
// a session cookie, so it is not configurable), host-only (never a
// Domain), Secure and SameSite from the Config.
func (c Config[ID]) baseCookie(name, value string) *http.Cookie {
	d := c.withDefaults()
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     d.CookiePath,
		HttpOnly: true,
		Secure:   !d.Insecure,
		SameSite: d.SameSite.httpSameSite(),
	}
}

// SessionCookie is the Set-Cookie payload for a fresh sign-in: the
// session cookie carrying token, with Max-Age from TTL (rounded up to a
// whole second; absent for a zero TTL, making it a browser-session
// cookie). Most net/http applications use [SetSessionCookie] instead;
// this constructor exists so the fiber glue can translate the exact
// same attributes into fasthttp's cookie type.
func (c Config[ID]) SessionCookie(token string) *http.Cookie {
	ck := c.baseCookie(c.EffectiveCookieName(), token)
	if d := c.withDefaults(); d.TTL > 0 {
		ck.MaxAge = int((d.TTL + time.Second - 1) / time.Second)
	}
	return ck
}

// SessionClearingCookie is the Set-Cookie payload that deletes the
// session cookie: same name and attributes, empty value, MaxAge -1 —
// net/http's convention for "expire immediately", which it serializes as
// Max-Age=0 (RFC 6265).
//
// MaxAge -1 is a value, not a wire format, and the fiber glue does not
// hand this cookie to net/http: each fiber major translates it into
// fasthttp's cookie type and must get the deletion attributes right for
// its own serializer. See fiberauth.ClearSessionCookie and
// fiberv3auth.ClearSessionCookie, which differ for that reason.
func (c Config[ID]) SessionClearingCookie() *http.Cookie {
	ck := c.baseCookie(c.EffectiveCookieName(), "")
	ck.MaxAge = -1
	return ck
}

// LoginCSRFCookie is the Set-Cookie payload for the login form's
// pre-session double-submit token: session-lifetime (no Max-Age),
// otherwise sharing the session cookie's attributes.
func (c Config[ID]) LoginCSRFCookie(token string) *http.Cookie {
	return c.baseCookie(c.LoginCSRFCookieName(), token)
}
