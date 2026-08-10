### Added

- `auth` package: secure cookie session authentication middleware for
  server-rendered apps. The application implements one interface —
  `Authenticate(ctx, token) (ID, error)` — and the library owns the
  request-side mechanics: opaque 256-bit session tokens with the
  store-the-hash pattern, always-`HttpOnly` host-only cookies
  (`Secure` by default, `SameSite` Strict or Lax only, automatic
  `__Host-` prefix when site-wide), htmx-aware login redirects (303
  for browser navigations, `HX-Redirect` + 204 for htmx requests),
  always-on per-session CSRF protection (derived synchronizer tokens,
  header or hidden-field channel, constant-time compare), and a
  pre-session double-submit token for the login form itself. The
  middleware is net/http-shaped, so `adapters/nethttp` servers and chi
  routers use it directly.
- Auth glue packages for the framework adapters whose contexts differ:
  `adapters/gin/ginauth`, `adapters/echo/echoauth`,
  `adapters/fiber/fiberauth`, and `adapters/fiberv3/fiberv3auth`, each
  exporting the same seven-function surface (`New`, `CSRF`,
  `IdentityFrom`, `SetSessionCookie`, `ClearSessionCookie`,
  `SetLoginCSRFCookie`, `ValidLoginCSRF`), enforced by a parity gate.
  Each fiber glue owns its major version's correct cookie-deletion
  serialization (past `Expires` on v2, `Max-Age=0` on v3).
- `AUTH.md`: the authentication reference — configuration, per-framework
  quick starts, the login flow, and the security model — also on the
  docs site under /docs/auth.
