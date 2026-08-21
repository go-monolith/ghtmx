### Added

- `auth.WithOnReject`: an observability hook on the CSRF middleware,
  called just before it answers 403 with the request's method, path, and
  an error wrapping `auth.ErrCSRF`. Applications can now count probes or
  debug a migration without wrapping the middleware. The option type is
  the core package's and every adapter's `CSRF` accepts it, so one hook
  value works behind gin, echo, fiber v2, and fiber v3 unchanged; read
  the identity from the same context with `auth.IdentityFrom`.
- `auth.HasFormContentType`: the rule deciding whether a request body may
  carry a CSRF token, exported so the fiber adapters — which read the
  body natively through fasthttp — apply the same gate `auth.VerifyCSRF`
  does instead of an equivalent one.

### Changed

- The fiber v2 and fiber v3 CSRF middleware now consult the request body
  for a `_csrf` field only when the Content-Type names a form, matching
  the net/http middleware exactly rather than relying on fasthttp to
  parse nothing for other types.

  Migration: none required for well-formed requests. Urlencoded and
  multipart submissions are unaffected, including with a `charset` or
  `boundary` parameter. The change is strictly fail-closed: a POST whose
  Content-Type is unparseable, or merely starts with
  `application/x-www-form-urlencoded` without being it, is now rejected
  instead of having its body searched for a token. A request carrying the
  token in the `X-CSRF-Token` header is never gated.

### Fixed

- `fiberv3auth`'s documentation no longer claims fiber v3 serializes
  cookies through `net/http`. It does not — like v2 it writes the header
  with fasthttp, and builds an `http.Cookie` only to validate it. The
  reason `MaxAge -1` suffices in v3 where v2 needs a past `Expires` is
  the newer fasthttp release v3 requires. The code was always correct;
  the explanation would have misled anyone "fixing" it to match.
