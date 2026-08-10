// Package auth provides secure cookie session authentication for
// server-rendered ghtmx applications: cookie hygiene, opaque session
// tokens, CSRF protection, and htmx-aware login redirects, behind one
// small interface the application implements.
//
// The package owns the request-side mechanics only. Session storage,
// lookup, expiry policy, user records, password hashing, registration,
// and authorization all live behind [Authenticator], supplied by the
// application. The library never dictates an identity shape: the
// Authenticator's ID type parameter is whatever the application returns.
//
// # Layering
//
// [Middleware] and [CSRF] are net/http middleware
// (func(http.Handler) http.Handler), directly usable with
// adapters/nethttp and chi. The adapters for frameworks whose contexts
// differ (gin, echo, fiber v2/v3) ship thin glue packages that delegate
// to the same engine functions ([Require], [AuthenticateToken],
// [VerifyCSRFToken]), so behavior is identical by construction.
//
// Install the session middleware before the CSRF middleware: the CSRF
// token is derived from the session and travels in the request context.
// Without a session ahead of it, the CSRF middleware rejects every
// unsafe request — it fails closed, never open.
//
// # Threat model
//
// Session tokens are 256-bit random values; only their SHA-256 hash is
// ever stored server-side ([NewSessionToken]), so a database disclosure
// cannot be replayed as a live session. The session cookie is always
// HttpOnly and host-only (no Domain attribute), Secure unless
// explicitly configured for local http development, and SameSite Strict
// or Lax — None is unrepresentable. When the cookie is scoped to the
// whole site over https, the __Host- prefix is applied automatically.
//
// The always-on CSRF layer uses per-session synchronizer tokens
// (derived, not stored), and the login form gets its own pre-session
// double-submit token ([SetLoginCSRFCookie]) because SameSite does not
// stop login CSRF: it governs whether an existing cookie is sent, not
// whether an unauthenticated POST is accepted.
//
// The cookie's TTL is advisory — it tells a well-behaved browser when
// to drop the cookie. Real expiry and revocation are enforced
// server-side, inside [Authenticator.Authenticate]: return an error
// wrapping [ErrUnauthorized] and the middleware clears the cookie and
// redirects to login. There is no anonymous session to fixate: tokens
// are minted only at successful sign-in, never promoted from a
// pre-login cookie.
package auth
