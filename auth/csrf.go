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

// CSRFRejection describes one rejected unsafe request, as handed to a
// [WithOnReject] hook. It is a struct rather than loose parameters so
// later releases can report more without breaking every hook.
//
// The identity is deliberately absent: it is generic in the
// Authenticator's ID type, which this struct cannot be, and the hook can
// read it from the same context with [IdentityFrom].
type CSRFRejection struct {
	// Method is the rejected request's HTTP method.
	Method string
	// Path is the request path — never the route pattern, and never the
	// query string, which must not carry tokens in the first place.
	Path string
	// Err is why the request was rejected; it always satisfies
	// errors.Is(err, ErrCSRF).
	Err error
}

// CSRFOptions is the applied form of the [CSRFOption] list a CSRF
// middleware was constructed with. It is exported, with unexported
// fields, because every glue package builds one and reports through it —
// the same reason [SafeMethod] and [VerifyCSRFToken] are exported.
type CSRFOptions struct {
	onReject func(context.Context, CSRFRejection)
	// safe is the method set that needs no token. Nil — the zero value
	// — means the package default, so a CSRFOptions built without
	// [WithSafeMethods] behaves exactly as [SafeMethod] does.
	safe map[string]bool
}

// CSRFOption configures a CSRF middleware. The same options work for
// [CSRF] and for every adapter's glue CSRF middleware, so an application
// wires its observability once and keeps it across a framework switch.
type CSRFOption func(*CSRFOptions)

// WithOnReject installs an observer called just before the middleware
// answers 403, so an application can count probes, alert, or debug a
// migration without wrapping the middleware. It cannot change the
// outcome: the 403 is already decided, and the hook's return value —
// there is none — is not consulted.
//
// The hook runs on the request's goroutine, so a slow one is a slow
// rejection; hand anything expensive to a queue. Read the identity, when
// there is one, with IdentityFrom on the same ctx.
func WithOnReject(fn func(ctx context.Context, rej CSRFRejection)) CSRFOption {
	return func(o *CSRFOptions) {
		o.onReject = fn
	}
}

// WithSafeMethods replaces the set of methods that need no CSRF token,
// for this middleware only. The default is [DefaultSafeMethods] — GET,
// HEAD, OPTIONS, and QUERY.
//
// It exists because the QUERY exemption arrived with htmx 4 support and
// applies to every consumer, whatever htmx version the project pins:
// this package is runtime code and cannot see the compile-time pin. An
// application that serves a state-changing handler on QUERY — Fiber v3
// carries QUERY in its DefaultMethods, so an app.All route answers it —
// declines the exemption with the htmx 2 safe-list:
//
//	auth.CSRF(auth.WithSafeMethods(
//		http.MethodGet, http.MethodHead, http.MethodOptions))
//
// Methods are matched exactly, so pass them in the upper case the
// standard library's http.Method* constants use. Calling it with no
// arguments makes every method unsafe, GET included — the safe-list is
// replaced wholesale, never merged with the default.
func WithSafeMethods(methods ...string) CSRFOption {
	return func(o *CSRFOptions) {
		// Allocated unconditionally: an empty non-nil map is "nothing
		// is safe", while nil would fall back to the default and mean
		// the opposite of what the caller asked for.
		safe := make(map[string]bool, len(methods))
		for _, m := range methods {
			safe[m] = true
		}
		o.safe = safe
	}
}

// NewCSRFOptions applies opts. The glue packages call it once at
// construction, not per request, and keep the result.
func NewCSRFOptions(opts ...CSRFOption) CSRFOptions {
	var o CSRFOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Report invokes the [WithOnReject] hook if one is set, and does nothing
// otherwise. It is the single reporting site every middleware shares, so
// the nil check can never diverge between adapters.
func (o CSRFOptions) Report(ctx context.Context, rej CSRFRejection) {
	if o.onReject != nil {
		o.onReject(ctx, rej)
	}
}

// SafeMethod reports whether method needs no CSRF token under these
// options: the [WithSafeMethods] list if one was given, and the
// package-level [SafeMethod] default otherwise. It is the single
// decision site every middleware shares, so the safe-list can never
// diverge between adapters — the same reason [CSRFOptions.Report] is
// the single reporting site.
func (o CSRFOptions) SafeMethod(method string) bool {
	if o.safe == nil {
		return SafeMethod(method)
	}
	return o.safe[method]
}

// CSRF returns the CSRF middleware. The methods [DefaultSafeMethods]
// names pass through; every other method must carry the session's
// CSRF token — in
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
//
// [WithOnReject] watches rejections. [WithSafeMethods] is the one
// option that changes whether a request is accepted: it replaces the
// exempt-method list, and is how an application declines the QUERY
// exemption.
func CSRF(opts ...CSRFOption) func(http.Handler) http.Handler {
	o := NewCSRFOptions(opts...)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if o.SafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			if err := VerifyCSRF(r); err != nil {
				o.Report(r.Context(), CSRFRejection{Method: r.Method, Path: r.URL.Path, Err: err})
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// DefaultSafeMethods returns the methods that need no CSRF token
// unless [WithSafeMethods] says otherwise: GET, HEAD, OPTIONS, and
// QUERY. The result is a fresh slice; mutating it changes nothing.
func DefaultSafeMethods() []string {
	return []string{http.MethodGet, http.MethodHead, http.MethodOptions, MethodQuery}
}

// SafeMethod reports whether method needs no CSRF token: GET, HEAD,
// OPTIONS, and QUERY, which must never change state. QUERY is the
// safe, idempotent method with a body that htmx 4's hx-query issues;
// like GET it must not mutate, and unlike a form post a browser sends
// it cross-origin only after a CORS preflight. Exported so the glue
// packages share one safe-list.
//
// It is the default, not the whole story: a middleware built with
// [WithSafeMethods] decides through [CSRFOptions.SafeMethod] instead.
// QUERY became exempt here in v0.2.0 for every consumer, whatever htmx
// version the project pins — see [WithSafeMethods] to decline it.
func SafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, MethodQuery:
		return true
	}
	return false
}

// MethodQuery is the HTTP QUERY method (safe, idempotent, with a body),
// which net/http does not name yet.
const MethodQuery = "QUERY"

// VerifyCSRF extracts the submitted CSRF token from the request and
// checks it against the session's ([VerifyCSRFToken]). The header is
// consulted first — a browser won't attach a custom header cross-origin
// without a CORS preflight, making it a strictly stronger channel than
// the form field — then the [DefaultCSRFFormField] form field, and only
// for form Content-Types ([HasFormContentType], the same gate the fiber
// glue applies). The query string is never consulted: tokens must not
// ride in URLs, where they leak into logs and Referer headers.
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

// HasFormContentType reports whether a Content-Type header value names a
// form body a CSRF token may legitimately arrive in: urlencoded or
// multipart, with any parameters (a charset, a multipart boundary).
// Anything it cannot parse is not a form — the gate fails closed.
//
// It is exported, and takes the header value rather than a request, so
// the fiber glue — which reads the body natively through fasthttp and
// has no *http.Request to hand over — gates on this exact rule instead
// of on an argument that the two paths happen to agree.
func HasFormContentType(contentType string) bool {
	ct, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return ct == "application/x-www-form-urlencoded" || ct == "multipart/form-data"
}

// hasFormContentType reports whether the request body is a form the
// standard library can parse a field out of.
func hasFormContentType(r *http.Request) bool {
	return HasFormContentType(r.Header.Get("Content-Type"))
}
