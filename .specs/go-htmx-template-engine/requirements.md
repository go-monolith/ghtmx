# Requirements — Go HTMX (`.ghtmx`)

## Overview

`.ghtmx` is an open source Go template engine, forked from `templ`, in which htmx is a first-class language concept rather than a collection of hand-written string attributes.

The engine compiles `.ghtmx` source files into plain Go. Its distinguishing capability is that htmx bindings reference **Go handler symbols and generated typed route constructors** instead of URL string literals, and are validated at build time against a route table derived by static analysis of the application's own Go source. A binding that cannot be resolved is a compile error, never a runtime 404 or a silently empty swap.

The second differentiator is **compile-time fragment-of-a-page**: an author declares a fragment inside a page template and the compiler emits two entry points for it — one that renders it inline as part of the full page, one that renders it standalone for an htmx swap. One template, two render modes, no duplicated markup. Fragment rendering is not itself new — upstream `templ` provides it as a runtime facility that renders the enclosing page and discards the markup outside the selected fragment. The `.ghtmx` contribution is to resolve the mode at build time into two directly-callable, typed entry points, so a standalone swap executes only the fragment body and an unresolvable fragment reference is a compile error rather than an empty response.

The third is the **server-driven event contract**: `HX-Trigger` events are declared in the template with typed payloads, and the compiler generates the only symbols by which a handler can emit them — making an undeclared event a build failure rather than a silently ignored string.

### Scope of this document

This document specifies **what** the engine must do, not how it is built. Architectural decisions (compiler pipeline structure, package loading strategy, module layout) are fixed by the project constitution and elaborated in the solution design.

### In scope for the MVP

Compiler and code generator; route-discovery analysis; runtime library; CLI (`generate`, `watch`, `fmt`, `routes`, dev server); LSP server with editor extensions; framework adapters for `net/http`, chi, echo, gin, and fiber; documentation site.

### Out of scope for the MVP

templ-to-ghtmx migration tooling; CSS/asset pipeline or bundling; i18n/localization primitives; out-of-band (OOB) swap validation; anonymous handler functions and programmatically-generated routes in route discovery; full URL sanitization (scheme allow-lists).

### Definition of success

A reference CRUD application with htmx partial updates builds and runs end-to-end with **zero hand-written htmx glue code**.

## User Roles

`.ghtmx` is a developer tool and a library. It has **no runtime authentication or authorization model of its own** — it renders HTML and does not own sessions, identity, or permissions. The roles below are development personas that define whose needs each requirement serves. Any access control in a `.ghtmx` application is the application's responsibility, not the engine's.

| Role | Description | Primary interactions |
| --- | --- | --- |
| **Template Author** | Writes `.ghtmx` files: components, fragments, htmx bindings, event contracts. The primary MVP audience (existing `templ` users). | Template language, compile diagnostics, LSP, `fmt` |
| **Application Developer** | Wires Go handlers and routers, calls generated render and event-emission entry points, configures the project. Often the same person as the Template Author. | Route registration, runtime API, adapters, config file |
| **Build / CI System** | Non-human actor running the toolchain in automation. | `generate`, `fmt --check`, exit codes, deterministic output |
| **Adapter Author** | Writes an integration for a router the project does not ship first-party support for. | Public runtime and route-table extension surfaces |
| **Engine Contributor** | Develops the compiler, runtime, or tooling itself. | Syntax spec, golden-file corpus, benchmark baseline |

## Functional Requirements

### Template Language

#### FR-001 — Typed component declarations

The language MUST support declaring reusable components with typed Go parameters.

Acceptance criteria:

- A component declared with parameters of Go types compiles to a Go function accepting those same types.
- Calling a component with a wrong argument type or arity is a compile error reported at the call site.
- A component with zero parameters is valid.

#### FR-002 — Go expression interpolation

The language MUST support interpolating Go expressions into template output, escaped according to the output context.

Acceptance criteria:

- An interpolated expression renders its value escaped for its surrounding context (HTML text, attribute value, URL, JS, CSS).
- A type that cannot be rendered produces a compile error, not a runtime failure.
- Bypassing escaping requires an explicit, named construct at the call site; there is no global escape-disabling switch.

#### FR-003 — Control flow

The language MUST support conditional and iterative constructs over Go expressions (`if`/`else`, `for`, `switch`).

Acceptance criteria:

- Each construct compiles to the equivalent Go control flow in the generated code.
- Variables bound in a control-flow construct are in scope for nested template content and are type-checked by the Go compiler.

#### FR-004 — Syntactic superset of current `templ` (MVP)

For the MVP, the `.ghtmx` language surface MUST be a **syntactic superset of the current `templ` syntax**, subject to two explicitly documented semantic carve-outs.

Every construct valid in the referenced `templ` version SHALL parse in `.ghtmx` with equivalent semantics, **EXCEPT**:

1. **`hx-*` attributes**, which are reinterpreted as typed bindings per FR-024 rather than treated as ordinary string attributes. Source that passes an arbitrary string to an `hx-*` attribute MAY therefore fail to compile as `.ghtmx`.
2. **URL-context escaping**, which is engine-determined per FR-023 rather than author-selected. `templ`'s author-discretionary safe-URL handling does not carry over at a route-binding site.

Acceptance criteria:

- The syntax specification names the exact `templ` version whose surface is covered.
- Constructs inherited from that surface — including children/slot composition, dynamic and conditional attributes, attribute spreading, scoped `css` blocks, and the inherited JavaScript-interop surface (script templates, typed JS function-call expressions, JSON-payload script emission, and once-only inclusion handles for scripts and styles) — parse and render with equivalent semantics.
- The inherited JavaScript-interop surface is enumerated construct-by-construct in the syntax specification, so the superset guarantee is testable rather than asserted.
- A conformance corpus derived from the referenced `templ` syntax passes, **excluding** cases covered by carve-outs 1 and 2.
- Every excluded case is individually documented with the carve-out it falls under and the `.ghtmx` construct that replaces it.
- Each carve-out produces a diagnostic that names the carve-out and the replacement construct, rather than a generic parse or type error.

> **Note:** This superset relationship is an **MVP-only guarantee**. Per the constitution's hard-fork posture, later versions MAY diverge. The guarantee is not a compatibility commitment and is not covered by any deprecation policy.

#### FR-005 — Cross-package component imports

Components MUST be importable and callable across Go packages.

Acceptance criteria:

- A component defined in package `a` can be called from a template in package `b` using normal Go import semantics.
- The generated code exports components following Go visibility rules (capitalized identifiers are exported).
- An unresolvable component reference is a compile error naming the missing symbol.

### Route Discovery

#### FR-010 — Syntax-only route table construction

The compiler MUST build a route table (verb, path pattern, path parameter names, handler package path, handler symbol, source position) by analyzing the application's Go source **without requiring that source to type-check or compile**.

Acceptance criteria:

- `ghtmx generate` in a clean checkout containing zero generated `.go` files produces a complete route table and a complete set of generated components in a single pass.
- A type error elsewhere in the module does not prevent route table construction.
- Handler symbols are resolved by package-qualified identifier matching using each file's import declarations.

#### FR-011 — `net/http` ServeMux patterns

Discovery MUST recognize Go 1.22+ `ServeMux` method-and-path patterns.

Acceptance criteria:

- `mux.HandleFunc("GET /users/{id}", handlers.GetUser)` yields verb `GET`, path `/users/{id}`, parameter `id`, handler `handlers.GetUser`.
- `mux.Handle` with an equivalent pattern is recognized identically.
- Wildcard (`{path...}`) and trailing-slash patterns are recorded with their pattern semantics intact.

#### FR-012 — Method-call registration for supported routers

Discovery MUST recognize method-call registration forms used by chi, echo, gin, and fiber.

Acceptance criteria:

- `r.Get("/users/{id}", h)`, `e.GET("/users/:id", h)`, `app.Post("/users", h)` and equivalents for all five supported verbs are recognized.
- Each router's path parameter syntax (`{id}` vs `:id`) is normalized into a common route table representation preserving the original form.
- The router flavour in use is determined per registration site, not assumed globally.

#### FR-013 — Route groups and nested prefixes

Discovery MUST compose path prefixes across route groups and nested route trees.

Acceptance criteria:

- A route registered inside `r.Route("/api", ...)` or `r.Group(...)` resolves to the fully-composed path.
- Arbitrarily nested groups compose correctly.
- A group whose prefix cannot be determined statically is reported per FR-051, not silently dropped.

#### FR-014 — Middleware-wrapped registrations

Middleware application MUST NOT hide a route from discovery.

Acceptance criteria:

- `r.With(mw).Get("/users", h)` is discovered with the same verb, path, and handler as the unwrapped form.
- Middleware applied at group level does not affect the discovered path or handler.
- Handlers wrapped in a middleware call expression still resolve to the underlying handler symbol where that symbol is syntactically present.

#### FR-015 — Escape-hatch route declarations

The engine MUST provide an explicit annotation allowing an author to declare a route that syntax-only discovery cannot resolve.

Acceptance criteria:

- A declared route enters the route table with the same fields and is bindable exactly like a discovered route.
- A declared route that duplicates a discovered route is reported per FR-050.
- The annotation form is documented and covered by the syntax specification.

### Route-Aware Bindings

#### FR-020 — Direct handler symbol binding (non-parameterised routes)

For routes with no path parameters, a binding MUST accept a direct Go handler symbol reference.

Acceptance criteria:

- `hx-post={ handlers.CreateUser }` resolves to that handler's registered path and emits it as the attribute value.
- Using a symbol that is not a registered handler is a compile error naming the symbol.
- The form is supported for `hx-get`, `hx-post`, `hx-put`, `hx-patch`, and `hx-delete`.

#### FR-021 — Generated typed route constructors (parameterised routes)

For routes with path parameters, the compiler MUST generate a typed constructor function per route, callable from a binding.

Acceptance criteria:

- `hx-get={ routes.GetUser(user.ID) }` and `hx-delete={ routes.DeleteUser(user.ID) }` compile and produce correctly-substituted URLs.
- The constructor's parameter list matches the route's path parameters in order; wrong arity or wrong argument type is a compile error.
- Constructors are regenerated when the route table changes, so a renamed or re-pathed route surfaces as a compile error at every call site.

#### FR-022 — Verb agreement

The binding attribute's HTTP verb MUST match the verb the handler is registered under.

Acceptance criteria:

- Binding `hx-post` to a handler registered only for `GET` is a compile error stating both the expected and actual verb.
- A handler registered for multiple verbs is bindable from each corresponding attribute.

#### FR-023 — URL escaping of interpolated parameters

Values substituted into a generated `hx-*` URL MUST be URL-escaped by the engine.

Acceptance criteria:

- A parameter containing `/`, `?`, `#`, `&`, or a space is percent-encoded appropriately for its position (path segment vs. query value).
- URL escaping is applied before, and composes with, HTML attribute-value escaping.
- The escaping context is determined by the engine from the attribute's declared type and cannot be overridden at the binding site.

#### FR-024 — Typed `hx-*` attribute checking

`hx-*` attributes MUST be validated against the attribute surface of the pinned htmx version.

Acceptance criteria:

- An unknown `hx-*` attribute name is a compile error listing the closest valid names.
- An invalid value for a constrained attribute (e.g. an unrecognized `hx-swap` mode) is a compile error listing the valid values.
- Valid attributes and values compile without warning.

### Fragments

#### FR-030 — Fragment declaration

The language MUST provide a `fragment` construct declaring a named, parameterised region inside a page template.

Acceptance criteria:

- A fragment declared within a page participates in that page's rendered output at its declaration site.
- A fragment accepts typed Go parameters like a component.
- Fragment names are unique within their scope; a duplicate is a compile error.

#### FR-031 — Dual render entry points

The compiler MUST emit two distinct, explicitly-callable entry points per fragment: one rendering it inline within the full page, one rendering it standalone.

Acceptance criteria:

- The standalone entry point emits only the fragment's markup, with no surrounding page chrome.
- The inline path emits byte-identical fragment markup to the standalone path for identical inputs.
- Both entry points are exported following Go visibility rules and are callable without any adapter.

#### FR-032 — Cross-page fragment composability

A single fragment MUST be referenceable and renderable from more than one page.

Acceptance criteria:

- A fragment referenced by two pages compiles once and is called from both.
- Each referencing page renders the fragment inline correctly.
- The standalone entry point is independent of which page references it.

#### FR-033 — Unused fragment warning

The compiler MUST warn — not error — when a declared fragment is never rendered or bound.

Acceptance criteria:

- A fragment with no inline reference and no binding produces a warning naming its declaration site.
- The warning does not fail the build and does not affect the exit code by default.

#### FR-034 — Handler-explicit fragment rendering

An application developer MUST be able to render a fragment explicitly, retaining full control over HTTP status code and response headers.

Acceptance criteria:

- A handler can call the standalone entry point directly against an `io.Writer` / `http.ResponseWriter`.
- When rendering explicitly, the engine writes no HTTP status code and no response headers that the handler has not requested.
- This path works with no adapter imported.

#### FR-035 — Adapter automatic mode selection (opt-in)

Adapters MUST offer an opt-in helper that selects the render mode automatically and sets the appropriate status code and htmx response headers.

Acceptance criteria:

- The helper inspects the `HX-Request` header and renders standalone for htmx requests, full-page otherwise.
- The helper sets the HTTP status code and htmx response headers on the developer's behalf, with documented defaults and explicit overrides.
- Automatic selection is never active unless the developer opts in; the core runtime performs no implicit header inspection.

#### FR-036 — htmx response header emission

The runtime MUST provide typed helpers for htmx response headers.

Acceptance criteria:

- `HX-Retarget`, `HX-Reswap`, and `HX-Redirect` are settable through typed helpers rather than raw strings.
- `HX-Trigger` events are emitted only through the generated per-event symbols defined by FR-037, with the event name and payload serialized correctly.
- Setting a header helper with a value invalid for the pinned htmx version is a compile error per FR-052.

#### FR-037 — Server-driven event contract

The language MUST provide an `event` declaration binding an `HX-Trigger` event name to a typed payload, and the compiler MUST generate the sole Go symbols through which that event can be emitted.

**Enforcement model.** The compiler does not inspect handler bodies — this is impossible under the syntax-only analysis mandated by FR-010. Instead, enforcement is delegated to the Go compiler: because the *only* way to emit an event is to call a generated per-event symbol, an undeclared event has no symbol to call and fails to compile at the handler call site as an undefined identifier. This makes the guarantee structural rather than analytical.

Acceptance criteria:

- An `event` declaration names the event and declares a typed payload; a declaration with no payload is valid.
- For each declared event the compiler generates an exported, uniquely-named Go emission symbol accepting the declared payload type and writing a correctly-serialized `HX-Trigger` header.
- Emitting an event that is not declared in any template is a Go compile error at the handler call site (undefined symbol). No untyped string-based emission API is exposed by the runtime.
- Passing a payload whose type does not match the declaration is a Go compile error at the call site.
- Multiple events emitted in one response are merged into a single, correctly-serialized `HX-Trigger` header.
- Renaming or removing an `event` declaration breaks every handler call site at compile time.
- A template-side reference to an event name (for example in `hx-on::` or `hx-trigger`) that no `event` declaration defines is a `.ghtmx` compile error naming the undeclared event.
- A declared event that is never emitted and never referenced produces a warning, consistent with FR-033 and FR-043.

### Compile-Time Diagnostics

#### FR-040 — Unresolvable or mismatched route binding

A binding that cannot be resolved against the route table MUST be a compile error.

Acceptance criteria:

- An unknown handler symbol, a verb mismatch (FR-022), and wrong constructor arity or type (FR-021) each produce a distinct, specific error.
- Every such error names the offending template location and the relevant route or handler.

#### FR-041 — Invalid `hx-*` attribute name or value

Per FR-024, invalid attribute names and values MUST be compile errors with suggestions.

#### FR-042 — Dangling swap target (warning)

An `hx-target` or `hx-select` whose fully-literal ID selector matches no fully-literal ID emitted anywhere in the compiled template set MUST produce a **warning** by default, promotable to an error under an opt-in strict mode.

The check is deliberately conservative. The set of IDs a `.ghtmx` application emits is not statically closed: IDs are commonly produced by interpolated Go expressions, fragments may be swapped into pages the compiler never analyzes together, and target elements may originate from markup outside the engine's control. Erroring by default would fail correct builds.

Acceptance criteria:

- The check applies **only** to fully-literal ID selectors matched against fully-literal emitted IDs. A selector or an emitted ID containing any interpolated expression is exempt from analysis entirely.
- A target whose corresponding ID is emitted anywhere in the compiled template set SHALL NOT produce a diagnostic, regardless of which template emits it.
- A dynamically-computed target expression SHALL NOT produce a diagnostic and SHALL NOT be reported as unresolvable.
- By default the diagnostic is a warning: it does not fail the build and does not affect the exit code.
- An opt-in strict mode (configuration setting and equivalent CLI flag) promotes the warning to an error.
- The warning names the selector, its source location, and states that the check covers statically-analyzable literal IDs only.
- The documentation records the check's scope and its known false-negative cases.

#### FR-043 — Unreachable route warning

The compiler MUST warn when a discovered route is never bound from any template.

Acceptance criteria:

- An unbound route produces a warning naming its registration site.
- The warning does not fail the build by default.

#### FR-044 — Contradictory attribute combinations

Mutually incompatible `hx-*` attribute combinations MUST be compile errors.

Acceptance criteria:

- Combinations invalid for the pinned htmx version (e.g. conflicting `hx-swap` modifiers) are rejected with an explanation of the conflict.
- The rule set is documented and versioned alongside the htmx attribute surface.

#### FR-045 — Diagnostic quality

Every diagnostic MUST carry a precise `file:line:column` position and, where the fix is determinable, a suggested correction.

Acceptance criteria:

- Every diagnostic emitted by the compiler includes a source position resolving to the correct `.ghtmx` or `.go` location.
- Diagnostics are emitted in a stable, machine-parseable format consumable by the LSP.
- Diagnostics carry a severity (error or warning) and a stable identifier, so warning-level checks can be individually configured.
- Positions map to original source, not generated output.

### Error and Edge Case Handling

#### FR-050 — Duplicate or conflicting route registration

Two registrations of the same verb and path MUST be a compile error.

Acceptance criteria:

- The error names both registration sites.
- A discovered route conflicting with an escape-hatch declaration (FR-015) is reported identically.

#### FR-051 — Statically unresolvable registration

A route whose path or handler cannot be determined syntactically MUST be a compile error pointing to the escape hatch.

Acceptance criteria:

- Computed path strings and dynamic registration forms produce an error naming the registration site.
- The error message explicitly directs the author to the FR-015 annotation.
- Unsupported forms (anonymous handlers, loop-generated routes) are reported this way rather than silently ignored.

#### FR-052 — htmx version mismatch

Use of a construct unsupported by the configured htmx version MUST be a compile error.

Acceptance criteria:

- The error names the construct, the configured htmx version, and the version that introduced or removed it.
- The runtime script-inclusion helper validates that the served htmx version matches the configured one.

#### FR-053 — Circular component reference

A cycle in component or fragment references MUST be detected at compile time.

Acceptance criteria:

- A direct or indirect cycle produces an error listing the full reference chain.
- Detection terminates deterministically without stack exhaustion.

#### FR-054 — Stale generated output detection

The toolchain MUST detect generated files that are older than, or inconsistent with, their `.ghtmx` source.

Acceptance criteria:

- `generate` reports which outputs were stale and regenerates them.
- A check mode reports staleness with a non-zero exit code without writing files, for CI use.

#### FR-055 — Graceful degradation on unparseable Go

A Go package that cannot be parsed MUST NOT abort the entire run.

Acceptance criteria:

- The compiler reports a diagnostic for the unparseable package and continues analyzing the remainder.
- Partial results plus accumulated diagnostics are produced rather than a first-error abort.
- Bindings that depend on the failed package are reported as unresolvable (FR-040) rather than silently omitted.

### CLI

#### FR-060 — `generate`

One-shot code generation across the module.

Acceptance criteria:

- Generates output for every `.ghtmx` file in the configured scope.
- Exits non-zero if any error-level diagnostic is produced; warnings alone do not fail the run.
- Produces byte-identical output for identical input across runs and machines.

#### FR-061 — `watch`

Incremental regeneration on file change.

Acceptance criteria:

- Watches both `.ghtmx` files and Go source in the route-discovery scope.
- A change to a Go handler's route registration triggers regeneration of affected route constructors and dependent templates.
- Diagnostics are reported per change without terminating the watch.

#### FR-062 — `fmt`

Canonical, idempotent formatting.

Acceptance criteria:

- Formatting an already-formatted file produces no change (idempotence).
- A `--check` mode reports unformatted files with a non-zero exit code and writes nothing.
- The formatter preserves semantics and does not reorder or drop content.

#### FR-063 — Dev server with live reload

Serves the application and refreshes the browser after successful regeneration.

Acceptance criteria:

- The browser reloads only after a regeneration that produced no error-level diagnostics.
- A regeneration failure leaves the last good build serving and surfaces the diagnostics.
- Responses to htmx-initiated requests (those carrying `HX-Request`) are proxied through **unmodified**: no reload script is injected into a partial response, so a swapped fragment never receives injected markup and the script is never duplicated per swap.
- Body modification applies only to full-page HTML responses; a compressed response is decoded before injection and re-encoded afterwards, and any response the server cannot safely modify is forwarded unchanged.
- The reload event channel is served on a reserved path that is never forwarded to the application.

#### FR-064 — `routes`

Prints the discovered route table for debugging.

Acceptance criteria:

- Outputs verb, path, handler package and symbol, and source position for every discovered and declared route.
- Marks escape-hatch declarations distinctly from discovered routes.
- Supports a machine-readable output format.

### Configuration

#### FR-070 — Project configuration file

A configuration file at the module root MUST be supported.

Acceptance criteria:

- The file is discovered from the module root; its absence is not an error (see FR-072).
- An invalid or unparseable configuration file is an error naming the offending key and position.

#### FR-071 — Configuration content

Configuration MUST cover at minimum the pinned htmx version, template source directories, route-discovery package scope, generated-file naming, and per-check severity settings including the FR-042 strict mode.

Acceptance criteria:

- Each setting takes effect and is documented with its default.
- The pinned htmx version drives attribute validation (FR-024) and the script-inclusion helper (FR-052).
- Warning-level checks are individually configurable by their stable diagnostic identifier (FR-045).

#### FR-072 — Convention over configuration

A project following default conventions MUST work with no configuration file.

Acceptance criteria:

- With no config file present, `generate` succeeds on a conventional project layout.
- Defaults are documented explicitly.

#### FR-073 — CLI flag override

CLI flags MUST override configuration file values.

Acceptance criteria:

- Every configurable setting has a corresponding flag.
- Precedence is flag > config file > default, and is documented.

### LSP

#### FR-080 — Real-time diagnostics

Compiler diagnostics MUST surface live in the editor.

Acceptance criteria:

- Diagnostics appear at the correct source range without an explicit save-and-build cycle.
- The diagnostic set and severities match what the CLI reports for the same source.
- Resolving an issue clears its diagnostic.

#### FR-081 — Route-aware completion

Completion inside a route binding MUST suggest valid handler symbols and route constructors.

Acceptance criteria:

- Completion within `hx-post={ … }` lists handlers registered for `POST`.
- Suggestions for parameterised routes insert the constructor with its parameter placeholders.

#### FR-082 — `hx-*` attribute completion

Attribute name and value completion MUST reflect the pinned htmx version.

Acceptance criteria:

- Attribute name completion offers only attributes valid for the configured version.
- Value completion for constrained attributes offers only valid values.
- Completion of an event name in an event-consuming attribute offers only events declared per FR-037.

#### FR-083 — Go-to-definition across template ↔ Go

Navigation MUST work in both directions between a binding and its Go handler.

Acceptance criteria:

- Go-to-definition on a bound handler symbol opens the handler's Go declaration.
- Go-to-definition on a component, fragment, or event reference opens its `.ghtmx` declaration.

#### FR-084 — Hover information

Hover MUST show types and documentation for components, fragments, events, and handlers.

Acceptance criteria:

- Hovering a component reference shows its parameter list and doc comment.
- Hovering a bound handler shows its verb, path, and Go signature.
- Hovering an event reference shows its declared payload type.

#### FR-085 — Embedded Go language features

Go expressions inside templates MUST receive full Go language assistance.

Acceptance criteria:

- Completion, hover, and diagnostics are available for Go expressions embedded in templates.
- Positions reported for embedded Go map back to the `.ghtmx` source, not generated output.

### Runtime

#### FR-090 — Component rendering API

The runtime MUST expose a stable component interface and rendering entry points.

Acceptance criteria:

- Generated components satisfy a documented interface accepting a context and a writer and returning an error.
- Rendering is usable from any `http.Handler` without importing an adapter.
- Render errors propagate to the caller rather than being swallowed or panicking.

#### FR-091 — htmx script inclusion helper

The runtime MUST provide a helper that emits the htmx script tag for the configured version.

Acceptance criteria:

- The emitted tag references the configured htmx version.
- A mismatch between the configured version and the served asset is reported per FR-052.

#### FR-092 — CSRF-safe request helpers

Attaching CSRF tokens to state-changing htmx requests MUST NOT require hand-written header plumbing.

Acceptance criteria:

- A documented helper attaches a token via `hx-headers` or an equivalent mechanism.
- The mechanism is available for `hx-post`, `hx-put`, `hx-patch`, and `hx-delete`.
- The token source is supplied by the application; the engine does not generate or validate tokens.

## Non-Functional Requirements

### NFR-001 — Full project regeneration time

Full regeneration MUST complete in **under 1 second** for a project of approximately 100 templates.

- Measured on the in-repo benchmark corpus (see DATA-006) in CI.
- A breach fails the build.

### NFR-002 — Render throughput

Render throughput MUST NOT regress by more than **5%** against the recorded in-repo baseline.

- Enforced in CI against the recorded baseline, never a live comparison with another project.
- Baseline revisions require an explicit, reviewed commit recording new figures and justification.

### NFR-003 — LSP responsiveness

Completion and diagnostic responses MUST return in **under 100 ms** on a typical project.

- Measured by protocol-level latency assertions against a fixture project.

### NFR-004 — Deterministic output

Identical input MUST produce **byte-identical** generated Go output.

- Independent of map iteration order, filesystem ordering, timestamps, host OS, and machine.
- Verified by regenerating the golden-file corpus and diffing; any drift fails CI.

### NFR-005 — Generated code validity

100% of generated Go MUST compile, pass `go vet`, and be `gofmt`-clean.

- Enforced across the entire golden-file corpus on every CI run.
- The compiler MUST fail with a diagnostic rather than write invalid output.

### NFR-006 — Parser robustness

The lexer and parser MUST NOT panic on any input.

- Enforced by continuous fuzzing; every crash found becomes a permanent regression test.
- Malformed input yields diagnostics, never a panic or non-termination.

### NFR-007 — Output escaping correctness

Contextual escaping MUST be correct for every supported output context.

- Verified by a rendering conformance suite covering HTML text, attribute, URL, JS, and CSS contexts.
- Includes the route-binding URL path (FR-023) and `HX-Trigger` payload serialization (FR-037) as mandatory cases.

### NFR-008 — Dependency vulnerability posture

`govulncheck` MUST run on every CI build; a known vulnerability blocks release until resolved or explicitly accepted with recorded rationale.

### NFR-009 — Diagnostic precision

100% of user-facing diagnostics MUST carry a `file:line:column` position resolving to original source.

- Verified by assertion in the diagnostic test suite; a positionless diagnostic is a defect.
- Every diagnostic carries a severity and a stable identifier (FR-045).

### NFR-010 — Host platform portability

The toolchain MUST run on Linux, macOS, and Windows on the supported Go versions.

- CI builds and tests on all three platforms.
- The supported Go version range is documented and tested.

### NFR-011 — Toolchain integration

Generated output MUST require no build step beyond `ghtmx generate` and MUST be consumable by the standard `go build` toolchain.

- No `cgo`, no Node.js or npm dependency in the core workflow.

### NFR-012 — Runtime import isolation

Tooling dependencies (CLI, LSP, dev server) MUST NOT appear in the transitive import graph of an application that imports only the runtime package.

- Verified by an automated check on the runtime package's transitive imports.

### NFR-013 — Language feature coverage

Every language feature MUST have a passing specification example before merge.

- The syntax specification is the source of truth for the test corpus; undocumented syntax is unsupported syntax.

### NFR-014 — WebAssembly build compatibility

A Go application that imports the `.ghtmx` runtime, its generated components, and a first-party router adapter MUST compile successfully for WebAssembly targets without error.

**Target:** 100% of WASM fixture builds succeed. Compilation MUST produce zero errors for `GOOS=js GOARCH=wasm` and `GOOS=wasip1 GOARCH=wasm`.

**Priority:** High — a compilation failure on a WASM target blocks release.

Acceptance criteria:

- WHEN CI runs THEN the build matrix SHALL include a fixture application that imports the runtime, generated `.ghtmx` components, and the `go-chi/chi` adapter, and SHALL compile it for `GOOS=js GOARCH=wasm`.
- WHEN CI runs THEN the same fixture SHALL additionally be compiled for `GOOS=wasip1 GOARCH=wasm`.
- IF any WASM target build produces a compilation error THEN CI SHALL fail the build and report the offending package and symbol.
- WHEN the runtime or generated code introduces a dependency unavailable on a WASM target THEN the automated test SHALL detect it at the point of introduction rather than at release time.
- WHERE a first-party adapter cannot support a WASM target for a documented upstream reason, the project SHALL record that exclusion explicitly in the test matrix rather than omitting the adapter silently.

**Constraints this imposes:**

- The runtime MUST NOT depend on packages or syscalls unavailable under `js/wasm` or `wasip1/wasm`.
- The runtime MUST NOT require `cgo` (reinforces NFR-011).
- This requirement governs **compilation success only**. Runtime behaviour under WASM, browser-hosted execution, and WASM-specific performance are out of MVP scope.

## Data Requirements

The engine persists no user data, operates no database, and processes no personally identifiable information. "Data" here means the compiler's own artifacts and models.

### DATA-001 — `.ghtmx` source files

Primary input. UTF-8 encoded, `.ghtmx` extension, located in configured source directories. Source positions (line, column, byte offset) MUST be tracked for every construct to satisfy NFR-009.

### DATA-002 — Shared AST

A single AST representation is the contract consumed by the parser, formatter, and LSP. It MUST carry source positions sufficient for diagnostics, formatting, and editor navigation. Duplicate or divergent parse trees are prohibited.

### DATA-003 — Route table

The central data structure linking Go source to template bindings. Each entry MUST record:

- HTTP verb
- Path pattern, in a normalized representation preserving the original router flavour's syntax
- Ordered path parameter names
- Handler package import path and symbol name
- Source position of the registration site
- Origin: discovered vs. escape-hatch declared

The table MUST be constructible without type information (FR-010) and MUST be serializable for the `routes` command's machine-readable output (FR-064).

### DATA-004 — Event contract registry

The set of `event` declarations (FR-037) across the compiled template set. Each entry MUST record the event name, its declared payload type, its declaring source position, and the generated emission symbol. Event names MUST be unique across the compiled set; a collision is a compile error. This registry is the authority for event-name validation, LSP completion (FR-082), and hover (FR-084).

### DATA-005 — Generated Go artifacts

Compiler output, including components, fragment entry points, route constructors, and event emission symbols. MUST be deterministic (NFR-004), valid and formatted (NFR-005), and follow a documented naming convention. Generated files are expected to be committed and diffed by users, so their textual stability is a functional property, not an implementation detail.

### DATA-006 — htmx attribute surface definition

A versioned description of htmx's attribute names, permitted values, and invalid combinations, keyed by htmx version. It is the authority for FR-024, FR-041, FR-044, FR-052, and FR-082. It MUST be updatable independently of compiler logic so an htmx version bump does not require rewriting the validator.

### DATA-007 — Benchmark corpus and baseline record

A named, in-repo corpus of representative page, fragment, and route-binding workloads, plus a recorded baseline of measured figures and the pinned `templ` version used for the one-time fork-time reference measurement. Sole input to NFR-001 and NFR-002 gates.

### DATA-008 — Project configuration

Structured configuration at the module root covering the pinned htmx version, source directories, route-discovery scope, output naming, and per-check severity settings. Schema MUST be documented with defaults; invalid keys and values MUST produce positioned diagnostics.

### DATA-009 — Golden-file and build-fixture corpora

Paired `.ghtmx` inputs and expected `.go` outputs covering every language construct, regenerated and diffed on every test run; drift is a test failure. This corpus also includes the compilable fixture applications used by the WASM build matrix (NFR-014), each pairing templates, handlers, and a router adapter.

## Integration Requirements

### INT-001 — htmx (JavaScript library)

The engine targets a **pinned htmx version**, declared in configuration and documentation.

- The pinned version determines the valid attribute surface (DATA-006) and drives FR-024, FR-044, and FR-052.
- The `HX-Trigger` serialization format produced by FR-037 MUST match the pinned version's expected format.
- The runtime script-inclusion helper (FR-091) emits and validates against that version.
- An htmx major-version upgrade is a deliberate, documented, changelog-recorded change — never implicit.

### INT-002 — `net/http`

First-party adapter and route discovery for the standard library.

- Discovery supports Go 1.22+ `ServeMux` method-and-path patterns (FR-011).
- The runtime MUST be fully usable with `net/http` alone, with no adapter imported (FR-034, FR-090).

### INT-003 — chi

- Discovery supports chi's method-call registration, `Route`/`Group` nesting, and `With` middleware chaining (FR-012, FR-013, FR-014).
- Adapter provides opt-in automatic render-mode selection (FR-035).
- Serves as the reference adapter in the WASM build matrix (NFR-014).

### INT-004 — echo

- Discovery supports echo's method-call registration and route groups.
- Echo's `:param` path syntax is normalized into the route table (FR-012).
- Adapter provides opt-in automatic render-mode selection.

### INT-005 — gin

- Discovery supports gin's method-call registration, route groups, and middleware chaining.
- Gin's `:param` and `*wildcard` path syntax is normalized into the route table.
- Adapter provides opt-in automatic render-mode selection.

### INT-006 — fiber

- Discovery supports fiber's method-call registration and route groups.
- Fiber's non-`http.ResponseWriter` context type is bridged to the runtime's writer-based rendering API.
- Adapter provides opt-in automatic render-mode selection.

### INT-007 — Go toolchain

- Route discovery consumes Go source via a package loader operating in syntax-only mode (FR-010).
- Event contract enforcement is delegated to the Go compiler via generated symbols (FR-037).
- Generated output is validated with `gofmt` and `go vet` (NFR-005).
- Cross-compilation to WASM targets uses the standard toolchain with no custom build step (NFR-014, NFR-011).
- Adapter and core packages MUST NOT depend on any adapter (constitution A5).

### INT-008 — Language Server Protocol and editors

- The LSP server implements the protocol capabilities required by FR-080 through FR-085.
- Editor extensions are provided for VS Code, Neovim, and JetBrains IDEs.
- The server consumes the shared AST (DATA-002); it MUST NOT embed a second parser.
- Embedded-Go assistance (FR-085) is delegated to an external `gopls` process. An absent, unresolvable, or incompatible `gopls` MUST degrade only the embedded-Go features, MUST be reported to the user, and MUST NOT suppress `.ghtmx` diagnostics, completion, or navigation.

### INT-009 — Dev server browser channel

- The dev server maintains a browser reload channel that triggers only after a diagnostic-free regeneration (FR-063).
- A failed regeneration leaves the previous good build serving.
- The channel is served on a reserved path that is never proxied to the application, and htmx partial responses are excluded from reload-script injection (FR-063).

### INT-010 — CI and release automation

- CI enforces NFR-001 through NFR-014 on every build across the host platforms in NFR-010 and the WASM targets in NFR-014.
- The build matrix includes `GOOS=js GOARCH=wasm` and `GOOS=wasip1 GOARCH=wasm` fixture compilation.
- `govulncheck` gates release (NFR-008).
- Release artifacts are versioned in lockstep across compiler, runtime, LSP, and adapters (single-module, single-version).

### INT-011 — Documentation site

- Publishes the syntax specification that serves as the test source of truth (NFR-013).
- Documents the pinned htmx version, configuration schema and defaults, and every diagnostic with its stable identifier, severity, and remedy.
- Documents the FR-004 carve-outs and each excluded `templ` construct with its `.ghtmx` replacement.
- Documents supported build targets, including the WASM compilation guarantee and its scope limits (NFR-014).
