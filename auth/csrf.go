package auth

import (
	"context"
	"errors"
	"mime"
	"net/http"

	"github.com/go-monolith/ghtmx"
)

// DefaultCSRFFormField is the form field name [VerifyCSRF] reads the
// CSRF token from when it is not in the request header. Templates
// embed it as a hidden input:
//
//	<input type="hidden" name="_csrf" value={ token }>
const DefaultCSRFFormField = "_csrf"

// ErrCSRF is returned by [VerifyCSRF] and [VerifyCSRFToken] when the
// request carries no CSRF token, or one that does not match the
// session's. The middleware answers it with a 403.
var ErrCSRF = errors.New("auth: csrf token missing or invalid")

// CSRF returns the CSRF middleware. GET, HEAD, and OPTIONS pass
// through; every other method must carry the session's CSRF token — in
// the ghtmx.DefaultCSRFHeaderName header or the [DefaultCSRFFormField]
// form field — or the request is rejected with a 403.
//
// Install it after [Middleware]: the expected token comes from the
// request context. With no session middleware ahead of it there is no
// token to match and every unsafe request is rejected — the layer fails
// closed. It is deliberately not optional: SameSite protects at the
// registrable-domain level, not the subdomain level, so a hostile or
// XSS'd sibling subdomain can still forge requests, and the token keeps
// the protection fail-closed if the cookie policy is ever loosened.
func CSRF() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if SafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			if err := VerifyCSRF(r); err != nil {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SafeMethod reports whether method needs no CSRF token: GET, HEAD, and
// OPTIONS, which must never change state. Exported so the glue packages
// share one safe-list.
func SafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// VerifyCSRF extracts the submitted CSRF token from the request and
// checks it against the session's ([VerifyCSRFToken]). The header is
// consulted first — a browser won't attach a custom header cross-origin
// without a CORS preflight, making it a strictly stronger channel than
// the form field — then the [DefaultCSRFFormField] form field, and only
// for form Content-Types. The query string is never consulted: tokens
// must not ride in URLs, where they leak into logs and Referer headers.
//
// The form fallback uses Request.PostFormValue, which parses and caches
// the request body (multipart bodies at the 32 MB default limit); a
// handler that later parses the form itself sees the cached result.
func VerifyCSRF(r *http.Request) error {
	submitted := r.Header.Get(ghtmx.DefaultCSRFHeaderName)
	if submitted == "" && hasFormContentType(r) {
		submitted = r.PostFormValue(DefaultCSRFFormField)
	}
	return VerifyCSRFToken(r.Context(), submitted)
}

// VerifyCSRFToken compares a submitted CSRF token against the one the
// session middleware stored in ctx, in constant time. It returns
// [ErrCSRF] when the context carries no token (session middleware not
// installed ahead of the caller — fail closed), the submission is
// empty, or the values differ. This is the single comparison site; the
// fiber glue extracts the submission natively and calls it.
func VerifyCSRFToken(ctx context.Context, submitted string) error {
	want, ok := CSRFTokenFrom(ctx)
	if !ok || !EqualTokens(want, submitted) {
		return ErrCSRF
	}
	return nil
}

// hasFormContentType reports whether the request body is a form the
// standard library can parse a field out of.
func hasFormContentType(r *http.Request) bool {
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	return ct == "application/x-www-form-urlencoded" || ct == "multipart/form-data"
}
