# ghtmx

[![codecov](https://codecov.io/gh/go-monolith/ghtmx/graph/badge.svg?token=8WD6GCUT9G)](https://codecov.io/gh/go-monolith/ghtmx)

A compiled Go template engine where htmx is a first-class,
compile-checked language concept — a hard fork of
[templ](https://github.com/a-h/templ) (see `TEMPL_SYNTAX_BASELINE.md`
and `NOTICE`) that adds route-aware bindings, compile-time fragments,
and a server-driven event contract, with a stdlib-only runtime.

- `hx-post={ handlers.CreateUser }` resolves against your actual Go
  routes at build time; renaming a route breaks the build at every
  binding site.
- `fragment` blocks compile to byte-identical inline and standalone
  entry points for htmx partial updates.
- `event` declarations generate the only symbols that can emit
  `HX-Trigger`, with typed payloads.

Documentation: [ghtmx.dev](https://ghtmx.dev) is the published site,
built from `docs/official/` — itself a ghtmx application, serving these
documents and every example, compiled natively or to wasm. The sources
it renders are `SYNTAX.md` (the language), `DIAGNOSTICS.md` (every
diagnostic), `CONFIG.md` (settings and flags), and the site-only pages
under `docs/official/pages/` (the landing page, getting started, build
targets). The Go API reference is on
[pkg.go.dev](https://pkg.go.dev/github.com/go-monolith/ghtmx).

## Install

Two supported paths, verified on Linux, macOS, and Windows by
`internal/installcheck` on every CI run:

**With the Go toolchain:**

```sh
go install github.com/go-monolith/ghtmx/cmd/ghtmx@latest
go install golang.org/x/tools/gopls@latest   # embedded-Go support in the LSP
```

**From a release archive:** download the archive for your platform
from the [GitHub release](https://github.com/go-monolith/ghtmx/releases),
verify it against `checksums.txt`, and put the binary on your PATH:

```sh
sha256sum --check --ignore-missing checksums.txt    # macOS: shasum -a 256 --check
tar -xzf ghtmx_<version>_linux_amd64.tar.gz ghtmx   # .zip on Windows (verify with Get-FileHash)
```

On Linux, macOS, and WSL, `scripts/install.sh` does those steps for you
and installs `gopls` alongside when a Go toolchain is present:

```sh
curl -fsSL https://raw.githubusercontent.com/go-monolith/ghtmx/main/scripts/install.sh | bash
```

It reads `GHTMX_VERSION` (default: the latest release) and
`GHTMX_BIN_DIR` (default: `$GOBIN`, then `$GOPATH/bin`, then
`~/.local/bin`), verifies the checksum before installing anything, and
tells you what to add to your PATH — it never edits startup files. It is
a convenience wrapper around the release archive, not a third path;
native Windows uses the manual steps above.

**VS Code users:** that same line is the whole setup. When it finds the
`code` CLI and the ghtmx extension is not installed yet, it asks whether
to add it, and installs the `.vsix` from the same release if you say
yes:

```text
Install the ghtmx VS Code extension (syntax highlighting, diagnostics, completion)? [y/N]
```

It only ever asks — answering anything but `y` installs nothing, and a
run with no terminal to ask on (a pipeline, CI) skips the question. Pass
`--no-interactive` to turn every prompt off, `GHTMX_INSTALL_VSCODE=1` to
say yes in advance, or `GHTMX_SKIP_VSCODE=1` to never be asked:

```sh
curl -fsSL https://raw.githubusercontent.com/go-monolith/ghtmx/main/scripts/install.sh | bash -s -- --no-interactive
```

Both paths yield the same single-version binary: `ghtmx version`
prints the release tag.

## Editor support

The VS Code, Neovim, and JetBrains extensions are attached to the
[GitHub releases](https://github.com/go-monolith/ghtmx/releases)
alongside the binaries, from v0.1.5 onward. They are thin clients:
highlighting is local, and diagnostics, completion, hover, and go to
definition all come from `ghtmx lsp`, so behavior is the same in all
three. Each needs `ghtmx` and `gopls` already on your PATH — installing
an extension does not install them; `scripts/install.sh` above does. It
installs the VS Code extension as well, so the table below is the route
for the other two editors and for picking a specific version by hand.

| Editor | Release asset | Install |
| --- | --- | --- |
| VS Code | `ghtmx-vscode-<ext>.vsix` | `code --install-extension ghtmx-vscode-<ext>.vsix` |
| JetBrains | `ghtmx-jetbrains-<ext>.zip` | Settings → Plugins → ⚙ → Install Plugin from Disk |
| Neovim | `ghtmx-nvim-<ext>.tar.gz` | Extract onto your runtimepath |

`<ext>` is the extension version, which is not the release tag: an
extension versioned `X.Y.*` works with any module `vX.Y.*`, so a `0.1.0`
extension is the right one to take off the `v0.1.5` release.
[`editors/README.md`](editors/README.md) has the compatibility table
and the per-editor setup, including the LSP4IJ requirement on JetBrains
community editions.

## The CLI beyond `generate`

Two flags are easy to miss and belong in a consumer's Makefile or CI
alongside `ghtmx generate -check`:

- `ghtmx routes` prints the discovered route table, and
  `ghtmx routes -json` emits it machine-readably — verb, path, handler,
  origin, recognizer, and source position — so scripts and tests can
  assert what the toolchain believes your application serves, instead
  of recomputing it.
- `ghtmx fmt -fail` writes nothing and exits non-zero if any template
  would change: the CI form of `ghtmx fmt`.
- `ghtmx routes -check-against routes.json` compares that table against
  the routes your own router reports, and fails on any disagreement. The
  [`routetable`](https://pkg.go.dev/github.com/go-monolith/ghtmx/routetable)
  package is the same thing for a Go test — `Load` reads the table,
  `Normalize` brings your framework's path syntax into line, and `Diff`
  reports what the two disagree about:

  ```go
  declared, err := routetable.Load(".")
  actual := routetable.Normalize(fromMyRouter(), routetable.ColonStyle)
  if report := routetable.Report(routetable.Diff(declared, actual)); report != "" {
      t.Errorf("route table and router disagree:\n%s", report)
  }
  ```

  This matters most once you take the `//ghtmx:route` escape hatch:
  `generate -check` verifies template-versus-generated-code drift, but
  only this verifies that the paths ghtmx believes in are the paths your
  framework actually serves.

`CONFIG.md` documents both, along with the dev-mode environment
variables that let a running application hot-reload template text
without recompiling.

## Test coverage

Every CI run publishes its profile to
[Codecov](https://codecov.io/gh/go-monolith/ghtmx), which is where the
badge above, the line-by-line report, and each pull request's coverage
diff come from. Codecov reports; it does not gate, and its line-coverage
figure is not the statement-coverage number the gate below measures.

The gate is the `coverage` CI job, which holds statement coverage of the
project's own code at **90% or above** and fails the build below that
floor. The threshold is a compile-time constant in `internal/covergate`,
so lowering it is a reviewed diff rather than a workflow edit.

"Own code" is what this project writes and is answerable for. Both the
gate and the Codecov report exclude:

| Excluded | Why |
| --- | --- |
| `*_ghtmx.go` | Generated by the compiler; gated by `internal/corpusgate` and the conformance suite instead |
| `internal/lsp/**` | Ported from the Go Language Server project (see `NOTICE`) |
| `internal/safehtml/**` | Adapted from `google/safehtml` (see `NOTICE`) |
| `examples/**` | Documentation; compiled and vetted by `internal/corpusgate` |
| `**/fixture/**`, `**/testprogram/**` | Test input, not code under test |

`CONTRIBUTING.md` has the command that reproduces the gate locally and
why its flags are what they are.

### Coverage in consuming projects

The same reasoning applies to a project built on ghtmx: committed
`*_ghtmx.go` files are verified by compile, vet, and this repository's
conformance suite — not by your line coverage. Linters that honour the
`// Code generated by ghtmx - DO NOT EDIT.` header (golangci-lint does)
already skip them, but Go's own `-cover` does not, so a coverage gate
will count generated statements against you unless you exclude them the
way this repository does.

For Codecov, ignore the generated suffix in `codecov.yml`:

```yaml
ignore:
  - "*_ghtmx.go"
  - "**/*_ghtmx.go"
```

For a threshold checked from the profile itself, strip generated files
before measuring (the suffix is your `generatedSuffix` setting,
`_ghtmx.go` by default):

```sh
go test -coverprofile=cover.out ./...
grep -v '_ghtmx.go:' cover.out > cover.own.out
go tool cover -func=cover.own.out
```

## Stability (pre-1.0)

ghtmx is pre-1.0: breaking changes to language syntax, generated-code
shape, and the runtime API may land between minor versions. Every
breaking change carries a `CHANGELOG.md` entry with a migration note —
that discipline is enforced by a build gate. Pin a version and read
the changelog before upgrading.

## License

Apache-2.0. Portions derived from templ (MIT) — see `NOTICE`.
