# TEMPL_SYNTAX_BASELINE

ghtmx is a hard fork of [templ](https://github.com/a-h/templ). Per FR-004, the
`.ghtmx` language surface is a syntactic superset of the templ syntax at the
pinned baseline below, with two documented semantic carve-outs.

| Field | Value |
| --- | --- |
| Upstream repository | `https://github.com/a-h/templ` |
| Pinned commit | `04abee5364c6fab2bde8c00d215fdcb630ad6a94` |
| Nearest tag | `v0.3.1020` (baseline is 36 commits after this tag) |
| Fork date | 2026-07-31 |

## Carve-outs (FR-004)

Constructs valid at the baseline that ghtmx deliberately reinterprets:

1. **`hx-*` attributes are typed bindings.** The five verb attributes
   (`hx-get`, `hx-post`, `hx-put`, `hx-patch`, `hx-delete`) accept a Go
   handler symbol or a generated route constructor call, not an arbitrary
   string or expression. Arbitrary values produce `GHTMX-E0601`/`GHTMX-E0602`
   diagnostics naming the replacement construct. Other `hx-*` attributes are
   validated against the pinned htmx version's attribute surface.
2. **URL-context escaping at route-binding sites is engine-determined.**
   The escaping context is derived from the attribute's declared type and is
   not author-selectable at the binding site.

The conformance corpus (ported from the baseline's parser, formatter, and
generator test suites) is the executable form of this guarantee: every
non-excluded case must parse and render equivalently. The corpus inventory
and the itemized exclusion list live in `CONFORMANCE.md`.
