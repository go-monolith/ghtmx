### Added

- htmx 4.0.0 support, side by side with htmx 2.0.x: pin `htmxVersion:
  "4.0.0"` in `ghtmx.json` (or `-htmx-version 4.0.0`) and the compiler,
  `ghtmx generate -check`, the language server, and `ghtmxgen.HTMXScript()`
  follow htmx 4 syntax — the `:inherited`/`:append` attribute-name
  modifiers, `hx-status:<code>`, `hx-query`, `hx-action`/`hx-method`,
  `hx-config`, `hx-ignore` and the new `hx-disable`, the morph swap styles
  and `showTarget:`/`scrollTarget:` modifiers, the htmx 4 trigger
  modifiers, colon-form event names (`hx-on::after:swap`), the attributes
  of every extension shipped with htmx 4, and `<hx-partial>`. The 4.0.0
  script asset is pinned with its subresource-integrity hash. Projects
  without an explicit pin stay on 2.0.10.
- Migration hints under an htmx 4 pin: htmx 2 leftovers (`hx-vars`,
  `hx-ext`, `hx-disabled-elt`, `hx-request`, `hx-target-404`, `hx-on-click`,
  `hx-on::after-swap`, `queue:` triggers, `show:#id:top`, `focus-scroll:`,
  `hx-include="inherit"`) report `GHTMX-E0501` naming the replacement
  instead of a generic unknown-attribute error.
- `GHTMX-W0202`: under htmx 4 an inheritable attribute without
  `:inherited` on an element that issues no request, while a descendant
  does, warns that the descendant no longer sees it (htmx 4 inheritance is
  explicit); `hx-headers` and `hx-boost` wrappers warn on their own, with
  a CSRF remark when the header looks like a token. Silence with
  `GHTMX-W0202=off`.
- Typed swap API for htmx 4: `ghtmx.SwapInnerMorph`, `SwapOuterMorph`,
  `SwapOuterSync`, the `SwapBefore`/`SwapPrepend`/`SwapAppend`/`SwapAfter`
  aliases, and the `SwapScrollTarget`, `SwapShowTarget`, `SwapTarget`,
  `SwapStrip`, `SwapEmpty`, and `SwapFocusScrollV4` modifiers.
- `QUERY` as a bindable route verb (`hx-query={ handler }`,
  `//ghtmx:route QUERY /search handlers.Search`).
- Language-server completion for htmx 4: htmx events after `hx-on::`,
  `:inherited`/`:append` after an inheritable attribute, `hx-status:`,
  morph styles and aliases for `hx-swap`, and `hx-query` bindings.

### Changed

- Under an htmx 4 pin the generated central package no longer emits the
  `Emit<Event>AfterSwap` and `Emit<Event>AfterSettle` symbols: htmx 4
  removed the `HX-Trigger-After-Swap` and `HX-Trigger-After-Settle`
  response headers they set. Projects pinned to htmx 2 are unchanged.

  Migration: none required for htmx 2 projects. When moving a project to
  `htmxVersion: "4.0.0"`, replace calls to the `AfterSwap`/`AfterSettle`
  emitters with the plain `Emit<Event>` and listen for the event on
  `htmx:after:swap` if the timing mattered.
- `GHTMX-E0501` now also covers constructs the pinned version removed or
  renamed (previously only constructs newer than the pin), and the
  unsupported-version error (`GHTMX-E0502`) lists every supported
  version family.

  Migration: none required.
