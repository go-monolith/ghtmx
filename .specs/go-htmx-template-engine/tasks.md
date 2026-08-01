# Implementation Tasks — Go HTMX (`.ghtmx`)

## Plan Overview

**Scope:** Full MVP — every deliverable through the success criterion (a reference CRUD application with htmx partial updates building and running with zero hand-written glue code).

**Sequencing strategy:** Vertical slice first. Milestone 2 builds an end-to-end walking skeleton — one trivial template travelling the full `parse → analyze → emit → render` path with a working `ghtmx generate` — before any subsystem is built out. This front-loads integration risk: the phase boundaries mandated by constitution A2, the source-map contract (D7), and the D11 validation split are all exercised on day one, when changing them is cheap. Subsequent milestones widen each phase against a pipeline that already works.

**Executor:** Solo maintainer. Tasks are ordered so they can be executed sequentially; dependencies are recorded to make the critical path visible rather than to enable parallelism.

**Complexity calibration.** Sized for a solo maintainer at the requested 2–5 day granularity:

| Label | Effort |
| --- | --- |
| Small | ~1–2 days |
| Medium | ~2–3 days |
| Large | ~4–5 days |

Anything that would exceed 5 days has been split.

**Task annotations.** Every task records its acceptance criteria, the requirements it satisfies, and the solution modules it touches — so no requirement is orphaned and no task is written without a spec anchor.

## Milestone 1 — Foundations and Repository Infrastructure

- [x] 1\. Repository, module, and package skeleton
  - Create the single Go module with the layout fixed by constitution A1 and solution D10: root package `ghtmx` (runtime), `cmd/ghtmx/`, `adapters/{nethttp,chi,echo,gin,fiber}/`, and all compiler packages under `internal/`.
  - Add MIT `LICENSE` retaining upstream `templ` copyright notices, `README`, and `CHANGELOG`.
  - Acceptance Criteria:
    - `go build ./...` succeeds on a clean checkout.
    - Every compiler package resides under `internal/` and is therefore unimportable from outside the module.
    - The root `ghtmx` package imports only the standard library.
  - _Dependencies: none_
  - _Requirements: NFR-011, NFR-012_
  - _Modules: M6, all_
  - _Complexity: Small_

- [x] 2\. CI pipeline baseline
  - GitHub Actions workflow running build, test, `gofmt -l`, and `go vet` on Linux, macOS, and Windows across the supported Go version range.
  - Establish the job structure that later quality gates (benchmarks, WASM matrix, `govulncheck`) will extend.
  - Acceptance Criteria:
    - All three platforms build and test green on the skeleton.
    - An unformatted file or a `go vet` finding fails the build.
    - The supported Go version range is declared in one place and consumed by the matrix.
  - _Dependencies: 1_
  - _Requirements: NFR-005, NFR-010_
  - _Modules: —_
  - _Complexity: Small_

- [x] 3\. Diagnostic model and reporter
  - Implement `Diagnostic{ID, Severity, Pos, Message, Suggest}` with the stable identifier scheme (`GHTMX-E01xx`…`GHTMX-W03xx`) and an accumulating `DiagnosticSink`.
  - Implement the CLI reporter with human-readable and JSON output modes.
  - Acceptance Criteria:
    - Every diagnostic carries a `file:line:column` position and a stable ID; a diagnostic constructed without a position fails a test assertion.
    - Severity is data, not control flow: the same check can be emitted as warning or error from configuration.
    - JSON output is stable and machine-parseable.
  - _Dependencies: 1_
  - _Requirements: FR-045, NFR-009_
  - _Modules: M3, M8_
  - _Complexity: Medium_

- [x] 4\. Source position primitives and SourceMap
  - Implement `Position{Index, Line, Col}`, `Range{From, To}`, and the bidirectional `SourceMap` with `SourceLinesToTarget` / `TargetLinesToSource` indices plus symbol-range entries.
  - Acceptance Criteria:
    - `TargetPositionFromSource` and `SourcePositionFromTarget` round-trip correctly on a fixture mapping.
    - A position with no mapping returns `ok=false` rather than a wrong position.
    - Symbol ranges are queryable independently of line/column spans.
  - _Dependencies: 1_
  - _Requirements: NFR-009_
  - _Modules: M1_
  - _Complexity: Medium_

- [x] 5\. Configuration loading and precedence
  - Implement the root config file covering pinned htmx version, source directories, route-discovery scope, output package, generated-file naming, and per-check severities; implement flag > file > default precedence.
  - Acceptance Criteria:
    - A conventional project with no config file loads successfully on defaults.
    - Every setting has a corresponding CLI flag, and the flag wins over the file.
    - An invalid key or value produces a positioned diagnostic naming the offending key.
  - _Dependencies: 3_
  - _Requirements: FR-070, FR-071, FR-072, FR-073_
  - _Modules: M8_
  - _Complexity: Medium_

## Milestone 2 — Walking Skeleton (End-to-End Vertical Slice)

- [x] 6\. Minimal lexer and parser for a trivial template
  - Parser-combinator lexer and parser handling exactly one shape: a component declaration containing static markup and a single Go expression interpolation. Positions recorded on every node.
  - Acceptance Criteria:
    - A trivial `.ghtmx` file parses into an AST whose node ranges map back to the correct source offsets.
    - Malformed input yields a positioned diagnostic and never panics.
    - Lexer and parser are separately testable with no cross-phase reach (constitution A2).
  - _Dependencies: 3, 4_
  - _Requirements: FR-001, FR-002, NFR-006_
  - _Modules: M1_
  - _Complexity: Medium_

- [x] 7\. Minimal runtime: Component, WriteString, buffer pool
  - Implement the `Component` interface, `ComponentFunc`, `WriteString`, the `sync.Pool` buffer pool with `GetBuffer`/`ReleaseBuffer` (bypassed when the writer is already buffered), `EscapeString`, and the `Error` type carrying `FileName`/`Line`/`Col`.
  - Acceptance Criteria:
    - A hand-written component renders to an `http.ResponseWriter` with no adapter imported.
    - Render errors are returned, never panicked.
    - The package's transitive import set is standard-library only.
  - _Dependencies: 1_
  - _Requirements: FR-090, NFR-012_
  - _Modules: M6_
  - _Complexity: Medium_

- [x] 8\. RangeWriter and minimal component emitter
  - Implement the `RangeWriter` literal-accumulation strategy flushing to `ghtmx.WriteString`, and the component emitter producing an exported function returning `ghtmx.Component`. Record target ranges into the source map. Implement the D11 generate-time self-validation (`go/parser` parse + `go/format`) and atomic write.
  - Acceptance Criteria:
    - Consecutive static markup collapses into a single `WriteString` call.
    - Emitted output is `gofmt`-clean and parses with `go/parser`; a failure writes nothing and reports an internal-defect diagnostic.
    - The emitted source map resolves generated positions back to `.ghtmx` source.
  - _Dependencies: 4, 6, 7_
  - _Requirements: FR-001, NFR-005, NFR-009_
  - _Modules: M1, M4_
  - _Complexity: Large_

- [x] 9\. `ghtmx generate` end-to-end wiring
  - Wire the CLI command through config resolution, parsing, a pass-through analyzer stub, and emission. Implement exit-code semantics: non-zero on any error-level diagnostic, zero when only warnings are present.
  - Acceptance Criteria:
    - `ghtmx generate` on a clean checkout with zero generated files produces valid Go in a single pass.
    - An error-level diagnostic yields a non-zero exit code; warnings alone do not.
    - Generated output compiles with standard `go build`.
  - _Dependencies: 5, 8_
  - _Requirements: FR-010, FR-060, NFR-011_
  - _Modules: M4, M5, M8_
  - _Complexity: Medium_

- [x] 10\. Golden-file harness and determinism check
  - Build the paired-input/expected-output golden-file test harness, plus a determinism check that regenerates the corpus twice and diffs. Salt-free ordering: assert no map iteration reaches output.
  - Acceptance Criteria:
    - Golden drift fails CI with a readable diff.
    - Two consecutive generations of the same input produce byte-identical output.
    - Output is identical across the three CI platforms.
  - _Dependencies: 2, 9_
  - _Requirements: NFR-004, NFR-005_
  - _Modules: M4_
  - _Complexity: Medium_

- [x] 11\. Walking-skeleton demo application
  - A minimal `net/http` application rendering a generated component, proving the full path from `.ghtmx` source to bytes in a browser.
  - Acceptance Criteria:
    - `ghtmx generate && go run ./cmd` serves the rendered template.
    - The application imports only the runtime — no adapter, no compiler package.
    - The demo is committed as the seed of the fixture-application corpus.
  - _Dependencies: 9_
  - _Requirements: FR-090, NFR-011_
  - _Modules: M6_
  - _Complexity: Small_

## Milestone 3 — Core Template Language

- [x] 12\. Elements, attributes, and dynamic attribute forms
  - Parse and emit elements, text, static attributes, conditional attributes, boolean attributes, and attribute spreading, matching the pinned `TEMPL_SYNTAX_BASELINE` semantics.
  - Acceptance Criteria:
    - Each attribute form renders equivalently to the baseline `templ` behaviour on the conformance fixtures.
    - Void elements and attribute quoting are handled correctly.
    - Golden files cover every attribute form.
  - _Dependencies: 8_
  - _Requirements: FR-004_
  - _Modules: M1, M4_
  - _Complexity: Large_

- [x] 13\. Control flow constructs
  - Parse and emit `if`/`else`, `for`, and `switch` over Go expressions, compiling to equivalent Go control flow.
  - Acceptance Criteria:
    - Variables bound in a construct are in scope for nested template content and type-check under `go build`.
    - Nested and sibling constructs emit correctly.
    - Golden files cover each construct including `else if` chains.
  - _Dependencies: 12_
  - _Requirements: FR-003_
  - _Modules: M1, M4_
  - _Complexity: Medium_

- [x] 14\. Typed component declarations and cross-package imports
  - Full component declaration with typed Go parameters; component invocation resolving across packages under normal Go import semantics and visibility rules.
  - Acceptance Criteria:
    - A component in package `a` is callable from a template in package `b`.
    - Wrong argument type or arity is a compile error at the call site.
    - An unresolvable component reference produces a positioned diagnostic naming the missing symbol.
  - _Dependencies: 13_
  - _Requirements: FR-001, FR-005_
  - _Modules: M1, M3, M4_
  - _Complexity: Large_

- [x] 15\. Children and slot composition
  - Support components accepting nested content, matching the inherited baseline semantics.
  - Acceptance Criteria:
    - A component receives and renders nested content passed by its caller.
    - Nested composition to arbitrary depth renders correctly.
    - Golden files cover the construct.
  - _Dependencies: 14_
  - _Requirements: FR-004_
  - _Modules: M1, M4_
  - _Complexity: Medium_

- [x] 16\. Scoped `css` and `script` blocks
  - Parse and emit scoped `css` and `script` blocks with generated identifiers and once-per-render deduplication.
  - Acceptance Criteria:
    - Generated identifiers are deterministic across runs.
    - A block rendered by multiple components on one page is emitted once.
    - Golden files cover both block types.
  - _Dependencies: 15_
  - _Requirements: FR-004, NFR-004_
  - _Modules: M1, M4, M6_
  - _Complexity: Large_

- [x] 17\. Context-aware escaping
  - Implement the `EscapeResolver` selecting the escaper at compile time from the output context, and the runtime escapers for HTML text, attribute, URL path segment, URL query value, JS, and CSS contexts. Implement the explicit, visibly-named unsafe construct.
  - Acceptance Criteria:
    - Each context escapes correctly against the conformance fixtures; a context-confusion case is covered per context.
    - The runtime calls a specific escaper directly with no runtime context dispatch.
    - No global switch exists that disables escaping.
  - _Dependencies: 12_
  - _Requirements: FR-002, NFR-007_
  - _Modules: M4, M6_
  - _Complexity: Large_

- [x] 18\. `templ` conformance corpus and carve-out exclusion list
  - Assemble the conformance corpus derived from the pinned `TEMPL_SYNTAX_BASELINE`, and document the two FR-004 carve-out exclusions (`hx-*` as typed bindings; engine-determined URL escaping) with the `.ghtmx` construct replacing each.
  - Acceptance Criteria:
    - Every non-excluded corpus case parses and renders equivalently.
    - Every exclusion is individually listed with its carve-out and replacement construct.
    - The corpus runs as a CI gate.
  - _Dependencies: 16, 17_
  - _Requirements: FR-004, NFR-013_
  - _Modules: M1, M3_
  - _Complexity: Medium_

- [x] 19\. Canonical formatter (`ghtmx fmt`)
  - AST-to-text formatter round-tripping through the shared AST, with a `--check` mode.
  - Acceptance Criteria:
    - Formatting an already-formatted file produces no change (idempotence), asserted across the whole corpus.
    - `--check` reports unformatted files with a non-zero exit code and writes nothing.
    - Formatting preserves semantics: the corpus renders identically before and after.
  - _Dependencies: 16_
  - _Requirements: FR-062_
  - _Modules: M1, M8_
  - _Complexity: Large_

- [x] 20\. Parser fuzzing harness
  - Continuous fuzz targets for the lexer and parser with an in-repo corpus; every crash becomes a permanent regression case.
  - Acceptance Criteria:
    - Fuzz targets run in CI on a bounded time budget and on demand for longer campaigns.
    - No input produces a panic or non-termination.
    - Discovered crashes are checked in as seed corpus entries.
  - _Dependencies: 16_
  - _Requirements: NFR-006_
  - _Modules: M1_
  - _Complexity: Medium_

## Milestone 4 — Route Discovery

- [x] 21\. Syntax-only package loader
  - `go/packages` loader configured to `NeedName | NeedFiles | NeedSyntax`, never requesting type information, per constitution A3.1 and solution D2.
  - Acceptance Criteria:
    - A module containing a deliberate type error still loads and yields ASTs for all packages.
    - A package that fails to parse produces a diagnostic and is skipped; the remainder still loads.
    - No code path requests type-checking of user packages.
  - _Dependencies: 3_
  - _Requirements: FR-010, FR-055_
  - _Modules: M2_
  - _Complexity: Medium_

- [x] 22\. Recognizer interface and `net/http` ServeMux recognizer
  - Define the `Recognizer` interface (`Detect`, `ParamSyntax`) and implement the standard-library recognizer for Go 1.22+ method-and-path patterns, including `HandleFunc` and `Handle`.
  - Acceptance Criteria:
    - `mux.HandleFunc("GET /users/{id}", handlers.GetUser)` yields verb, path, parameter `id`, and handler symbol.
    - Wildcard (`{path...}`) and trailing-slash patterns are recorded with their semantics intact.
    - Handler symbols resolve by package-qualified identifier matching against import declarations.
  - _Dependencies: 21_
  - _Requirements: FR-010, FR-011_
  - _Modules: M2_
  - _Complexity: Large_

- [x] 23\. chi, echo, gin, and fiber recognizers
  - One `Recognizer` implementation per router, normalizing each flavour's path parameter syntax (`{id}` vs `:id`, plus wildcard forms) into the common route table representation while preserving the original form.
  - Acceptance Criteria:
    - All five HTTP verbs are recognized for each router against fixture files.
    - Router flavour is determined per registration site, not assumed globally.
    - The normalized path round-trips to the original form for the `routes` command.
  - _Dependencies: 22_
  - _Requirements: FR-012_
  - _Modules: M2_
  - _Complexity: Large_

- [x] 24\. Group and nested prefix composition
  - `PrefixResolver` walking `Route`/`Group` nesting and accumulating literal prefixes to a fully-composed path.
  - Acceptance Criteria:
    - A route inside a group resolves to the fully-composed path.
    - Arbitrarily nested groups compose correctly.
    - A group whose prefix is not statically determinable is reported rather than silently dropped.
  - _Dependencies: 23_
  - _Requirements: FR-013_
  - _Modules: M2_
  - _Complexity: Medium_

- [x] 25\. Middleware-wrapped registration handling
  - `MiddlewareUnwrapper` seeing through `With(...)`-style chains and group-level middleware to the underlying registration.
  - Acceptance Criteria:
    - `r.With(mw).Get("/users", h)` is discovered identically to the unwrapped form.
    - Group-level middleware does not affect the discovered path or handler.
    - Handlers wrapped in a middleware call expression still resolve when the symbol is syntactically present.
  - _Dependencies: 24_
  - _Requirements: FR-014_
  - _Modules: M2_
  - _Complexity: Medium_

- [x] 26\. Escape-hatch route declarations
  - Implement the explicit annotation form allowing an author to declare a route that syntax-only discovery cannot resolve, and merge declarations into the route table with `RouteOrigin=Declared`.
  - Acceptance Criteria:
    - A declared route is bindable exactly like a discovered route.
    - The annotation form is documented and covered by the syntax specification.
    - Declared and discovered routes are distinguishable in the route table.
  - _Dependencies: 25_
  - _Requirements: FR-015_
  - _Modules: M2_
  - _Complexity: Medium_

- [x] 27\. Route conflict and unresolvable registration diagnostics
  - `ConflictDetector` for verb+path uniqueness across discovered and declared routes, plus diagnostics for statically unresolvable registrations directing authors to the escape hatch.
  - Acceptance Criteria:
    - A duplicate verb+path is an error naming both registration sites.
    - A computed path or dynamic registration produces an error naming the site and pointing at the FR-015 annotation.
    - Unsupported forms (anonymous handlers, loop-generated routes) are reported rather than silently ignored.
  - _Dependencies: 26_
  - _Requirements: FR-050, FR-051_
  - _Modules: M2_
  - _Complexity: Medium_

- [x] 28\. `ghtmx routes` command
  - Print the route table with verb, path, handler package and symbol, recognizer, origin, and source position; support a machine-readable JSON mode.
  - Acceptance Criteria:
    - Every discovered and declared route appears with all recorded fields.
    - Escape-hatch declarations are marked distinctly.
    - JSON output is stable and parseable.
  - _Dependencies: 27_
  - _Requirements: FR-064_
  - _Modules: M2, M8_
  - _Complexity: Small_

## Milestone 5 — Route-Aware Bindings

- [x] 29\. Embedded htmx attribute surface set
  - Build the `go:embed`ded, version-keyed surface set spanning the supported htmx range, with per-attribute permitted values, invalid combinations, and introduced-in / removed-in metadata. Implement `SurfaceSet.ForVersion` and the `Surface` API.
  - Acceptance Criteria:
    - The configured htmx version selects the matching surface; a version outside the supported range is a `GHTMX-E05xx` error naming the range.
    - Introduced-in and removed-in metadata is queryable per attribute.
    - `generate` performs no network I/O.
  - _Dependencies: 5_
  - _Requirements: FR-071, NFR-004_
  - _Modules: M3_
  - _Complexity: Large_

- [x] 30\. `hx-*` attribute validator
  - Validate attribute names, constrained values, and contradictory combinations against the selected surface, with did-you-mean suggestions drawn from the same table.
  - Acceptance Criteria:
    - An unknown `hx-*` name is an error listing the closest valid names.
    - An invalid value for a constrained attribute is an error listing the valid values.
    - A contradictory combination is an error explaining the conflict.
  - _Dependencies: 29_
  - _Requirements: FR-024, FR-041, FR-044_
  - _Modules: M3_
  - _Complexity: Medium_

- [x] 31\. Direct handler symbol bindings
  - `BindingResolver` resolving `hx-post={ handlers.CreateUser }` against the route table for non-parameterised routes, across all five verbs, with verb agreement enforcement.
  - Acceptance Criteria:
    - A binding to a registered handler emits that handler's path as the attribute value.
    - A symbol that is not a registered handler is an error naming the symbol.
    - A verb mismatch is an error stating both expected and actual verb.
  - _Dependencies: 27, 30_
  - _Requirements: FR-020, FR-022, FR-040_
  - _Modules: M3_
  - _Complexity: Large_

- [x] 32\. Typed route constructors and the central generated package
  - Emit the central generated package containing path constants for non-parameterised routes and typed constructor functions for parameterised routes, with the parameter list matching path parameters in order. Path parameter types default to `string` unless annotated.
  - Acceptance Criteria:
    - `hx-get={ routes.GetUser(user.ID) }` compiles and produces a correctly-substituted URL.
    - Wrong constructor arity or argument type is a Go compile error at the call site.
    - Renaming or re-pathing a route regenerates constructors so every call site breaks at compile time.
  - _Dependencies: 31_
  - _Requirements: FR-021, FR-040_
  - _Modules: M3, M4_
  - _Complexity: Large_

- [x] 33\. URL escaping of interpolated route parameters
  - Percent-encode parameters substituted into generated `hx-*` URLs according to position (path segment vs. query value), composed with attribute-value escaping, with the context determined by the engine and not overridable at the binding site.
  - Acceptance Criteria:
    - Parameters containing `/`, `?`, `#`, `&`, or a space are encoded correctly for their position.
    - URL escaping is applied before, and composes with, HTML attribute-value escaping.
    - The route-binding URL path is covered as a mandatory case in the escaping conformance suite.
  - _Dependencies: 17, 32_
  - _Requirements: FR-023, NFR-007_
  - _Modules: M4, M6_
  - _Complexity: Medium_

- [x] 34\. Carve-out reporter
  - Detect and report the two FR-004 semantic carve-outs with dedicated `GHTMX-E06xx` diagnostics: an `hx-*` attribute given an arbitrary string where a typed binding is required, and author-selected URL escaping at a binding site.
  - Acceptance Criteria:
    - Each carve-out produces a specific diagnostic naming the carve-out and its `.ghtmx` replacement construct, not a generic parse or type error.
    - Every corpus exclusion from task 18 triggers its corresponding diagnostic.
  - _Dependencies: 18, 33_
  - _Requirements: FR-004_
  - _Modules: M3_
  - _Complexity: Medium_

- [x] 35\. htmx version mismatch diagnostics
  - Report use of a construct unsupported by the configured htmx version, and validate the served htmx version in the script-inclusion path.
  - Acceptance Criteria:
    - The error names the construct, the configured version, and the version that introduced or removed it.
    - A construct valid for the configured version produces no diagnostic.
  - _Dependencies: 30_
  - _Requirements: FR-052_
  - _Modules: M3, M6_
  - _Complexity: Small_

- [x] 36\. Swap target and reachability warnings
  - `TargetChecker` performing literal-to-literal ID matching only, emitting a warning by default with an opt-in strict mode (`--strict-targets`); plus the unreachable-route warning.
  - Acceptance Criteria:
    - A selector or emitted ID containing any interpolated expression is exempt from analysis and produces no diagnostic.
    - A target whose ID is emitted anywhere in the compiled set produces no diagnostic.
    - The default severity is warning and does not affect the exit code; strict mode promotes it to an error.
  - _Dependencies: 31_
  - _Requirements: FR-042, FR-043_
  - _Modules: M3_
  - _Complexity: Medium_

## Milestone 6 — Fragments

- [x] 37\. `fragment` declaration and `FragmentRef` parsing
  - Parse `fragment` declarations with typed parameters inside page templates, and the `FragmentRef` node referencing a fragment declared elsewhere, carrying an optional package qualifier and argument expressions.
  - Acceptance Criteria:
    - A fragment declared within a page participates in that page's output at its declaration site.
    - A duplicate fragment name within scope is a compile error.
    - A `FragmentRef` parses with its qualifier and arguments and carries correct source ranges.
  - _Dependencies: 14_
  - _Requirements: FR-030, FR-032_
  - _Modules: M1_
  - _Complexity: Medium_

- [x] 38\. Fragment reference resolution
  - `FragmentRefResolver` resolving each reference to its declaring fragment, checking argument arity against the declared parameter list and enforcing Go visibility rules for cross-package references.
  - Acceptance Criteria:
    - A fragment referenced by two different pages resolves to the same declaration.
    - A cross-package reference to an unexported fragment is an error.
    - An unresolvable reference is a `GHTMX-E03xx` error naming the fragment.
  - _Dependencies: 37_
  - _Requirements: FR-032_
  - _Modules: M3_
  - _Complexity: Medium_

- [x] 39\. Fragment code generation: shared body and two wrappers
  - Emit one unexported shared body function per fragment plus the inline wrapper (returning `ghtmx.Component`) and the standalone wrapper (returning `ghtmx.Fragment`), both taking the declared parameter list. Every `FragmentRef` emits a call to the same shared body.
  - Acceptance Criteria:
    - A fragment referenced by five pages emits exactly one body function.
    - The inline path and the standalone path produce byte-identical fragment markup for identical inputs.
    - The standalone entry point is independent of which page references the fragment.
  - _Dependencies: 38_
  - _Requirements: FR-031, FR-032_
  - _Modules: M4_
  - _Complexity: Large_

- [x] 40\. Runtime `Fragment` interface and handler-explicit rendering
  - Add the `Fragment` interface to the runtime and support rendering the standalone entry point directly against an `io.Writer` / `http.ResponseWriter` with no adapter imported.
  - Acceptance Criteria:
    - A handler renders a fragment standalone with no adapter present.
    - On the explicit path the engine writes no HTTP status code and no unrequested response headers.
    - The semantics of each interface method on a standalone wrapper are documented.
  - _Dependencies: 39_
  - _Requirements: FR-034, FR-090_
  - _Modules: M6_
  - _Complexity: Medium_

- [x] 41\. Fragment reachability and cycle detection
  - Warn on fragments never rendered or bound; detect direct and indirect cycles across component and fragment reference edges.
  - Acceptance Criteria:
    - An unused fragment produces a warning naming its declaration site and does not fail the build.
    - A reference cycle produces an error listing the full chain.
    - Cycle detection terminates deterministically without stack exhaustion.
  - _Dependencies: 39_
  - _Requirements: FR-033, FR-053_
  - _Modules: M3_
  - _Complexity: Medium_

## Milestone 7 — Server-Driven Event Contract

- [x] 42\. `event` declaration parsing and registry
  - Parse `event` declarations with typed payloads (payload-less declarations valid), build the `EventRegistry`, and enforce global name uniqueness across the compiled set.
  - Acceptance Criteria:
    - A duplicate event name across the compiled set is a compile error naming both declarations.
    - A template-side reference to an undeclared event name is a `.ghtmx` compile error naming the event.
    - A declared-but-unreferenced event produces a warning.
  - _Dependencies: 14_
  - _Requirements: FR-037_
  - _Modules: M1, M3_
  - _Complexity: Medium_

- [x] 43\. Event emission code generation
  - Emit one exported emission symbol and payload type per declared event into the central generated package, writing a correctly-serialized `HX-Trigger` header, with multiple emissions merging into a single header.
  - Acceptance Criteria:
    - Emitting an undeclared event fails to compile at the handler call site as an undefined identifier.
    - A payload type mismatch is a Go compile error at the call site.
    - Two events emitted in one response merge into one correctly-serialized `HX-Trigger` header matching the pinned htmx version's format.
  - _Dependencies: 32, 42_
  - _Requirements: FR-036, FR-037_
  - _Modules: M4, M6_
  - _Complexity: Large_

- [x] 44\. Typed htmx response headers and CSRF helper
  - Typed helpers for `HX-Retarget`, `HX-Reswap`, and `HX-Redirect`; CSRF token attachment via `hx-headers` for the four state-changing verbs.
  - Acceptance Criteria:
    - Each header is settable through a typed helper rather than a raw string.
    - A CSRF-safe state-changing binding requires no hand-written header plumbing.
    - The token source is supplied by the application; the engine neither generates nor validates tokens.
  - _Dependencies: 43_
  - _Requirements: FR-036, FR-092_
  - _Modules: M6_
  - _Complexity: Medium_

- [x] 45\. htmx script inclusion helper
  - Runtime helper emitting the htmx script tag for the configured version, validating that the served version matches.
  - Acceptance Criteria:
    - The emitted tag references the configured htmx version.
    - A mismatch between configured version and served asset is reported per FR-052.
  - _Dependencies: 35_
  - _Requirements: FR-091_
  - _Modules: M6_
  - _Complexity: Small_

## Milestone 8 — Build Performance and Developer Loop

- [x] 46\. On-disk content-hash build cache
  - `HashStore` keyed by SHA-256 of content, salted with toolchain version and config hash; corrupt entries discarded and rebuilt.
  - Acceptance Criteria:
    - An unchanged unit is served from cache across process restarts.
    - A toolchain or config change invalidates the whole cache.
    - A corrupt entry is discarded silently and the unit rebuilt; a cache miss is never an error.
  - _Dependencies: 9_
  - _Requirements: NFR-001_
  - _Modules: M5_
  - _Complexity: Large_

- [x] 47\. Dependency graph and two-tier invalidation
  - Track template-to-template (including fragment references), template-to-route, and template-to-event dependencies; implement two-tier invalidation across Go source changes and `.ghtmx` changes.
  - Acceptance Criteria:
    - Editing a fragment invalidates every page referencing it.
    - Editing a Go route registration invalidates the route table and every unit with a binding.
    - Editing an unrelated template invalidates only that unit.
  - _Dependencies: 39, 46_
  - _Requirements: FR-061_
  - _Modules: M5_
  - _Complexity: Large_

- [x] 48\. `ghtmx watch`
  - Watch `.ghtmx` files and Go source within discovery scope via `fsnotify`, regenerating incrementally and reporting diagnostics per change without terminating.
  - Acceptance Criteria:
    - A change to a Go handler's route registration regenerates affected constructors and dependent templates.
    - Diagnostics are reported per change and the watch continues.
    - Rapid successive edits are coalesced without dropping the final state.
  - _Dependencies: 47_
  - _Requirements: FR-061_
  - _Modules: M5, M8_
  - _Complexity: Medium_

- [x] 49\. Stale generated output detection
  - Compare recorded hashes against on-disk artifacts; regenerate in normal mode, report with a non-zero exit code in check mode without writing.
  - Acceptance Criteria:
    - `generate` reports which outputs were stale and regenerates them.
    - Check mode writes nothing and exits non-zero when drift exists.
    - Drift is reported as a `GHTMX-W03xx` diagnostic.
  - _Dependencies: 46_
  - _Requirements: FR-054_
  - _Modules: M5, M8_
  - _Complexity: Small_

- [x] 50\. Dev server with SSE live reload
  - Reverse proxy in front of the application, HTML response script injection, and an SSE hub at `/_ghtmx/reload/events` fanning out reload events — the same transport as `templ`'s live reload.
  - Acceptance Criteria:
    - The browser reloads only after a regeneration with no error-level diagnostics.
    - A failed regeneration leaves the previous good build serving and surfaces diagnostics to the terminal.
    - Reserved paths are never forwarded upstream.
  - _Dependencies: 48_
  - _Requirements: FR-063_
  - _Modules: M10_
  - _Complexity: Large_

- [x] 51\. Compile-time benchmark and regeneration gate
  - Benchmark full regeneration on a ~100-template corpus and enforce the sub-1s budget in CI; add the verbose per-phase timing and cache hit/miss report.
  - Acceptance Criteria:
    - Full regeneration of ~100 templates completes in under 1 second on the CI reference machine.
    - A breach fails the build.
    - Verbose mode attributes time to parse, discovery, analyze, and emit phases.
  - _Dependencies: 47_
  - _Requirements: NFR-001_
  - _Modules: M5, M8_
  - _Complexity: Medium_

## Milestone 9 — LSP and Editor Tooling

- [x] 52\. LSP server skeleton and live diagnostics
  - LSP endpoint with a URI-keyed document store holding text, AST, source map, and diagnostics; implement `didOpen`/`didChange`/`didSave` and `publishDiagnostics` consuming the shared AST — never a second parser.
  - Acceptance Criteria:
    - Diagnostics appear at the correct source range without a save-and-build cycle.
    - The diagnostic set and severities match what the CLI reports for the same source.
    - Resolving an issue clears its diagnostic.
  - _Dependencies: 36_
  - _Requirements: FR-080_
  - _Modules: M9_
  - _Complexity: Large_

- [x] 53\. gopls proxy and position translation
  - Spawn and supervise `gopls`; implement `PositionTranslator` rewriting positions outbound to generated Go and inbound back to `.ghtmx` across the source map.
  - Acceptance Criteria:
    - Completion, hover, and diagnostics for embedded Go expressions report positions in `.ghtmx`, not generated output.
    - A `gopls` crash degrades embedded-Go features only; `.ghtmx` diagnostics keep working and the proxy restarts with backoff.
    - A position that does not map is dropped rather than reported at a wrong location.
  - _Dependencies: 52_
  - _Requirements: FR-085_
  - _Modules: M9_
  - _Complexity: Large_

- [x] 54\. Completion providers
  - Route-aware completion (handlers filtered by the attribute's verb, constructors inserted with parameter placeholders), `hx-*` name and value completion for the configured htmx version, and event-name completion from the event registry.
  - Acceptance Criteria:
    - Completion within `hx-post={ … }` lists only handlers registered for `POST`.
    - Attribute name and value completion offer only entries valid for the configured version.
    - Event-name completion offers only declared events.
  - _Dependencies: 53_
  - _Requirements: FR-081, FR-082_
  - _Modules: M9_
  - _Complexity: Large_

- [x] 55\. Hover and go-to-definition
  - Hover showing component parameter lists and doc comments, handler verb/path/signature, and event payload types; bidirectional go-to-definition between bindings and Go handlers, and to component, fragment, and event declarations.
  - Acceptance Criteria:
    - Go-to-definition on a bound handler opens its Go declaration.
    - Go-to-definition on a component, fragment, or event reference opens its `.ghtmx` declaration.
    - Hovering an event reference shows its declared payload type.
  - _Dependencies: 54_
  - _Requirements: FR-083, FR-084_
  - _Modules: M9_
  - _Complexity: Medium_

- [x] 56\. LSP protocol tests and latency gate
  - Fixture documents driven through the server asserting completion, hover, diagnostic, and definition responses, with a latency assertion tied to the 100ms budget.
  - Acceptance Criteria:
    - Completion and diagnostic responses return in under 100ms on the fixture project.
    - Every capability from tasks 52–55 has a protocol-level test.
    - The latency assertion runs as a CI gate.
  - _Dependencies: 55_
  - _Requirements: NFR-003_
  - _Modules: M9_
  - _Complexity: Medium_

- [x] 57\. Editor extensions
  - VS Code, Neovim, and JetBrains extensions providing syntax highlighting and LSP client wiring, with a documented packaging and release path.
  - Acceptance Criteria:
    - Each extension connects to the server and surfaces diagnostics, completion, hover, and definition.
    - Syntax highlighting covers `.ghtmx`-native constructs (`fragment`, `event`, route bindings).
    - Extension versioning and its relation to the module version are documented.
  - _Dependencies: 56_
  - _Requirements: FR-080, FR-081, FR-082, FR-083, FR-084_
  - _Modules: M9_
  - _Complexity: Large_

## Milestone 10 — Framework Adapters

- [x] 58\. `net/http` adapter with opt-in automatic mode selection
  - `Render` helper inspecting `HX-Request` to select the render mode and set status code and htmx response headers, with documented defaults and explicit overrides.
  - Acceptance Criteria:
    - An htmx request renders standalone; a normal request renders the full page.
    - Automatic selection is inactive unless explicitly opted into; the core runtime performs no implicit header inspection.
    - An application importing no adapter remains fully supported.
  - _Dependencies: 40, 44_
  - _Requirements: FR-035_
  - _Modules: M7_
  - _Complexity: Medium_

- [x] 59\. chi, echo, and gin adapters
  - Mirror the `net/http` adapter shape in each framework's idiom.
  - Acceptance Criteria:
    - Each adapter performs mode selection, status, and header setting equivalently.
    - Each adapter is covered by a fixture application that builds and serves.
    - No core package depends on any adapter.
  - _Dependencies: 58_
  - _Requirements: FR-035_
  - _Modules: M7_
  - _Complexity: Medium_

- [x] 60\. fiber adapter with context bridging
  - Bridge fiber's non-`http.ResponseWriter` context type to the runtime's writer-based rendering API.
  - Acceptance Criteria:
    - Rendering through the fiber adapter produces output identical to the `net/http` path for the same inputs.
    - Mode selection, status, and headers behave equivalently.
    - A fiber fixture application builds and serves.
  - _Dependencies: 59_
  - _Requirements: FR-035_
  - _Modules: M7_
  - _Complexity: Medium_

- [x] 61\. Runtime import isolation CI check
  - Automated check asserting the root `ghtmx` package's transitive import set is standard-library only, and that framework imports appear solely within `adapters/`.
  - Acceptance Criteria:
    - Introducing a non-stdlib import into the runtime fails CI.
    - A framework import outside `adapters/` fails CI.
    - The check runs on every build.
  - _Dependencies: 60_
  - _Requirements: NFR-012_
  - _Modules: M6, M7_
  - _Complexity: Small_

## Milestone 11 — Quality Gates and Verification

- [x] 62\. Rendering conformance suite
  - Expected-HTML comparison across every escaping context, both fragment render modes, and `HX-Trigger` payload serialization.
  - Acceptance Criteria:
    - Every supported output context has a passing conformance case including a context-confusion negative case.
    - Both fragment render modes are asserted byte-identical for identical inputs.
    - The suite runs as a CI gate.
  - _Dependencies: 39, 43_
  - _Requirements: NFR-007_
  - _Modules: M4, M6_
  - _Complexity: Medium_

- [x] 63\. Render benchmark corpus and baseline record
  - Assemble the in-repo benchmark corpus covering page, fragment, and route-binding workloads; take the one-time fork-time reference measurement against the pinned `templ` version and record the baseline. Gate CI at no more than 5% regression against the recorded baseline.
  - Acceptance Criteria:
    - The pinned `templ` version and measured figures are recorded in-repo.
    - CI gates on the in-repo baseline only, never a live comparison.
    - A baseline revision requires an explicit commit recording new figures and justification.
  - _Dependencies: 39_
  - _Requirements: NFR-002_
  - _Modules: M4, M6_
  - _Complexity: Large_

- [x] 64\. `go vet` and compilation CI gate
  - Enforce the D11 CI-phase validation: `go vet` and full compilation over the golden-file corpus and every fixture application.
  - Acceptance Criteria:
    - A `go vet` finding in generated code fails the build.
    - Every golden corpus entry and fixture application compiles.
    - The gate is distinct from generate-time self-validation, and the split is documented.
  - _Dependencies: 10, 60_
  - _Requirements: NFR-005_
  - _Modules: M4_
  - _Complexity: Medium_

- [x] 65\. WASM build matrix
  - Extend CI with a fixture application importing the runtime, generated components, and the chi adapter, compiled for `GOOS=js GOARCH=wasm` and `GOOS=wasip1 GOARCH=wasm`.
  - Acceptance Criteria:
    - Both WASM targets compile with zero errors; a failure reports the offending package and symbol and blocks release.
    - A dependency unavailable on a WASM target is detected at the point of introduction.
    - Any adapter excluded from a WASM target for a documented upstream reason is recorded explicitly in the matrix.
  - _Dependencies: 61_
  - _Requirements: NFR-014_
  - _Modules: M6, M7_
  - _Complexity: Medium_

- [x] 66\. `govulncheck` release gate
  - Run `govulncheck` on every CI build; block release on a known vulnerability unless explicitly accepted with recorded rationale.
  - Acceptance Criteria:
    - A known vulnerability in the dependency graph fails the release job.
    - Accepted findings are recorded in-repo with rationale.
  - _Dependencies: 2_
  - _Requirements: NFR-008_
  - _Modules: —_
  - _Complexity: Small_

- [x] 67\. End-to-end reference CRUD application
  - Build the reference application exercising create, read, update, delete with htmx partial updates through fragments, route-aware bindings, and the event contract — with zero hand-written htmx glue code.
  - Acceptance Criteria:
    - The application builds and runs end-to-end with no hand-written URL strings and no hand-written `HX-Trigger` emission.
    - Deleting or renaming a route breaks the build at every binding site.
    - The application serves as the primary acceptance demonstration for the MVP.
  - _Dependencies: 58, 62_
  - _Requirements: FR-020, FR-021, FR-031, FR-035, FR-037_
  - _Modules: M4, M6, M7_
  - _Complexity: Large_

## Milestone 12 — Documentation

- [x] 68\. Syntax specification
  - Author the `.ghtmx` syntax specification as the test source of truth, naming the pinned `TEMPL_SYNTAX_BASELINE` and documenting every construct with a passing example.
  - Acceptance Criteria:
    - Every language feature has a documented example that runs as a test.
    - The two FR-004 carve-outs and every corpus exclusion are documented with replacement constructs.
    - Undocumented syntax is treated as unsupported and fails the coverage check.
  - _Dependencies: 34, 43_
  - _Requirements: NFR-013_
  - _Modules: M1_
  - _Complexity: Large_

- [x] 69\. Diagnostic catalogue and configuration reference
  - Document every diagnostic with its stable ID, severity, cause, and remedy; document the configuration schema with defaults and precedence. Derive both from in-repo sources so they cannot drift.
  - Acceptance Criteria:
    - Every emitted diagnostic ID appears in the catalogue; an undocumented ID fails a coverage test.
    - The configuration reference lists every setting, its default, and its CLI flag.
    - Common Go compile errors arising from D9 enforcement are mapped back to their `.ghtmx` cause.
  - _Dependencies: 68_
  - _Requirements: FR-045, FR-071_
  - _Modules: M3, M8_
  - _Complexity: Medium_

- [x] 70\. Documentation site
  - Publish the site hosting the syntax specification, getting-started guide, diagnostic catalogue, configuration reference, and supported build targets including the WASM guarantee and its scope limits.
  - Acceptance Criteria:
    - The site builds in CI and publishes on release.
    - The pinned htmx version and supported range are documented.
    - Getting-started takes a reader from install to a rendered fragment.
  - _Dependencies: 69_
  - _Requirements: NFR-013, NFR-014_
  - _Modules: —_
  - _Complexity: Large_

## Milestone 13 — Release and Deployment

- [x] 71\. Release automation
  - Automate versioned, checksummed releases of the `ghtmx` binary across the supported platforms, with compiler, runtime, LSP, and adapters versioned in lockstep from the single module.
  - Acceptance Criteria:
    - A tagged release produces checksummed binaries for all supported platforms.
    - Release artifacts carry a single version across every component.
    - The release job runs the full gate set including `govulncheck` and the WASM matrix.
  - _Dependencies: 65, 66_
  - _Requirements: NFR-008, NFR-010, NFR-014_
  - _Modules: —_
  - _Complexity: Medium_

- [x] 72\. Installation paths and changelog discipline
  - Verify `go install` and release-binary installation paths; establish the changelog-driven pre-1.0 breaking-change process with a migration note per breaking change.
  - Acceptance Criteria:
    - Both installation paths are documented and verified on all three platforms.
    - Every breaking change in the release carries a changelog entry with a migration note.
    - The pre-1.0 stability posture is stated in the README and the documentation site.
  - _Dependencies: 71_
  - _Requirements: NFR-010_
  - _Modules: —_
  - _Complexity: Small_

## Requirement Coverage Map

This map is **derived from the `_Requirements:_` annotation on each task** and must be regenerated whenever an annotation changes. A CI check regenerates it from the task list and fails on drift, so it cannot silently diverge from the tasks it describes.

### Functional requirements

| Requirement | Covered by tasks |
| --- | --- |
| FR-001 typed component declarations | 6, 8, 14 |
| FR-002 Go expression interpolation | 6, 17 |
| FR-003 control flow | 13 |
| FR-004 `templ` syntactic superset | 12, 15, 16, 18, 34 |
| FR-005 cross-package component imports | 14 |
| FR-010 syntax-only route table | 9, 21, 22 |
| FR-011 ServeMux patterns | 22 |
| FR-012 method-call registration | 23 |
| FR-013 route groups and prefixes | 24 |
| FR-014 middleware-wrapped registrations | 25 |
| FR-015 escape-hatch route declarations | 26 |
| FR-020 direct handler symbol binding | 31, 67 |
| FR-021 typed route constructors | 32, 67 |
| FR-022 verb agreement | 31 |
| FR-023 URL escaping of parameters | 33 |
| FR-024 typed `hx-*` attribute checking | 30 |
| FR-030 fragment declaration | 37 |
| FR-031 dual render entry points | 39, 67 |
| FR-032 cross-page fragment composability | 37, 38, 39 |
| FR-033 unused fragment warning | 41 |
| FR-034 handler-explicit fragment rendering | 40 |
| FR-035 adapter automatic mode selection | 58, 59, 60, 67 |
| FR-036 htmx response header emission | 43, 44 |
| FR-037 server-driven event contract | 42, 43, 67 |
| FR-040 unresolvable/mismatched binding | 31, 32 |
| FR-041 invalid `hx-*` name or value | 30 |
| FR-042 dangling swap target | 36 |
| FR-043 unreachable route warning | 36 |
| FR-044 contradictory attribute combinations | 30 |
| FR-045 diagnostic quality | 3, 69 |
| FR-050 duplicate route registration | 27 |
| FR-051 unresolvable registration | 27 |
| FR-052 htmx version mismatch | 35 |
| FR-053 circular component reference | 41 |
| FR-054 stale generated output | 49 |
| FR-055 graceful degradation on unparseable Go | 21 |
| FR-060 `generate` | 9 |
| FR-061 `watch` | 47, 48 |
| FR-062 `fmt` | 19 |
| FR-063 dev server live reload | 50 |
| FR-064 `routes` | 28 |
| FR-070 project configuration file | 5 |
| FR-071 configuration content | 5, 29, 69 |
| FR-072 convention over configuration | 5 |
| FR-073 CLI flag override | 5 |
| FR-080 real-time diagnostics | 52, 57 |
| FR-081 route-aware completion | 54, 57 |
| FR-082 `hx-*` attribute completion | 54, 57 |
| FR-083 go-to-definition | 55, 57 |
| FR-084 hover information | 55, 57 |
| FR-085 embedded Go language features | 53 |
| FR-090 component rendering API | 7, 11, 40 |
| FR-091 htmx script inclusion helper | 45 |
| FR-092 CSRF-safe request helpers | 44 |

### Non-functional requirements

| Requirement | Covered by tasks |
| --- | --- |
| NFR-001 regeneration time | 46, 51 |
| NFR-002 render throughput | 63 |
| NFR-003 LSP responsiveness | 56 |
| NFR-004 deterministic output | 10, 16, 29 |
| NFR-005 generated code validity | 2, 8, 10, 64 |
| NFR-006 parser robustness | 6, 20 |
| NFR-007 escaping correctness | 17, 33, 62 |
| NFR-008 dependency vulnerability posture | 66, 71 |
| NFR-009 diagnostic precision | 3, 4, 8 |
| NFR-010 host platform portability | 2, 71, 72 |
| NFR-011 toolchain integration | 1, 9, 11 |
| NFR-012 runtime import isolation | 1, 7, 61 |
| NFR-013 language feature coverage | 18, 68, 70 |
| NFR-014 WASM build compatibility | 65, 70, 71 |

## Critical Path

### Derived critical path

Computed mechanically from the recorded `_Dependencies:_` edges — the longest chain through the graph, twenty tasks ending at release:

```
1 → 3 → 21 → 22 → 23 → 24 → 25 → 26 → 27 → 31 → 32 → 43 → 44
  → 58 → 59 → 60 → 61 → 65 → 71 → 72
```

Route discovery dominates the path: the recogniser and prefix/middleware chain (22 → 27) is nine tasks deep before the first binding can be resolved, and every downstream milestone — bindings, events, adapters, the WASM gate, release — hangs off it. This is where schedule risk actually sits, not in the template language.

Notable secondary chains, none of which lengthen the critical path:

| Chain | Depth | Ends at |
| --- | --- | --- |
| Language surface: 1 → 3 → 6 → 8 → 12 → 13 → 14 → 15 → 16 → 18 | 10 | conformance corpus |
| Fragments: … → 14 → 37 → 38 → 39 | 11 | fragment codegen |
| MVP acceptance: … → 43 → 62 → 67 | 15 | reference CRUD app |
| Editor tooling: … → 31 → 36 → 52 → 53 → 54 → 55 → 56 → 57 | 17 | editor extensions |
| Documentation: … → 43 → 68 → 69 → 70 | 17 | documentation site |

Three tasks are reachable early and can be started as soon as their single prerequisite lands, rather than waiting on the language work: **21** (route discovery) needs only task 3, **29** (htmx surface set) needs only task 5, and **66** (`govulncheck`) needs only task 2.

### Suggested execution order

This is a **narrative reading order for a solo maintainer**, not the derived path above. It interleaves the chains so that each milestone ends on something demonstrable:

```
1 → 2 → 3 → 4 → 5                      foundations
  → 6 → 7 → 8 → 9 → 10 → 11            walking skeleton proven
  → 12 → 13 → 14 → 15 → 16 → 17 → 18   language surface complete
  → 19 → 20                            formatter and fuzzing
  → 21 → 22 → 23 → 24 → 25 → 26 → 27 → 28    route table available
  → 29 → 30 → 31 → 32 → 33 → 34 → 35 → 36    bindings: the core differentiator
  → 37 → 38 → 39 → 40 → 41             fragments
  → 42 → 43 → 44 → 45                  event contract
  → 46 → 47 → 48 → 49 → 50 → 51        incremental build and dev loop
  → 52 → 53 → 54 → 55 → 56 → 57        editor experience
  → 58 → 59 → 60 → 61                  adapters
  → 62 → 63 → 64 → 65 → 66 → 67        quality gates, then MVP acceptance
  → 68 → 69 → 70 → 71 → 72             documentation and release
```

Tasks 9 and 67 are the two proof points: task 9 establishes that the pipeline works end to end on a clean checkout, and task 67 establishes that the MVP success criterion is met. Everything between widens the surface between those two demonstrations.
