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

The extension is chosen per project in `ghtmx.json`:

```json
{
  "templateExtension": ".htmx"
}
```

`.ghtmx` is the default and needs no configuration; `.htmx` is the one
alternative. A project uses **exactly one** — with `.htmx` configured, a
`.ghtmx` file is not a template and is neither generated from nor served
by the language server.

The editor claims both extensions regardless, so highlighting and the
icon work either way. Generation and diagnostics follow the configured
value, so set it to match the files you actually write.

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
