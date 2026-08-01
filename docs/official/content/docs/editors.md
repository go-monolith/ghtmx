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

The extensions are deliberately excluded from the Go module's build and
CI toolchain: packaging them needs Node (VS Code) or a JDK (JetBrains),
and the engine's constitution keeps those out of the module pipeline.
`editors_test.go` validates the artifacts' structure with the standard
Go toolchain instead.
