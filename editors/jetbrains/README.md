# ghtmx for JetBrains IDEs

Language support for `.ghtmx` templates in IntelliJ-platform IDEs:

- Syntax highlighting through the shared ghtmx TextMate bundle (the
  same grammar the VS Code extension uses; the repository's editors
  test keeps the two copies byte-identical).
- Language-server wiring through the IntelliJ Platform LSP API: the
  plugin launches `ghtmx lsp`, which provides diagnostics, completion,
  hover, and go to definition, and proxies gopls for the embedded Go.

## Requirements

- A commercial JetBrains IDE 2024.2+ (GoLand, IntelliJ IDEA Ultimate,
  …). The native LSP client API is not available in Community
  editions — there, install [LSP4IJ](https://plugins.jetbrains.com/plugin/23257-lsp4ij)
  and register `ghtmx lsp` for the `ghtmx` file type manually.
- `ghtmx` and `gopls` on PATH:
  `go install github.com/go-monolith/ghtmx/cmd/ghtmx@latest`
  `go install golang.org/x/tools/gopls@latest`

## Building

The plugin builds with the IntelliJ Platform Gradle Plugin. A Gradle
wrapper is not committed — install Gradle 8.10+ and JDK 17+ (or run
`gradle wrapper` once to generate one):

```bash
cd editors/jetbrains
gradle buildPlugin   # produces build/distributions/ghtmx-jetbrains-<version>.zip
gradle runIde        # launches a sandbox IDE with the plugin installed
```

Install the zip via Settings → Plugins → ⚙ → Install Plugin from Disk.

## Releasing

1. Bump `version` in `build.gradle.kts`. Follow the versioning policy in
   [`editors/README.md`](../README.md): the MAJOR.MINOR must match the
   ghtmx module series the plugin was tested against.
2. `gradle buildPlugin` and smoke-test the zip in a sandbox IDE
   (`gradle runIde`).
3. Upload to the JetBrains Marketplace: `PUBLISH_TOKEN=<token> gradle
   publishPlugin` (the token is wired in `build.gradle.kts`), or upload
   manually through the vendor page.
