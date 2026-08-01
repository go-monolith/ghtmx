# templ Conformance Corpus

ghtmx guarantees that the `.ghtmx` language surface is a **syntactic
superset** of templ at the pinned baseline (FR-004), subject to two
documented semantic carve-outs. This document defines the corpus that makes
the guarantee testable, and itemizes every exclusion.

Baseline: templ commit `04abee5` (≈ v0.3.1020+36) — see
`TEMPL_SYNTAX_BASELINE.md`.

## The corpus

The conformance corpus is the baseline's own test suite, ported with the
mechanical rename and kept green. It is not a curated subset: every parser,
formatter, and rendering test the baseline ships runs here.

| Corpus part | Location | What it asserts |
| --- | --- | --- |
| Parser table tests | `internal/parser/*_test.go` | Exact ASTs including literal source ranges, for every construct: elements, all six attribute forms, expression keys, control flow, components, children, css/script templates, comments, doctype |
| Script parser txtar | `internal/parser/scriptparsertestdata/` | JS-aware `<script>` lexing: quoting, escapes, regex literals |
| Go expression fuzz+tests | `internal/parser/goexpression/` | Go-island extraction |
| Formatter golden corpus | `internal/format/testdata/*.txt` | Canonical formatting and idempotence (second-pass diff) |
| Generator golden dirs | `internal/generator/test-*/` (50 dirs) | Committed generated Go plus expected rendered HTML per construct |
| Render conformance | `render_test.go` in each golden dir | Byte/structural equality of rendered output |
| Determinism | ensure-generated CI job | Regenerating the whole corpus is a no-op |

Coverage relative to the baseline (audited):

- Generator golden dirs: identical set except `test-fragment` (exclusion 3).
- Parser test files: identical set, zero exclusions.
- Formatter corpus: identical set except one renamed case (exclusion 4).

## CI gate

The corpus runs on every build: the `test` job executes the full suite on
Linux/macOS/Windows, and the `ensure-generated` job regenerates every
committed golden and fails on drift (`.github/workflows/ci.yml`). Golden
updates are deliberate: `GHTMX_UPDATE_GOLDEN=1 go test <package>`.

## Exclusion list

Every corpus case or behavior excluded from "parses and renders
equivalently", with its reason and replacement construct.

### Carve-out 1 — `hx-*` verb attributes are typed bindings

The five verb attributes (`hx-get`, `hx-post`, `hx-put`, `hx-patch`,
`hx-delete`) accept a Go handler symbol or a generated route constructor,
never a string URL or arbitrary expression. Replacement constructs:
`hx-post={ handlers.CreateUser }` (FR-020) and
`hx-get={ ghtmxgen.GetUser(id) }` (FR-021). Diagnostics: `GHTMX-E0601`
(arbitrary expression), `GHTMX-E0602` (string URL).

| # | Excluded case | Detail |
| --- | --- | --- |
| 1.1 | `internal/generator/test-attribute-escaping/template.ghtmx` (baseline: `hx-post="/click"`) | **Resolved.** The carve-out reporter is live: a string-URL verb attribute is `GHTMX-E0602`. The corpus case now uses `data-post="/click"` (preserving the attribute-escaping intent), and the original string form is a negative-case test: `TestCarveOut1StringURL` in `internal/analyzer/bindings_test.go` asserts the exact baseline string produces E0602 naming the carve-out and both replacement constructs. |
| 1.2 | `internal/generator/test-element-attributes/template.ghtmx` (baseline: `hx-post="/api/secret/unlock"`) | **Resolved** the same way: corpus uses `data-post`, the baseline string is covered by `TestCarveOut1StringURL`. |

Arbitrary expressions on verb attributes are `GHTMX-E0601`
(`TestCarveOut1ArbitraryExpression`).

Not excluded (these remain valid templ-inherited syntax): non-verb `hx-*`
attributes with constant values (`hx-trigger="click"`, `hx-vals='{"..."}'`,
`hx-target="#secret"`, `hx-indicator="..."` — validated against the pinned
htmx surface, FR-024) and `hx-on::*` script attributes, which keep the
baseline's script-attribute semantics.

Note on 1.2's neighbour `hx-target-*="#errors"` (an attribute of the
htmx `response-targets` extension): it parses per the superset guarantee
and stays in the corpus. How extension attributes interact with FR-024
unknown-name validation is decided when the validator is wired (spec task
30); the corpus case is the fixture for that decision.

### Carve-out 2 — URL-context escaping at binding sites is engine-determined

At a route-binding site the escaping context derives from the attribute's
declared type and is not author-selectable; parameters are percent-encoded
by the generated constructors (S1.1, FR-023). Replacement construct: the
`ghtmx.SafeURL`-returning generated route constructors.

| # | Excluded case | Detail |
| --- | --- | --- |
| 2.1 | *(no corpus case)* | The baseline corpus contains no author-selected safe-URL usage at an `hx-*` site, because in templ `hx-*` values are ordinary attributes. The carve-out is enforced twice over: verb attributes accept only typed bindings (carve-out 1), and an explicit `ghtmx.URL`/`ghtmx.SafeURL`/`templ.URL` call at a binding site is `GHTMX-E0603` (`TestCarveOut2AuthorEscaping`). `templ.URL`/`ghtmx.URL` and `SafeURL` for `href`/`action`/`data` attributes are inherited unchanged (`internal/generator/test-a-href`, `test-form-action` remain in the corpus). |

### Non-carve-out exclusions

Deviations that are not part of the FR-004 syntactic-superset contract but
are documented here for completeness (see also `CHANGELOG.md`):

| # | Excluded case | Reason and replacement |
| --- | --- | --- |
| 3 | `generator/test-fragment/` (baseline dir, not ported) | Tests the baseline's *runtime* fragment API (`templ.Fragment`, `RenderFragments`, `WithFragments`), which ghtmx removes. The `@templ.Fragment("id") { ... }` *syntax* is an ordinary templ-element call and still parses; only the runtime symbols are gone. Replacement: compile-time `fragment` declarations with generated inline + standalone entry points (FR-030/FR-031, spec tasks 37–41). |
| 4 | `internal/format/testdata/attributes_style_values_are_passed_through.txt` (renamed from `..._are_formatted_by_prettier.txt`) plus 11 regenerated formatter expectations | The baseline shells out to Prettier to reformat `<script>`/`<style>` bodies and constant style attributes; ghtmx has no Node.js dependency anywhere (constitution). The formatter passes those bodies through verbatim. Formatting behavior, not syntax: the inputs still parse and render equivalently. |
| 5 | Rendering comparison normalization | The baseline's `htmldiff` normalized expected/actual HTML through Prettier; ghtmx normalizes structurally via `x/net/html` (`internal/htmldiff`). Ten `expected.html` goldens were regenerated where the baseline's copies encoded Prettier's whitespace/quote restyling inside script/style content. Audited: zero structural HTML differences. |

## Keeping this honest

Any future corpus divergence must be added to the exclusion list above in
the same change, with its carve-out (or reason) and replacement construct.
The `ensure-generated` job prevents silent golden drift; deliberate drift
without a matching entry here is a review defect.
