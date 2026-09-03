### Added

- Three htmx 4 examples, each pinned to `4.0.0` in its own `ghtmx.json`
  and served as live demos on ghtmx.dev: `examples/htmx4-inheritance`
  (`:inherited` and `:inherited:append`), `examples/htmx4-status`
  (`hx-status:422`/`5xx` routing, `hx-disable`, `<hx-partial>`), and
  `examples/htmx4-query` (the bound `QUERY` verb with `innerMorph`).
- `ghtmx.IsHistoryRestoreRequest`: reports an htmx back/forward refetch
  (`HX-History-Restore-Request: true`).

### Fixed

- `ghtmx generate -path <dir>` below the module root resolves template
  packages against the module, so a nested project with its own
  `ghtmx.json` (a differently pinned example) binds bare handler
  identifiers correctly instead of reporting `GHTMX-E0101`.

### Changed

- The adapters' automatic render mode answers a history-restore request
  with the full page instead of the fragment. htmx 4 selects the
  `[hx-history-elt]` element out of that response and swaps nothing when
  it is missing, so a fragment blanked the region on back navigation;
  htmx 2 accepts the page as well. Migration: handlers or tests that
  expected the fragment on a request carrying
  `HX-History-Restore-Request` now get the page; pass
  `Mode(ModeStandalone)` to keep the old shape.
- The documentation site runs on htmx 4.0.0, with explicit `:inherited`
  attributes on `<body>` and compiler-checked `hx-on::` listeners, and
  the Getting Started guide builds an htmx 4 project (a `ghtmx.json`
  step pinning `4.0.0`). Migration: none required — the default pin for
  projects without a `ghtmx.json` stays `2.0.10`.
