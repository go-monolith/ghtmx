# Changelog

All notable changes to ghtmx are documented in this file.

**This file is assembled, not edited.** Contributors add a fragment in
`changelog.d/` (one file per PR, so two PRs can never conflict); the release
automation folds those into a new section here under the version it actually
assigns, and brings the fold back to `main` as a `changelog/<tag>` pull
request. Do not edit it by hand: CI rejects hand-written version headings,
and entries belong in fragments — the next release's fold would collide with
anything written here directly.

Pre-1.0 stability posture: breaking changes to language syntax, generated-code
shape, and the runtime API are allowed between minor versions and are always
recorded here with a migration note. The discipline is enforced: every entry
in a Changed, Removed, or Breaking section must carry a `Migration:` note
("Migration: none required" is valid), and `internal/installcheck` fails the
build otherwise — for the fragments in `changelog.d/` too. Releases follow
`RELEASING.md`.

## [0.1.18] - 2026-08-08

The entries below accumulated in this file's former `[Unreleased]`
section while releases v0.1.1 through v0.1.17 shipped without moving
them; they are folded here, where the fragment system first assigned
them a version.

### Added

- Method values are discoverable as route handlers:
  `r.Get("/users", h.ListUsers)` now enters the table as
  `Handlers.ListUsers` whenever the receiver's type is named in the same
  function — a parameter, a `var` with an explicit type, a composite
  literal, or `new(T)`. Giving handlers their dependencies through a
  struct receiver is the ordinary Go shape; requiring package-level
  funcs pushed projects into rebuilding dependency injection through
  request-scoped storage. The inference stays syntax-only and
  single-function, so nothing that resolved before changes: an import
  alias still wins over a same-named receiver, and a receiver whose type
  is not syntactically visible (a constructor call, a struct field, a
  call result) keeps reporting `GHTMX-E0402`.
- `//ghtmx:route` annotations accept `Type.Method` and
  `pkg.Type.Method`, so the escape hatch covers what discovery cannot.
  A method is not a symbol a template can name, so these routes bind
  through their generated central symbols, which fold the dot away:
  `ghtmxgen.HandlersListUsersPath` without parameters,
  `ghtmxgen.HandlersGetUser(id)` with. A folded name that collides with
  another handler's still reports `GHTMX-E0404`, and the language
  server offers the generated symbol rather than the method.
- The `routetable` package and `ghtmx routes -check-against
  <file.json>`, which turn annotation-versus-reality drift into a test.
  `generate -check` verifies template-versus-generated-code drift; it
  cannot see whether the paths the toolchain believes in are the paths
  the framework serves, which is exactly what a project taking the
  `//ghtmx:route` escape hatch outside `routeScope` gives up. `Load`
  returns the same table `ghtmx routes` prints, `Normalize` converts a
  router's own path syntax with the toolchain's normalizer rather than a
  copy that could drift, and `Diff` reports missing, unexpected, and
  handler-mismatched routes. `routes -json` now emits the package's own
  type, so CLI output and the type it unmarshals into cannot diverge.
- `//ghtmx:routeprefix /admin/user`, a package-scoped directive
  declaring the mount prefix a sub-application's routes are served
  under. Routes registered inside a sub-app are discovered at their
  sub-app-relative path, and a mount site using a variable prefix and a
  cross-package router value cannot be recognised syntactically — so the
  prefix is declared rather than inferred. Every route the package
  registers, discovered or annotated, composes under it; group prefixes
  still nest inside. The prefix must be static, and two files declaring
  different prefixes for one package is `GHTMX-E0403`. Annotated paths
  become relative to it: an annotation that already spells the mount
  point composes twice, so shorten those when adopting the directive.
- `adapters/fiberv3`, a first-party render adapter for fiber's v3 major
  version — the same thin bridge over `adapters/nethttp` as the fiber v2
  adapter, ported to v3's `fiber.Ctx` interface and `Context()`
  accessor. A nested module like the other adapters, released in
  lockstep as `adapters/fiberv3/vX.Y.Z`. The directory is `fiberv3`
  rather than `fiber/v3` because Go reads a trailing `/v3` as a module
  major-version suffix and would demand `v3.x.x` tags.
- `GHTMX-W0105`: warns when one handler symbol is registered for the
  same verb at more than one path — typically a route both discovered
  and declared by annotation — naming every site and the path template
  bindings actually resolve to. Previously `Lookup` picked a winner
  silently, so a binding could generate an unexpected URL with no
  diagnostic. Warning-class: projects that deliberately serve one
  handler at several URLs can set `GHTMX-W0105=off`.
- `GHTMX-E0308`: importing the ghtmx root package in a template file —
  or aliasing any import as `ghtmx` or `ghtmxruntime` — is now reported
  at the import line the author wrote, with the alias escape hatch in
  the message. Previously the collision surfaced as a Go redeclaration
  error inside generated code, pointing at a line nobody can edit. The
  check runs in both `ghtmx generate` and the language server.
- A trailing `nav` marker on `//ghtmx:route` annotations
  (`//ghtmx:route GET /audit handlers.AuditLog nav`) declares a
  navigation-only route — reached by `<a href>` or a native form post —
  exempting that route from the `GHTMX-W0104` unbound-route warning
  without silencing the check project-wide. Server-rendered portals
  previously had to turn `GHTMX-W0104` off globally, losing orphan
  detection everywhere.
- `htmxScript` in `ghtmx.json` (flag: `-htmx-script`): set `false` to omit
  the `HTMXScript()` helper from the central generated package, for
  projects that use ghtmx purely as a server-side template engine and
  load no htmx at all. `htmxVersion` still drives attribute validation,
  but no longer needs a pinned script asset when the helper is off.
- `ghtmx.HTMXScriptIntegrity(version)`: returns the pinned
  subresource-integrity hash for a supported htmx version, so a project
  serving htmx itself (`WithScriptSrc`) can assert in a unit test that
  its vendored file is the exact published build the tag pins, instead
  of scraping the hash out of rendered HTML.
- `templateExtension` in `ghtmx.json` (flag: `-template-extension`): the
  file extension templates are written with, `.ghtmx` by default and
  `.htmx` as the alternative. A project uses exactly one — with `.htmx`
  configured, a `.ghtmx` file is not a template and is neither generated
  from nor served by the language server. Any other value is rejected by
  name; `.go` in particular would make the generator walk its own output.
  The value salts the build cache key, so switching does not reuse
  artifacts built for the other extension. The `-watch-pattern` default
  follows it.
- Editor support for `.htmx` alongside `.ghtmx` in the VS Code extension,
  the Neovim plugin, and the JetBrains TextMate bundle, plus a file icon
  for templates in light and dark variants.
- Published coverage reporting. The `coverage` CI job now uploads its
  profile to Codecov, so the README carries a live badge and every pull
  request gets a line-by-line coverage diff. `codecov.yml` mirrors the
  exclusions `internal/covergate` already enforces and marks Codecov's
  own statuses informational — the 90% floor remains the only coverage
  check that can fail a build.
- `scripts/install.sh`, which installs `ghtmx` and `gopls` onto your
  PATH. Installing an editor extension never installed the binaries the
  extension needs, and the only route was two `go install` commands
  found in the README. The script downloads the release archive for your
  platform, verifies it against `checksums.txt` before writing anything,
  installs `gopls` when a Go toolchain is present, and prints the exact
  `export PATH=` line when the install directory is unreachable — it
  never edits startup files. Configured by `GHTMX_VERSION`,
  `GHTMX_BIN_DIR`, `GOPLS_VERSION`, and `GHTMX_SKIP_GOPLS`. It is a
  wrapper around the existing release-archive install path, not a third
  supported path: bash only, so Linux, macOS, and WSL.
- The VS Code extension in that same one-liner. After the binaries are
  in place the script looks for the `code` CLI and, when the ghtmx
  extension is not installed, asks whether to add it and installs the
  `.vsix` from the same release — so a VS Code user goes from nothing to
  a working setup in one command. It only ever asks: any answer but `y`
  installs nothing, and a run with no terminal to ask on skips the
  question rather than blocking. `--no-interactive` turns off every
  prompt (the extension passes it when it runs the script itself),
  `GHTMX_INSTALL_VSCODE` answers yes in advance, and `GHTMX_SKIP_VSCODE`
  leaves the editor alone.
- A way out of the VS Code extension's "cannot start the server" error.
  It now names both binaries and offers to run the installer in a
  terminal — with the command typed but not executed, so nothing runs
  without a keypress — to open the `ghtmx.path` setting, or to reload
  the window. The same flow is in the command palette as
  **ghtmx: Install ghtmx and gopls**.
- A CI check that fails a pull request changing code a release will
  ship without a changelog fragment, and rejects hand-written
  `CHANGELOG.md` version sections, so the mandatory-changelog rule is
  enforced rather than remembered. `internal/installcheck` lints the
  fragments themselves: known section headings only, and the pre-1.0
  `Migration:` discipline applies before entries ever reach
  `CHANGELOG.md`.

### Changed

- Generated render functions acquire their buffer with one `GetBuffer`
  call and one deferred `ghtmxruntime.ReleaseAcquiredBuffer`, replacing
  the nine-statement acquire-and-conditionally-release block every one
  of them used to carry. Behaviour is identical — the buffer is still
  released only by the outermost component, a flush error is still
  reported only when the render itself succeeded — but generated
  statements land in a consuming project's coverage figures, and this
  was the largest block of code nobody wrote. Migration: run
  `ghtmx generate` and commit the result; `generate -check` reports the
  stale files otherwise. Hand-written code is unaffected, and
  `GetBuffer`/`ReleaseBuffer` remain exported.
- How changelog entries are written: add a fragment in `changelog.d/`
  instead of editing `CHANGELOG.md`'s `[Unreleased]` section. One file
  per PR means two open PRs can no longer conflict on the same lines,
  and nobody writes a version number — the release automation folds the
  fragments into `CHANGELOG.md` under the version it actually assigns,
  inside the release-prep commit the tag points at, and opens a
  `changelog/<tag>` pull request that brings the fold back to the
  protected `main` (and, through it, to the changelog page on the docs
  site). Migration: none required for users; contributors write
  `changelog.d/<branch>.md` files from now on — see
  `changelog.d/README.md`.

### Fixed

- Dev-mode hot literal reload for projects using a non-default template
  extension. The generator hashes a template's real path to name its
  literals sidecar, while generated code recovered that path from its own
  `_ghtmx.go` name by appending `.ghtmx` unconditionally, so the two
  sides addressed different files and the reader found nothing.

## [0.1.0] - 2026-08-02

First tagged release. Before this, the module had no tags at all, so
`go get github.com/go-monolith/ghtmx` could only resolve to a pseudo-version
and the nested adapter modules did not resolve at all.

### Added

- Live example demos on the documentation site: every `examples/*`
  application is now an importable package with a thin `cmd/` main, and
  the docs binary compiles the real routers in and serves them at their
  native paths (`/todos`, `/items`, `/fragments`, `/events`, `/hello`).
  Run examples standalone with `go run ./cmd` from the example directory
  (previously `go run .`).
- The official documentation site (`docs/official/`): a nested module that
  dogfoods the engine — chi routes with symbol and constructor bindings,
  compile-time fragments behind the chi adapter's `WithPage`, the
  `DocViewed` event contract, and every reference document and example
  embedded via `embed.FS`. Compiles natively and to `js/wasm` for
  Cloudflare Workers (`syumai/workers`); the embedded copies are
  drift-gated against their single-source documents in CI.
- Initial hard fork of [templ](https://github.com/a-h/templ) at commit
  `04abee5` (see `TEMPL_SYNTAX_BASELINE.md`): parser, generator, runtime,
  formatter, CLI (`generate`, `fmt`, `lsp`, `info`, `version`), live-reload
  dev proxy, and LSP proxy, renamed to the `ghtmx` module with the `.ghtmx`
  file extension and `*_ghtmx.go` generated files.

### Changed (vs. upstream templ)

- Module path is `github.com/go-monolith/ghtmx`; the runtime package is
  `ghtmx` and generated code calls `ghtmxruntime` (`ghtmx/runtime`).
  Migration: replace `github.com/a-h/templ` imports with
  `github.com/go-monolith/ghtmx` and rename `.templ` files to `.ghtmx`.
- Generated files use the `ghtmx_7f3b9d1a_` variable prefix and the
  `// Code generated by ghtmx - DO NOT EDIT.` header. Migration: delete
  old `*_templ.go` files and run `ghtmx generate`.
- Dev-mode environment variables are `GHTMX_DEV_MODE*`; the live-reload
  reserved path is `/_ghtmx/reload/`. Migration: rename any
  `TEMPL_DEV_MODE*` usage and update proxies that special-cased the old
  reload path.
- The five `hx-*` verb attributes are typed route bindings (FR-004
  carve-out 1): string URLs are `GHTMX-E0602`, arbitrary expressions
  `GHTMX-E0601`. Migration: bind a handler symbol
  (`hx-post={ handlers.CreateUser }`) or a generated constructor
  (`hx-get={ ghtmxgen.GetUser(id) }`); see `CONFORMANCE.md`.
- Rendering no longer flushes the underlying `http.Flusher` after every
  component (upstream forced chunked encoding and early header commit).
  Migration: none for typical apps; for deliberate streaming, render a
  `ghtmx.Flush()` component where a flush is wanted.

### Removed (vs. upstream templ)

- The runtime fragment API (`templ.Fragment`, `RenderFragments`,
  `WithFragments`): replaced by compile-time `fragment` declarations with
  dual generated entry points (upcoming milestone). Migration: declare a
  `fragment` in the template and call its generated `...Fragment` entry point.
- The Prettier integration in `fmt` and the test suite (`-prettier-command`,
  `-prettier-required` flags): ghtmx has no Node.js dependency anywhere in
  the toolchain. Migration: none required; `ghtmx fmt` output no longer
  reformats `<script>`/`<style>` bodies via Prettier.
- The legacy `templ.WriteWatchModeString` API is not carried over.
  Migration: none required; watch mode uses the dev-mode text sidecars
  automatically.
- `ghtmx.Class`, `ghtmx.SafeClass`, and `ghtmx.ConstantCSSClass` are carried
  over (the conformance corpus exercises them) but remain deprecated and are
  slated for removal before v1.0. Migration: use plain strings for CSS
  classes.
