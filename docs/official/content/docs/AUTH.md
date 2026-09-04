# Authentication

ghtmx ships a first-party secure cookie session authentication
middleware: a framework-agnostic core (`github.com/go-monolith/ghtmx/auth`)
plus thin glue packages for the framework adapters that have middleware
systems. The library owns the request-side mechanics — cookie hygiene,
opaque token handling, CSRF, htmx-aware login redirects — behind one
small interface the application implements. It does **not** own users,
passwords, or authorization.

htmx's own security guidance says it plainly: the best way to do
authentication with htmx is cookies, set with `Secure`, `HttpOnly`, and
`SameSite`. This package makes those defaults — and the harder parts
the essays defer to OWASP — correct by construction.

## The one interface you implement

```go
type Authenticator[ID any] interface {
	// Authenticate returns the identity behind a session token, or an
	// error wrapping auth.ErrUnauthorized when the session is unusable
	// (expired, revoked, unknown).
	Authenticate(ctx context.Context, token string) (ID, error)
}
```

Session storage, lookup, expiry policy, user records, password hashing —
all behind this interface, all your application's business. The `ID`
type parameter is whatever identity shape your application uses: a user
ID, a struct, anything.

`Authenticate` is where real session expiry and revocation are
enforced. The cookie's TTL only tells a well-behaved browser when to
drop the cookie; a stolen token must die server-side. When your store
says a token is dead, return an error wrapping `auth.ErrUnauthorized` —
the middleware clears the cookie (so the browser stops resending a
value that can never work) and redirects to login. Any *other* error is
answered with a plain 500: an auth-store outage must not degrade into
"please sign in".

## Configuration

```go
cfg := auth.Config[string]{
	Authenticator: store,          // required
	LoginURL:      "/login",       // required
	CookieName:    "ghtmx_session", // default
	CookiePath:    "/",             // default
	SameSite:      auth.SameSiteStrict, // default; SameSiteLax allowed
	TTL:           30 * 24 * time.Hour, // 0 = browser-session cookie
	// Insecure: true               // local http dev only
}
```

The zero value of every optional field is the safe choice:

- **`HttpOnly` is always on** and not configurable — there is no good
  reason to turn it off for a session cookie, and an option would
  invite it.
- **`Secure` is on unless you set `Insecure: true`** (the field is
  inverted so that forgetting it means secure). Use `Insecure` only for
  local http development.
- **`SameSite` is `Strict` (default) or `Lax` — `None` is
  unrepresentable.** The choice is UX, not security: the always-on CSRF
  layer keeps both settings safe. `Strict` is right for portals and
  dashboards nothing links into. `Lax` is right for an app users arrive
  at via external links — under `Strict` that first navigation carries
  no cookie and a signed-in user lands on the login page.
- **The cookie is always host-only** — no `Domain` attribute is ever
  set, so the session is never shared with subdomains.
- **The `__Host-` prefix is applied automatically** when the cookie is
  site-wide (`CookiePath` `/`) and Secure.
  `Config.EffectiveCookieName` reports the name on the wire. Setting
  `CookiePath` to a sub-app path (say `/admin`) keeps the token off the
  rest of the host's requests, but is mutually exclusive with the
  `__Host-` prefix, which requires `Path=/` — pick per app.

`Config.Validate` reports anything unusable. The middleware
constructors call it and panic at startup — a bad Config is a wiring
bug and must not serve a single request. Call `Validate` yourself first
if you prefer an error.

## Installing the middleware

Install the session middleware first, then the CSRF middleware. The
CSRF token is derived from the session and travels in the request
context; with no session middleware ahead of it, the CSRF layer
rejects every unsafe request — it fails closed, never open.

### net/http and chi

The core package's middleware is net/http's shape, so
`adapters/nethttp` servers and chi routers use it directly — there is
no separate glue package:

```go
import "github.com/go-monolith/ghtmx/auth"

r := chi.NewRouter() // or any net/http mux
r.Group(func(r chi.Router) {
	r.Use(auth.Middleware(cfg), auth.CSRF())
	r.Get("/private", privateHandler)
})
```

In handlers:

```go
id, ok := auth.IdentityFrom[string](r.Context())
if !ok {
	// Middleware not installed ahead of this handler, or the wrong ID
	// type — a wiring bug either way. Refuse the request and say why
	// in the log; never treat it as anonymous.
}
token, _ := auth.CSRFTokenFrom(r.Context())
```

### gin, echo, fiber, fiber v3

Each framework has a glue package with the same seven-function surface
(`New`, `CSRF`, `IdentityFrom`, `SetSessionCookie`,
`ClearSessionCookie`, `SetLoginCSRFCookie`, `ValidLoginCSRF`), enforced
by a parity gate:

| Framework | Package |
| --- | --- |
| gin | `github.com/go-monolith/ghtmx/adapters/gin/ginauth` |
| echo | `github.com/go-monolith/ghtmx/adapters/echo/echoauth` |
| fiber v2 | `github.com/go-monolith/ghtmx/adapters/fiber/fiberauth` |
| fiber v3 | `github.com/go-monolith/ghtmx/adapters/fiberv3/fiberv3auth` |

```go
// gin
priv := r.Group("/", ginauth.New(cfg), ginauth.CSRF())

// echo
priv := e.Group("", echoauth.New(cfg), echoauth.CSRF())

// fiber v2
priv := app.Group("/", fiberauth.New(cfg), fiberauth.CSRF())

// fiber v3 — same shape, different package
priv := app.Group("/", fiberv3auth.New(cfg), fiberv3auth.CSRF())
```

The gin and echo glue delegate to the same engine functions the core
middleware uses. The fiber glue is implemented natively on fasthttp —
including each fiber major's own correct cookie-deletion serialization,
which differ — but authentication, token comparison, cookie attribute
policy, and the rule deciding when a request body may carry a token
(`auth.HasFormContentType`) are all shared code, not restatements of
it. Auditing one adapter therefore tells you about the others.

Cookie deletion is the one place the two fiber majors genuinely diverge,
and it is worth knowing why, because the surface reason is wrong:
**neither major serializes cookies through `net/http`.** Both write the
header with fasthttp. The fasthttp release fiber v3 requires emits
`Max-Age=0` for a negative `MaxAge`, so `fiberv3auth.ClearSessionCookie`
is the plain `MaxAge -1`; the older release fiber v2 pins drops a
non-positive `Max-Age` from the header entirely, which would leave the
cookie alive, so `fiberauth.ClearSessionCookie` adds the past-`Expires`
incantation. Each package's tests pin its own emitted header.

### Observing rejections

`CSRF` takes options, and they are observability only — nothing about
them changes whether a request is accepted. `auth.WithOnReject` runs
just before the 403, so an application can count probes or debug a
migration without wrapping the middleware:

```go
hook := auth.WithOnReject(func(ctx context.Context, rej auth.CSRFRejection) {
	// The identity, when there is one, comes from the same context —
	// which is why one hook shape works for every adapter.
	id, _ := auth.IdentityFrom[string](ctx)
	log.Warn("csrf rejected", "method", rej.Method, "path", rej.Path, "user", id)
})

http.Handler(auth.CSRF(hook)(next)) // core
priv := app.Group("/", fiberv3auth.New(cfg), fiberv3auth.CSRF(hook)) // any adapter
```

The option type is the core package's, so the same `hook` value moves
between frameworks unchanged. `rej.Path` is the request path in every
adapter — never the route pattern, and never the query string, which is
not a token channel here. The hook runs on the request's goroutine, so
hand anything expensive to a queue.

## The login flow

The login page is yours; the middleware only needs its URL. A complete
flow with the core package (the glue packages mirror it):

```go
// GET /login — render the form with a pre-session CSRF token.
func loginForm(w http.ResponseWriter, r *http.Request) {
	formValue, err := auth.SetLoginCSRFCookie(w, cfg)
	if err != nil { /* 500 */ }
	// Render your login page with:
	//   <input type="hidden" name="login_csrf" value={ formValue }>
}

// POST /login — check the token before the credentials.
func login(w http.ResponseWriter, r *http.Request) {
	if !auth.ValidLoginCSRF(r, cfg, r.PostFormValue("login_csrf")) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// ... verify credentials your way (bcrypt, argon2, LDAP, ...) ...
	token, storedHash, err := auth.NewSessionToken()
	if err != nil { /* 500 */ }
	// Persist storedHash (never the token) with the user and expiry.
	auth.SetSessionCookie(w, cfg, token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// POST /logout — inside the middleware chain.
func logout(w http.ResponseWriter, r *http.Request) {
	// Delete the session record server-side, then:
	auth.ClearSessionCookie(w, cfg)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
```

Why the login form needs its own token: **`SameSite` does not protect
the login form — on either setting.** It governs whether an *existing*
cookie is sent, not whether an unauthenticated POST is *accepted* — so
login CSRF (an attacker submitting *their* credentials from the
victim's browser, after which the victim unknowingly works, and is
audited, as the attacker) still works without it. The session CSRF
token can't help either: there is no session yet. The pre-session
double-submit cookie closes the gap.

Session fixation is impossible by construction: there is no anonymous
session to upgrade. A token is minted only at successful sign-in
(`auth.NewSessionToken`), never read from a pre-login cookie and
promoted.

## Tokens: store the hash

`auth.NewSessionToken` returns a 256-bit random token and its SHA-256
hex. Only the hash is ever stored server-side, so a database disclosure
(backup, dump, read-only SQL injection) cannot be replayed as a live
session; at lookup time, hash the presented token with
`auth.HashSessionToken` and query by that. Plain SHA-256 is correct
here — the input is uniform randomness, so a slow KDF would buy nothing
but per-request latency. (Password hashing is a different problem and
stays your application's business.)

## CSRF

The CSRF layer is always on, per-session, and needs no storage: the
token is derived from the session token (domain-separated, so it is
computable from neither the stored hash nor vice versa) and installed
in the request context by the session middleware.

Unsafe methods (everything but GET, HEAD, OPTIONS, and QUERY — the
`auth.SafeMethod` list; QUERY is htmx 4's read-only method with a body)
must carry the token in one of two channels:

- **The `X-CSRF-Token` header** (`ghtmx.DefaultCSRFHeaderName`) — the
  stronger channel: a browser won't attach a custom header cross-origin
  without a CORS preflight. For htmx elements, the existing
  `ghtmx.CSRFHeader` helper emits the `hx-headers` attribute. Placed on
  a common ancestor (e.g. `<body>`) it reaches every element below it —
  but **how it reaches them depends on the pinned htmx version**:

  ```html
  <!-- htmx 2.0.x: attributes are inherited implicitly -->
  <body hx-headers={ ghtmx.CSRFHeader(token) }>

  <!-- htmx 4.0.0: inheritance is explicit; without :inherited the
       header never leaves <body> and every unsafe request is rejected -->
  <body hx-headers:inherited={ ghtmx.CSRFHeader(token) }>
  ```

  with `token` read from the request context via `auth.CSRFTokenFrom`.
  Under an htmx 4 pin the compiler reports the first form as
  `GHTMX-W0202` (with a CSRF remark), so the mistake does not reach
  production silently; see `docs/official/pages/htmx-versions.md` for
  the rest of the 2 → 4 differences.

- **A hidden form field** named `_csrf` (`auth.DefaultCSRFFormField`)
  for plain HTML form posts:

  ```html
  <input type="hidden" name="_csrf" value={ token }>
  ```

The query string is never consulted — tokens must not ride in URLs,
where they leak into logs and `Referer` headers. Comparison is
constant-time and rejects empty strings outright
(`subtle.ConstantTimeCompare("", "")` returns 1; `auth.EqualTokens`
does not repeat that mistake).

Why keep the token layer when `SameSite` is already set? Because
`SameSite` protects at the registrable-domain level, not the subdomain
level: a hostile — or merely XSS'd — sibling subdomain is same-site and
can still forge requests. The token also keeps the middleware
fail-closed if the cookie policy is ever loosened.

Note on form parsing: the form fallback uses `Request.PostFormValue`,
which parses and caches the request body (multipart bodies at the
stdlib's 32 MB default). A handler that later calls
`ParseMultipartForm` with its own limit sees the cached parse. Prefer
the header channel for htmx elements; it reads no body at all.

### The safe-method list, and declining QUERY

**v0.2.0 widened the safe-method list for every application, whatever
htmx version it pins.** Before it, GET, HEAD, and OPTIONS were exempt;
from it, QUERY is too. `auth.SafeMethod` is runtime code and cannot see
the compile-time `htmxVersion` pin, so upgrading the module is enough
to change the CSRF surface — no `hx-query` and no htmx 4 pin required.

The precondition is narrow: some route has to answer QUERY at all.
Nothing routes it by accident in net/http, gin, or echo, but Fiber v3
carries QUERY in its `DefaultMethods`, so a route registered with
`app.All(...)` — or a handler mounted as middleware — answers it. On
v0.1.x such a request was refused for a missing token; from v0.2.0 it
is exempt. If any handler of yours mutates state on QUERY, it must
stop: QUERY is safe and idempotent by specification, and the work
belongs on POST, PUT, PATCH, or DELETE.

To decline the exemption instead, give the middleware its own
safe-list. `auth.WithSafeMethods` replaces the default wholesale, and
works identically for `auth.CSRF` and for every adapter's glue:

```go
// The pre-v0.2.0 safe-list: QUERY needs a token like any other method.
htmx2 := auth.WithSafeMethods(
    http.MethodGet, http.MethodHead, http.MethodOptions)

mux.Handle("/", auth.Middleware(cfg)(auth.CSRF(htmx2)(app)))
// ... or, behind an adapter:
priv := app.Group("/", fiberv3auth.New(cfg), fiberv3auth.CSRF(htmx2))
```

`auth.DefaultSafeMethods()` returns the default list if you would
rather extend it than replace it. The list is matched exactly, so pass
methods in upper case; calling `auth.WithSafeMethods()` with no
arguments makes every method unsafe, GET included.

## Testing your handlers

Handler tests don't need to run the middleware — build the context
directly:

```go
ctx := auth.ContextWithIdentity(context.Background(), "user-1")
ctx = auth.ContextWithCSRFToken(ctx, "test-token")
req := httptest.NewRequest("GET", "/private", nil).WithContext(ctx)
```

## What this package deliberately does not do

- User/password storage, password hashing policy, registration,
  lockout, throttling — application concerns behind `Authenticator`.
- Roles and authorization — identity is generic; layer your own guards
  on top of `IdentityFrom`.
- Login page UI — the middleware only needs `LoginURL`.
- JWTs / stateless sessions — opaque server-side tokens are the right
  default for server-rendered apps, and the `token -> identity`
  interface keeps the library out of the crypto-policy business.
