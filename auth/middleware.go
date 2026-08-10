package auth

import (
	"errors"
	"net/http"

	"github.com/go-monolith/ghtmx"
)

// Middleware returns the session middleware: it authenticates the
// session cookie on every request and either installs the identity and
// per-session CSRF token in the request context or answers the request
// itself ([Require] describes the exact outcomes). The signature is
// net/http's middleware shape, so it plugs directly into
// adapters/nethttp servers and chi routers; the other frameworks have
// glue packages.
//
// Middleware panics if cfg is invalid — see [Config.Validate].
func Middleware[ID any](cfg Config[ID]) func(http.Handler) http.Handler {
	if err := cfg.Validate(); err != nil {
		panic(err)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r2, ok := Require(cfg, w, r)
			if !ok {
				return
			}
			next.ServeHTTP(w, r2)
		})
	}
}

// Require runs the session check for one request. On success it returns
// the request with identity and CSRF token installed in its context and
// ok true. Otherwise it writes the complete response itself and returns
// ok false:
//
//   - no session cookie: redirect to LoginURL ([RedirectToLogin]).
//   - Authenticate wraps [ErrUnauthorized]: clear the cookie, then
//     redirect — the browser must stop resending a dead token.
//   - any other Authenticate error: plain 500. An auth-store outage is
//     not "please sign in".
//
// Require assumes cfg has been validated; [Middleware] and the glue
// packages' constructors do that once at startup. It is exported as the
// per-request engine those glue packages share.
func Require[ID any](cfg Config[ID], w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	ck, err := r.Cookie(cfg.EffectiveCookieName())
	if err != nil || ck.Value == "" {
		RedirectToLogin(w, r, cfg.LoginURL)
		return r, false
	}
	ctx, _, err := AuthenticateToken(r.Context(), cfg, ck.Value)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			ClearSessionCookie(w, cfg)
			RedirectToLogin(w, r, cfg.LoginURL)
			return r, false
		}
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return r, false
	}
	return r.WithContext(ctx), true
}

// RedirectToLogin sends an unauthenticated request to the login page
// the way its client can follow: browser navigations get a 303 See
// Other, but a request htmx issued must not — htmx would swap the login
// page into the target element — so it gets HX-Redirect with a 204 and
// htmx performs a full page load. The HX-Redirect header is set before
// the status is written; net/http drops headers set afterwards.
func RedirectToLogin(w http.ResponseWriter, r *http.Request, loginURL string) {
	if ghtmx.IsHTMXRequest(r) {
		ghtmx.SetRedirect(w, loginURL)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, loginURL, http.StatusSeeOther)
}
