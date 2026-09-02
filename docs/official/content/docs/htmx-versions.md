# htmx versions

ghtmx pins one htmx version per project and validates every `hx-*`
attribute — names, values, combinations — against that version's
surface. The pin also decides which script `ghtmxgen.HTMXScript()`
serves, so the markup the compiler checked and the htmx the browser
runs cannot disagree.

| | |
| --- | --- |
| Default | **2.0.10** |
| Supported | any **2.0.0 – 2.0.10** release, and **4.0.0** |
| Configure | `"htmxVersion"` in `ghtmx.json`, or `ghtmx generate -htmx-version` |

```json
{
  "htmxVersion": "4.0.0"
}
```

A version outside the supported set fails fast with `GHTMX-E0502`. A
construct the pinned version lacks — newer than the pin, or removed or
renamed by it — reports `GHTMX-E0501` naming the replacement, so the
same template tells you what to change whichever direction you move.

## htmx 2.0.x

The default. Templates use the htmx 2 surface as documented at
htmx.org: implicit attribute inheritance, `hx-on:<event>` (the
`hx-on-<event>` dash form is accepted too), kebab-case htmx events
(`hx-on::after-request`), `hx-vars`, `hx-ext`, `hx-disabled-elt`, the
`queue:` trigger modifier, and `show:#id:top`-style swap modifiers.
Value keywords carry their own version metadata — `hx-include="inherit"`
is valid from 2.0.5 — and each release has a pinned script asset with
its subresource-integrity hash.

## htmx 4.0.0

Pinning `4.0.0` switches the whole toolchain — compiler,
`ghtmx generate -check`, language server, generated script helper — to
htmx 4 syntax:

- **Explicit inheritance.** An attribute reaches descendants only with
  the `:inherited` modifier (`hx-target:inherited="#out"`); `:append`
  extends an inherited value instead of replacing it
  (`hx-vals:inherited:append`). The modifiers are accepted on the
  attributes htmx 4 inherits and rejected elsewhere.
- **Per-status swaps.** `hx-status:422="target:#errors select:#validation-errors"`,
  with exact codes and `50x`/`5xx` wildcards, in htmx's `key:value`, comma
  or JSON form.
- **New attributes.** `hx-query`, `hx-action` with `hx-method`,
  `hx-config`, `hx-ignore`, and `hx-disable` in its new meaning (the
  elements to disable during a request). `hx-morph-skip` and
  `hx-morph-skip-children` steer the built-in morph.
- **Swaps and triggers.** The `innerMorph`, `outerMorph`, and
  `outerSync` styles, the `before`/`prepend`/`append`/`after` aliases,
  the `showTarget:`, `scrollTarget:`, `focusScroll:`, `strip:`,
  `swapEmpty:`, and `target:` modifiers; the `prevent`, `stop`, `halt`,
  `capture`, and `passive` trigger modifiers.
- **Colon-form events.** `hx-on::after:swap`, and
  `hx-trigger="htmx:after:swap from:body"`.
- **Extensions without `hx-ext`.** The attributes of every extension
  shipped with htmx 4 are known — `hx-sse:connect`, `hx-ws:send`,
  `hx-live`, `hx-preload`, `hx-prompt`, `hx-targets`, and the rest —
  since loading the script is all htmx 4 needs.
- **`<hx-partial>`.** The element (and its `<template hx type="partial">`
  form) is validated like any other.

The generated central package drops the `Emit<Event>AfterSwap` and
`Emit<Event>AfterSettle` emitters, because htmx 4 removed the response
headers they set; the plain `Emit<Event>` stays. The typed swap API
gains `ghtmx.SwapInnerMorph`, `SwapOuterMorph`, `SwapOuterSync`, the
insertion aliases, and the `SwapShowTarget`, `SwapScrollTarget`,
`SwapTarget`, `SwapStrip`, `SwapEmpty`, and `SwapFocusScrollV4`
modifiers.

## Moving a project from 2 to 4

1. Set `"htmxVersion": "4.0.0"` and run `ghtmx generate`. Every htmx 2
   leftover is reported with its replacement:

   | You wrote | You get |
   | --- | --- |
   | `hx-vars` | `GHTMX-E0501`: use `hx-vals` with a `js:` prefix |
   | `hx-disabled-elt` | `GHTMX-E0501`: renamed to `hx-disable` |
   | `hx-disable` (bare) | `GHTMX-E0202`: the htmx 2 flag is `hx-ignore` |
   | `hx-ext`, `hx-request`, `hx-disinherit`, `hx-inherit`, `hx-params` | `GHTMX-E0501` with the htmx 4 way |
   | `hx-target-404` | `GHTMX-E0501`: use `hx-status:404` |
   | `hx-on::after-swap`, `hx-on-click` | `GHTMX-E0501`: `hx-on::after:swap`, `hx-on:click` |
   | `hx-trigger="click queue:first"` | `GHTMX-E0202`: queue with `hx-sync` |
   | `hx-swap="innerHTML show:#x:top"` | `GHTMX-E0202`: `show:top showTarget:#x` |
   | `hx-include="inherit"` | `GHTMX-E0202`: put `:inherited` on the ancestor |

2. Fix the `GHTMX-W0202` warnings. htmx 2 let a wrapper's `hx-target`,
   `hx-swap`, `hx-headers`, or `hx-boost` apply to everything beneath
   it; under htmx 4 that markup is valid and silently wrong. The warning
   names the wrapper, the attribute, and the descendant that issues the
   request — add `:inherited`, or move the attribute onto the requesting
   element. `hx-headers` on a layout warns on its own (its children are
   rendered elsewhere), with a note when the value looks like a CSRF
   token. Silence the check with `"checks": {"GHTMX-W0202": "off"}` if
   the page enables `htmx.config.implicitInheritance` instead.

3. Replace calls to the `AfterSwap`/`AfterSettle` emitters with the plain
   `Emit<Event>`, and listen on `htmx:after:swap` where the timing
   mattered.

Pinning back to `2.0.10` reverses the checks: every htmx 4 construct
reports `GHTMX-E0501` as introduced in 4.0.0, so a template cannot mix
generations unnoticed.

## Self-hosting the script

`ghtmxgen.HTMXScript()` emits the jsDelivr tag for the pinned version
with its subresource-integrity hash. To serve the file yourself, pass
`ghtmx.WithScriptSrc("/static/htmx.min.js")`; the hash stays, so the
vendored file must be the exact published build — `ghtmx.HTMXScriptIntegrity`
returns the pin for a test to assert against.
