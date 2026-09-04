# Migration

Where to look when an upgrade breaks something, and what to do about
it. ghtmx is pre-1.0, so breaking changes to language syntax,
generated-code shape, and the runtime API may land between minor
versions — always with a migration note.

There are two independent axes, and they move for different reasons:

| Axis | What changes it | Where it is written down |
| --- | --- | --- |
| The **ghtmx version** you build with | `go get`, `go install`, the install script | `CHANGELOG.md` — every `Changed` and `Removed` entry carries a `Migration:` note |
| The **htmx version** you pin | `htmxVersion` in `ghtmx.json` | [ghtmx.dev/docs/htmx-versions](https://ghtmx.dev/docs/htmx-versions) |

Bumping the module does not change your htmx pin, and changing the pin
does not require a module bump. Read both when you do both.

## Upgrading ghtmx itself

1. Read `CHANGELOG.md` from your current version forward. Every entry
   under `### Changed` or `### Removed` carries a `Migration:` line —
   that is enforced by a build gate, so the absence of one means the
   entry is genuinely drop-in.
2. Run `ghtmx generate` and rebuild. Generated files carry the version
   that produced them (`// ghtmx: version: vX.Y.Z`), so
   `ghtmx generate -check` in CI reports drift the moment the committed
   output and the pinned toolchain disagree.
3. Keep the toolchain and the module in step. `ghtmx version` prints
   what the binary is; the `require github.com/go-monolith/ghtmx` line
   in `go.mod` is what your code links against. `ghtmx generate` warns
   when they differ.

A binary built from a source checkout reports `vX.Y.Z-dev` and stamps
that into anything it generates. That is a development build, not a
release — regenerate with an installed release before committing
generated output.

## Moving from htmx 2 to htmx 4

The full path is
[ghtmx.dev/docs/htmx-versions](https://ghtmx.dev/docs/htmx-versions),
which lists every difference the compiler can see and maps each htmx 2
construct to the diagnostic you will get and the htmx 4 replacement.
The short version:

- Set `"htmxVersion": "4.0.0"` in `ghtmx.json` and run `ghtmx generate`.
  Removed and renamed constructs report `GHTMX-E0501` naming their
  replacement, so the compiler drives the edit list.
- Fix the `GHTMX-W0202` warnings: htmx 4 inheritance is explicit, so an
  inheritable attribute on a wrapper needs `:inherited`. **The CSRF
  header is the one that bites** — `hx-headers={ ghtmx.CSRFHeader(token) }`
  on a layout silently stops reaching the elements that issue requests,
  and every unsafe request is rejected. Write
  `hx-headers:inherited={ ... }`.
- Replace `Emit<Event>AfterSwap` and `Emit<Event>AfterSettle` with the
  plain `Emit<Event>`: htmx 4 removed the `HX-Trigger-After-Swap` and
  `HX-Trigger-After-Settle` response headers they set. Listen on
  `htmx:after:swap` where the timing mattered.
- Check the handler side, which the compiler cannot see: `HX-Source`
  replaces `HX-Trigger` on requests, `hx-delete` no longer sends form
  values on its own, and 4xx/5xx responses swap by default.
- Pinning back to `2.0.10` reverses every check, so the two generations
  cannot mix unnoticed.

The three `htmx4-*` examples in `examples/` are working applications on
the new surface.

## Upgrading to v0.2.0: the CSRF safe-method list

Called out separately because it is the one change in v0.2.0 that
reaches applications with no htmx 4 in them.

`auth.SafeMethod` — and so the CSRF middleware of every adapter —
exempts `QUERY` alongside GET, HEAD, and OPTIONS from v0.2.0 onward.
QUERY is safe and idempotent by specification and is what htmx 4's
`hx-query` issues. The middleware is runtime code and cannot see the
compile-time `htmxVersion` pin, so **the widened list applies whether or
not the project pins htmx 4** — bumping the module is enough.

It only matters if some route answers QUERY, which is easier to hit
than it sounds: an `http.ServeMux` pattern that names no method matches
every method, so a plain `mux.Handle("/x", h)` reaches its handler on
QUERY, and Fiber v3 carries QUERY in its `DefaultMethods`, so an
`app.All(...)` route or a handler mounted as middleware does too. gin
and echo route QUERY only where a route names it explicitly.

- If a handler mutates state on QUERY, move that work to POST, PUT,
  PATCH, or DELETE. It must not mutate on a safe method regardless of
  what the middleware does.
- To keep the pre-v0.2.0 behaviour instead, give the middleware its own
  safe-list:

  ```go
  htmx2 := auth.WithSafeMethods(
      http.MethodGet, http.MethodHead, http.MethodOptions)

  auth.CSRF(htmx2)          // net/http and chi
  fiberv3auth.CSRF(htmx2)   // and identically for gin, echo, fiber
  ```

`AUTH.md` documents the whole CSRF layer, including the two token
channels and why the token stays alongside `SameSite`.

## Where the notes live

- `CHANGELOG.md` — per-version `Migration:` notes. It is assembled from
  `changelog.d/` fragments at release time, never edited by hand.
- `AUTH.md` — sessions, CSRF, and the safe-method list.
- `CONFIG.md` — every `ghtmx.json` key and CLI flag, including
  `htmxVersion`.
- `DIAGNOSTICS.md` — every diagnostic code, including the `GHTMX-E0501`
  and `GHTMX-W0202` families the htmx 4 move relies on.
- [ghtmx.dev/docs/htmx-versions](https://ghtmx.dev/docs/htmx-versions) —
  the htmx 2 versus 4 comparison and the step-by-step move.
