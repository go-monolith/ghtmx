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

Installing this extension does **not** install any binaries. It needs:

- The `ghtmx` binary on PATH (or set `ghtmx.path`):
  `go install github.com/go-monolith/ghtmx/cmd/ghtmx@latest`
- `gopls` for embedded-Go support:
  `go install golang.org/x/tools/gopls@latest`
  (`ghtmx lsp` looks for it on PATH, then in `~/go/bin`, then in
  `~/.local/bin`. The script below pins the version this repository
  tests against rather than taking `@latest`.)

On Linux, macOS, and WSL, one command does both — it takes `ghtmx` from
the release archive, so no Go toolchain is needed for that half:

```sh
curl -fsSL https://raw.githubusercontent.com/go-monolith/ghtmx/main/scripts/install.sh | bash
```

Run before this extension is installed, that same line offers to install
it too: it looks for the `code` CLI and, when the extension is missing,
asks before fetching the `.vsix` from the release. So a new machine goes
from nothing to a working setup in one command.

If the server fails to start, the extension offers to run the script for
you; it is also in the command palette as **ghtmx: Install ghtmx and
gopls**. The command opens a terminal with the line typed but not
executed — you press Enter. It carries `--no-interactive`, which turns
off the prompts: the extension is already installed in that case, and
there is nothing to ask. On Windows it points at the manual steps
instead, since the script is bash-only.

The script installs the newest release. Pre-1.0 that is not guaranteed
to be in the module series this extension was tested against (see the
compatibility table in [`editors/README.md`](../README.md)); set
`GHTMX_VERSION` to pin a tag.

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
