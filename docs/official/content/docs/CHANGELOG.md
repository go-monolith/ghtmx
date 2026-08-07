# Changelog

All notable changes to ghtmx are documented in this file.

Pre-1.0 stability posture: breaking changes to language syntax, generated-code
shape, and the runtime API are allowed between minor versions and are always
recorded here with a migration note. The discipline is enforced: every entry
in a Changed, Removed, or Breaking section must carry a `Migration:` note
("Migration: none required" is valid), and `internal/installcheck` fails the
build otherwise. Releases follow `RELEASING.md`.

## [Unreleased]

### Added

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
