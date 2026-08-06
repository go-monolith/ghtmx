# Editor support

Extensions wiring editors to the ghtmx toolchain. Each is a thin client:
syntax highlighting is local, and everything else — live diagnostics,
route-aware completion, hover, go to definition — comes from `ghtmx lsp`
(FR-080–FR-084), so behavior stays identical across editors.

| Editor | Directory | Highlighting | LSP wiring |
| --- | --- | --- | --- |
| VS Code | [`vscode/`](vscode/) | TextMate grammar | `vscode-languageclient` launching `ghtmx lsp` |
| Neovim | [`nvim/`](nvim/) | Vim syntax file | `vim.lsp.start` launching `ghtmx lsp` |
| JetBrains | [`jetbrains/`](jetbrains/) | Shared TextMate bundle | Platform LSP API launching `ghtmx lsp` |

All three highlight the ghtmx-native constructs: `templ`, `fragment`,
and `event` declarations, htmx attributes, and route bindings
(`hx-post={ handlers.CreateItem }`).

## The binaries are not bundled

Installing an extension installs no binaries. Every integration spawns
`ghtmx lsp` off your PATH, and that server in turn needs `gopls` for the
embedded Go — it looks on PATH, then in `~/go/bin`, then in
`~/.local/bin`. Those last two are where a default Go toolchain puts
things, so it usually just works; with a customized `GOBIN` or `GOPATH`,
put that directory on your PATH. Install both first (Linux, macOS, WSL):

```sh
curl -fsSL https://raw.githubusercontent.com/go-monolith/ghtmx/main/scripts/install.sh | bash
```

That script is a wrapper around the release-archive install path; see
the root [`README.md`](../README.md) for the manual steps and the
Windows route. The VS Code extension also offers to run it from the
error it shows when the server will not start, and from the command
palette as **ghtmx: Install ghtmx and gopls**. It installs the newest
release, which pre-1.0 is not guaranteed to fall in the module series
the extension was tested against — set `GHTMX_VERSION` to pin.

The VS Code TextMate grammar
(`vscode/syntaxes/ghtmx.tmLanguage.json`) is the single source of
truth; the JetBrains bundle ships a byte-identical copy, enforced by
`editors_test.go`. One caveat: the grammar's `source.go`, `source.css`,
and `source.js` includes resolve against grammars the host editor
provides, so embedded-language coloring varies by editor (VS Code's
built-ins cover them; JetBrains falls back to plain text inside
embedded regions).

## Versioning

Extension versions are related to the ghtmx module version by series:

- An extension versioned `X.Y.*` is developed and tested against the
  ghtmx module series `vX.Y.*` and its `ghtmx lsp`.
- Extension PATCH releases are extension-only fixes and never require a
  module update; module PATCH releases never change the LSP surface, so
  any `X.Y.*` extension works with any `vX.Y.*` module.
- When a module MINOR release changes the LSP surface (new
  capabilities, changed completion behavior), each extension gets a
  matching `X.Y.0` release, even if its own code is unchanged.

Current compatibility:

| Extension | Version | ghtmx module series |
| --- | --- | --- |
| VS Code (`vscode/package.json`) | 0.1.0 | v0.1 |
| Neovim (released with the repository) | 0.1.0 | v0.1 |
| JetBrains (`jetbrains/build.gradle.kts`) | 0.1.0 | v0.1 |

`editors_test.go` pins the VS Code and JetBrains versions to this table.

## Release path

1. Verify the LSP against the target module version: the protocol suite
   and latency gate (`go test ./cmd/ghtmx/lspcmd/`, task 56) must be
   green at the module tag being released.
2. Bump each extension's version per the policy above and update the
   compatibility table here.
3. Package and publish per editor — see
   [`vscode/README.md`](vscode/README.md),
   [`nvim/README.md`](nvim/README.md), and
   [`jetbrains/README.md`](jetbrains/README.md).

The extensions stay out of the Go module's own build: packaging them
needs Node (VS Code) or a JDK (JetBrains), and the engine's constitution
keeps those out of the module pipeline. `editors_test.go` validates
their structure with the standard Go toolchain instead.

They are packaged in CI, though, by `.github/workflows/editors.yml`:

- A release attaches three artifacts — `ghtmx-vscode-<version>.vsix`,
  `ghtmx-jetbrains-<version>.zip`, and `ghtmx-nvim-<version>.tar.gz` —
  so an editor can be installed from the release page without a Node or
  JDK toolchain locally.
- Each is packaged independently, so a packaging failure costs one
  artifact rather than all three. The JetBrains build is the likeliest
  to fail: it resolves an IntelliJ platform from JetBrains'
  repositories. A release can therefore be published with an asset
  missing — the workflow run goes red, but the release itself carries no
  sign of it, so check the `editors` run if an expected asset is absent.
- Each is built from its own manifest, not stamped with the module tag.
  A `0.1.0` extension attached to `v0.1.4` is the versioning policy
  working as intended: any `X.Y.*` extension serves any `vX.Y.*` module.
- A pull request touching `editors/` builds all three without uploading,
  so a packaging break surfaces on the PR rather than mid-release.
