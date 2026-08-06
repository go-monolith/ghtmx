# Changelog

## Unreleased

- The error shown when `ghtmx lsp` will not start is now a way out
  rather than a dead end. It names both binaries the extension needs and
  offers to install them, to open the `ghtmx.path` setting, or to reload
  the window. The installer also has a command of its own,
  **ghtmx: Install ghtmx and gopls**, which opens a terminal with the
  install line typed but not run — you press Enter. On Windows it points
  at the manual steps, since the script is bash-only.

## 0.1.0

- Initial release: syntax highlighting for `.ghtmx` (templates,
  fragments, events, htmx attributes, route bindings) and language-server
  wiring for `ghtmx lsp` (diagnostics, completion, hover, definition).
  Tested against the ghtmx module v0.1 series.
