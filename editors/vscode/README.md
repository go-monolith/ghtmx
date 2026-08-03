# ghtmx for Visual Studio Code

Language support for `.ghtmx` and `.htmx` templates:

- Syntax highlighting for the full template language, including the
  ghtmx-native constructs: `fragment` and `event` declarations, htmx
  attributes, and route bindings (`hx-post={ handlers.CreateItem }`).
- A file icon, in light and dark variants, for file icon themes that
  show language-contributed icons.
- Language-server wiring: the extension launches `ghtmx lsp`, which
  provides live diagnostics, route-aware completion, hover, and go to
  definition, and proxies gopls for the embedded Go.

## File extensions

`.ghtmx` is the canonical extension. `.htmx` is accepted because some
projects prefer it, and gets the same highlighting and icon.

**The toolchain currently matches `.ghtmx` only.** `ghtmx generate`
discovers `*.ghtmx`, and `ghtmx lsp` filters the workspace the same way,
so a `.htmx` file is highlighted but produces no generated Go and no
diagnostics. Name files `.ghtmx` unless you only want highlighting.

## Requirements

- The `ghtmx` binary on PATH (or set `ghtmx.path`):
  `go install github.com/go-monolith/ghtmx/cmd/ghtmx@latest`
- `gopls` on PATH for embedded-Go support:
  `go install golang.org/x/tools/gopls@latest`

## Settings

| Setting | Default | Purpose |
| --- | --- | --- |
| `ghtmx.path` | `ghtmx` | Path to the ghtmx binary. |
| `ghtmx.log` | (empty) | File to write language-server logs to. |
| `ghtmx.goplsLog` | (empty) | File to write the proxied gopls logs to. |
| `ghtmx.goplsRPCTrace` | `false` | Trace ghtmx↔gopls JSON-RPC into the gopls log. |

Settings are read when the server starts: after changing one, reload
the window (`Developer: Reload Window`) to apply it.

Embedded Go expressions are colored through the built-in Go grammar;
install the [Go extension](https://marketplace.visualstudio.com/items?itemName=golang.go)
for the richest embedded-Go highlighting.

## Building and packaging

```bash
cd editors/vscode
npm install
npm run compile     # type-check and emit out/extension.js
npm run package     # produce ghtmx-vscode-<version>.vsix via vsce
```

Install the local build with `code --install-extension ghtmx-vscode-<version>.vsix`.

## Releasing

1. Bump `version` in `package.json` and add a `CHANGELOG.md` entry.
   Follow the versioning policy in [`editors/README.md`](../README.md):
   the MAJOR.MINOR must match the ghtmx module series the extension was
   tested against.
2. `npm run package` and smoke-test the `.vsix` locally.
3. Publish with `npx vsce publish` (requires a marketplace token for the
   `go-monolith` publisher).
