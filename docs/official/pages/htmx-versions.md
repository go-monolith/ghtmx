# htmx versions

ghtmx pins one htmx version per project and validates every `hx-*`
attribute — names, values, combinations — against that version's
surface. The pin also decides which script `ghtmxgen.HTMXScript()`
serves, so the markup the compiler checked and the htmx the browser
runs cannot disagree.

```json
{
  "htmxVersion": "4.0.0"
}
```

Set it in `ghtmx.json`, or per invocation with
`ghtmx generate -htmx-version 4.0.0`. Without a pin the project is on
the default, **2.0.10**. ghtmx.dev itself is pinned to `4.0.0`: the
page you are reading declares its swap target once on `<body>` with
`hx-target:inherited`, and the `htmx4-*` entries under
[Examples](/examples) are complete htmx 4 applications you can copy.

## Supported versions

"Supported" means all three at once: the attribute surface the compiler
and language server validate against, a script asset pinned by its
subresource-integrity hash, and the typed Go API (`ghtmx.SwapStyle`,
the `HX-*` header helpers) covering what the version accepts. Every
version below has all three; `ghtmx.SupportedHtmxVersions()` returns
the same list at runtime, and a build gate keeps this table in step
with it.

| Version | Line | Notes |
| --- | --- | --- |
| `2.0.0` | htmx 2 | |
| `2.0.1` | htmx 2 | |
| `2.0.2` | htmx 2 | |
| `2.0.3` | htmx 2 | |
| `2.0.4` | htmx 2 | |
| `2.0.5` | htmx 2 | adds the `inherit` keyword on `hx-include`, `hx-indicator`, `hx-disabled-elt` |
| `2.0.6` | htmx 2 | |
| `2.0.7` | htmx 2 | |
| `2.0.8` | htmx 2 | |
| `2.0.9` | htmx 2 | |
| `2.0.10` | htmx 2 | **default pin** |
| `4.0.0` | htmx 4 | explicit inheritance, `hx-status`, morph swaps — see below |

Not supported: htmx 1.x (its `hx-on="…"` and `hx-ws`/`hx-sse` attributes
are gone in 2 and 4 alike), any 3.x number (htmx skipped it), and
prereleases. A pin outside the table fails fast with `GHTMX-E0502`
naming the supported set. A construct the pinned version lacks — newer
than the pin, or removed or renamed by it — reports `GHTMX-E0501`
naming the replacement, so the same template tells you what to change
whichever direction you move.

## htmx 2.x versus 4.x: what changes

The differences the compiler enforces and the ones your handlers see.
Everything in the 4.x column is validated under a `4.0.0` pin; the 2.x
column stays exactly as it was for projects on 2.0.x.

| | htmx 2.x | htmx 4.x |
| --- | --- | --- |
| **Attribute inheritance** | implicit: `<div hx-target="#out">` applies to every descendant | explicit: only `hx-target:inherited="#out"` reaches descendants; `:append` extends an inherited value. `GHTMX-W0202` flags wrappers that lost their reach |
| **Request attributes** | `hx-get` `hx-post` `hx-put` `hx-patch` `hx-delete` | the same five plus `hx-query`, and `hx-action` with `hx-method` (form-style) |
| **Listeners and events** | `hx-on:click`, `hx-on-click`, kebab-case htmx events: `hx-on::after-request` | `hx-on:click` only; colon-form events: `hx-on::after:request`, `hx-trigger="htmx:after:swap from:body"` |
| **Per-status handling** | `hx-target-404` (response-targets extension) | `hx-status:404="target:#errors"`, with `40x`/`4xx` wildcards; 4xx/5xx responses swap by default |
| **Swap styles** | `innerHTML` … `none`, `delete` | plus `innerMorph`, `outerMorph`, `outerSync`, and the `before`/`prepend`/`append`/`after` aliases |
| **Swap modifiers** | `show:#id:top`, `scroll:#id:bottom`, `focus-scroll:` | `show:top showTarget:#id`, `scroll:bottom scrollTarget:#id`, `focusScroll:`, `strip:`, `swapEmpty:`, `target:` |
| **Trigger modifiers** | `queue:first` … | `queue:` removed (use `hx-sync`); `prevent`, `stop`, `halt`, `capture`, `passive` added |
| **Removed attributes** | `hx-vars`, `hx-params`, `hx-ext`, `hx-request`, `hx-disinherit`, `hx-inherit`, `hx-disabled-elt`, `hx-history` all valid | all reported with their replacement: `hx-vals js:`, `htmx:config:request`, script inclusion, `hx-config`, `:inherited`, `hx-disable` |
| **`hx-disable`** | a flag: skip htmx processing | the elements to disable during a request (htmx 2's `hx-disabled-elt`); the old flag is `hx-ignore` |
| **Extensions** | activated per element with `hx-ext="sse"`; attributes such as `sse-connect` | loading the script activates it; attributes are namespaced and known to the compiler: `hx-sse:connect`, `hx-ws:send`, `hx-live`, `hx-preload`, `hx-prompt`, `hx-targets`, … |
| **Multi-target responses** | `hx-swap-oob` | `hx-swap-oob` still works; `<hx-partial hx-target hx-swap>` is the clearer form, and out-of-band content swaps *after* the main content |
| **Request headers your handlers read** | `HX-Trigger` (id), `HX-Trigger-Name`, `HX-Target` (id), `HX-Prompt` | `HX-Source` and `HX-Target` as `tag#id`, `HX-Request-Type` (`full`/`partial`); `HX-Trigger-Name` and `HX-Prompt` gone. `ghtmx.IsHTMXRequest` works on both |
| **Response headers** | `HX-Trigger`, `HX-Trigger-After-Settle`, `HX-Trigger-After-Swap`, … | the `After-*` pair is removed; the generated `Emit<Event>AfterSwap`/`AfterSettle` symbols disappear with it, `Emit<Event>` stays |
| **Form data on `hx-delete`** | the enclosing form's values are sent | not sent (like `hx-get`); add `hx-include="closest form"` |
| **Request timeout** | none | 60 s by default (`htmx.config.defaultTimeout`) |
| **History** | cached in `localStorage`; a cache miss refetches with `HX-History-Restore-Request` | always refetched on back navigation, selecting `[hx-history-elt]` out of the response — the adapters answer such a request with the full page, on either version; the `hx-history-cache` extension restores caching |
| **Typed Go API** | `ghtmx.SwapInnerHTML` … `SwapFocusScroll` | plus `SwapInnerMorph`, `SwapOuterMorph`, `SwapOuterSync`, the aliases, `SwapShowTarget`, `SwapScrollTarget`, `SwapTarget`, `SwapStrip`, `SwapEmpty`, `SwapFocusScrollV4` |

The rows the compiler cannot see — headers, form data, timeout,
history — are the ones to check in handlers and page scripts when
moving a project; the rest is reported at build time.

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

4. Check the handler-side rows of the table above: read `HX-Source`
   instead of `HX-Trigger`, add `hx-include="closest form"` where a
   `hx-delete` relied on form values, and decide whether 4xx/5xx
   responses should swap (`hx-status:4xx="swap:none"` restores the htmx 2
   behaviour).

5. Look at what the compiler cannot see: page scripts listening on the
   htmx 2 event names (`htmx:afterSwap` → `htmx:after:swap`), and
   history. Moving the listener into an `hx-on::after:swap` attribute
   puts the name under the compiler's check. A history restore is a
   full-page request now; the adapters' automatic mode already answers
   it with the page, so a `hx-history-elt` region keeps working.

The three `htmx4-*` examples in the repository show the new surface in
working applications: explicit inheritance
([`htmx4-inheritance`](/examples/htmx4-inheritance)), status routing and
partials ([`htmx4-status`](/examples/htmx4-status)), and the `QUERY` verb
with morph swaps ([`htmx4-query`](/examples/htmx4-query)).

Pinning back to `2.0.10` reverses the checks: every htmx 4 construct
reports `GHTMX-E0501` as introduced in 4.0.0, so a template cannot mix
generations unnoticed.

## Self-hosting the script

`ghtmxgen.HTMXScript()` emits the jsDelivr tag for the pinned version
with its subresource-integrity hash. To serve the file yourself, pass
`ghtmx.WithScriptSrc("/static/htmx.min.js")`; the hash stays, so the
vendored file must be the exact published build — `ghtmx.HTMXScriptIntegrity`
returns the pin for a test to assert against.
