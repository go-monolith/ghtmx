# Project Constitution — Go HTMX (`.ghtmx`)

> This document defines the non-negotiable engineering constraints for the `.ghtmx` project.
> Changes to this constitution require an explicit, recorded decision; individual features may not silently override it.

## Project Vision

### Problem

Go's server-rendered UI story is strong (`templ` proved the compiled-template model works), but htmx integration remains a matter of hand-written strings. Authors type `hx-get="/users/42"` as an opaque string literal, hand-write a separate handler to serve the partial, and discover mismatches only at runtime as a 404 or an empty swap. The htmx contract — which URL, which verb, which fragment, which event — is invisible to the compiler.

### Target Users

Primary audience for the MVP: **existing `templ` users who want deeper htmx ergonomics**. These are Go backend engineers already comfortable with compiled templates and server-driven UI, who are currently paying a manual glue-code tax for every interactive region.

### Long-term Outcome

`.ghtmx` is an open source Go template engine, forked from `templ`, in which htmx is a first-class language concept rather than a set of string attributes. Concretely:

- **Route-aware bindings.** `hx-get`, `hx-post`, `hx-put`, `hx-patch`, and `hx-delete` reference Go handler symbols, not string URLs. The compiler resolves them against a route table derived from the application's Go source.
- **Typed, compile-checked `hx-*` attributes.** Invalid attribute names, values, and combinations are build errors.
- **Native fragment primitives.** The signature authoring journey is *fragment-of-a-page*: an author declares a fragment inside a page template, and it renders standalone on an htmx request and inline on a full page load — one template, two render modes, no duplicated markup.
- **Automatic htmx runtime wiring** and a **server-driven event contract** (`HX-Trigger`, `hx-on`) that is declared and type-checked in the template.

### Product Boundary

`.ghtmx` is a **template engine only** — unopinionated, bring-your-own-router. It generates plain Go and must work with whatever router, middleware stack, and project layout the user already has. It is **not** a full-stack framework and does not impose project structure.

### Compatibility Posture

A **hard fork of `templ` with intentional divergence**. Where htmx integration demands a different syntax, the fork breaks compatibility deliberately. `templ` source compatibility is a non-goal, and templ-to-ghtmx migration tooling is explicitly out of MVP scope.

### MVP Deliverables

- CLI: `generate`, `fmt`, `watch`
- Go runtime library (component interface, rendering, htmx helpers)
- LSP server plus editor extensions (completion, hover, diagnostics, go-to-definition)
- Dev server with live reload
- Documentation site with examples
- HTTP framework adapters (`net/http`, chi, echo, gin, fiber)

### Explicitly Out of MVP Scope

- templ-to-ghtmx migration tooling
- CSS / asset pipeline or bundling
- i18n / localization primitives

### MVP Success Criterion

A reference application (CRUD with htmx partial updates) builds and runs end-to-end with **zero hand-written htmx glue code**.

## Core Principles

These principles govern technical trade-offs. When they conflict with convenience, they win.

### P1 — Generated code is always valid, vetted, formatted Go

Every `.go` file emitted by the compiler MUST compile, MUST pass `go vet`, and MUST be `gofmt`-clean. The compiler MUST NOT emit code that breaks a user's build. If the compiler cannot produce valid output, it MUST fail with a diagnostic rather than write a broken file.

### P2 — No runtime surprises: htmx wiring errors fail at compile time

A route or fragment reference that does not resolve is a **build error**, never a runtime 404 or a silently empty swap. Any htmx binding the language accepts must be statically provable against the discovered route table.

### P3 — Auto-escaping by default; unsafe is explicit

All interpolated values are escaped by default. Bypassing escaping MUST require an explicit, visibly-named construct at the call site. There is no global switch that disables escaping.

### P4 — Errors are actionable

Every diagnostic MUST carry a precise `file:line:column` source position and, where the fix is determinable, a suggested correction. "Parse error" without a location is a defect.

### P5 — Deterministic, reproducible codegen

The same input MUST always produce byte-identical Go output, independent of map iteration order, filesystem ordering, timestamps, or host environment. Generated output is expected to be committed and diffed by users.

### P6 — Minimal, justified dependencies

The project MAY use the dependency set already proven by `templ`, plus additional dependencies where genuinely warranted. Every new dependency requires a stated justification. The default answer to "add a library" is no; the bar rises sharply for anything on the runtime path.

## Technology Constraints

### Required

- **Language:** Go. The compiler, runtime, LSP, and CLI are all written in Go.
- **Output target:** plain Go source code, consumable by the standard `go build` toolchain with no custom build step beyond `ghtmx generate`.
- **File extension:** `.ghtmx`.
- **Formatting/vetting:** generated and hand-written code is `gofmt`-formatted and `go vet`-clean.

### Dependency Policy

- The dependency baseline is **the set already used by `templ`** (notably `golang.org/x/tools` and `golang.org/x/mod` for Go source analysis, a parser-combinator library for the template grammar, a filesystem watcher for `watch` mode, and atomic-write and browser-launch helpers for the CLI and dev server).
- Additional dependencies MAY be added as needed, but the list MUST be kept minimal and each addition MUST be justified in review.
- Dependencies pulled in by tooling (CLI, LSP, dev server) MUST NOT leak into the runtime import graph of a user's application where avoidable.

### Disallowed

- No dependency on Node.js, npm, or any JavaScript build toolchain for the core workflow.
- No code generation that requires `cgo`.
- No runtime template parsing or interpretation — `.ghtmx` is a compiled engine; templates become Go code ahead of time.

## Architecture Constraints

### A1 — Single Go module monorepo

One repository, one `go.mod`, one version. The CLI (`cmd/ghtmx`), parser, generator, runtime package, LSP server, and framework adapters live together and ship together. Compiler and runtime versions are therefore always in lockstep.

### A2 — Classic multi-pass compiler pipeline

The compiler MUST maintain strict phase separation:

```
lex → parse → AST → analyze → emit
```

Each phase MUST be independently testable, with defined inputs and outputs. Phases MUST NOT reach across boundaries (e.g. the lexer does not resolve routes; the emitter does not reparse source).

### A3 — Route discovery is a separate static-analysis pass

Route-aware `hx-*` bindings are resolved by a **dedicated analysis pass over the application's Go source**. This pass scans Go handler code, builds a route table (verb + path pattern + handler symbol), and feeds that table into code generation. It is a distinct, separately testable stage — the template parser has no knowledge of Go route registration, and the code generator consumes a route table it did not build.

#### A3.1 — Route discovery MUST be syntax-only (bootstrap rule)

The route-discovery pass MUST operate in **syntax-only mode**: it loads Go source at the `NeedName | NeedFiles | NeedSyntax` level (AST and import declarations only) and MUST NOT request full type-checking of user packages.

This rule exists to break a load-bearing circular dependency. Application handlers import the generated components; the generated components cannot exist until the route table is extracted; a type-checking loader would therefore fail on any package whose generated files are missing or stale, taking P2 down with it. Syntax-only analysis reads route registrations without requiring the surrounding package to compile.

Binding consequences:

- **Route discovery MUST NOT require the user's project to compile.** A type error, a missing generated file, or a stale generated file anywhere in the module MUST NOT prevent the route table from being built.
- **Handler symbol identity is resolved by package-qualified identifier matching**, using the file's import declarations to map a selector expression (`handlers.GetUser`) to a package path plus symbol name. Full type resolution is unavailable by construction and MUST NOT be assumed by any consumer of the route table.
- **A clean-checkout first run MUST succeed.** `ghtmx generate` in a freshly cloned repository containing zero generated `.go` files MUST produce a complete route table and a complete, valid set of generated components in a single pass. No stub-emission or two-phase bootstrap step is permitted.
- **Statically unresolvable registrations are a build error** (per P2), not a silent omission. Where a route path or handler reference cannot be determined syntactically — a computed path string, a route registered through a dynamic indirection — the compiler MUST emit an actionable diagnostic (per P4) naming the registration site. An explicit, documented annotation MUST be provided so authors can declare such routes to the compiler rather than lose compile-time checking.

### A4 — A single shared AST is the contract for all tools

The parser, formatter (`ghtmx fmt`), and LSP server MUST all consume the **same AST**. Duplicate or divergent parser implementations are prohibited. A syntax change lands once, in one grammar, and is immediately visible to every tool.

### A5 — Engine, not framework

The generated code MUST NOT require a specific router, middleware stack, or directory layout. Framework adapters are thin, optional packages layered on top of a router-agnostic core.

## Testing Approaches

The following strategies are mandatory. A feature is not "done" until it is covered by the applicable ones.

### T1 — Golden-file codegen snapshot tests

Every language construct has a `.ghtmx` input paired with expected `.go` output. The full corpus is regenerated and diffed on every test run. Golden-file drift is a failing test, not a warning.

### T2 — Spec-driven development

The `.ghtmx` syntax specification is the source of truth for tests. **Every language feature MUST have a passing spec example before it can be merged.** Undocumented syntax is unsupported syntax.

### T3 — Fuzz testing the lexer and parser

The lexer and parser MUST NOT panic on arbitrary input. Fuzz corpora are maintained in-repo; any crash found becomes a permanent regression test case.

### T4 — Rendering conformance suite

Rendered output is compared against expected HTML, including escaping behaviour in every supported context and both render modes of a fragment-of-a-page (standalone vs. inline).

### T5 — Benchmarks with CI-enforced regression thresholds

Render throughput and compile time are benchmarked in CI against the in-repo baseline defined under Performance Targets. A regression beyond the recorded threshold fails the build.

## Coding Standards

- All Go code is `gofmt`-formatted and passes `go vet`; CI enforces both.
- Exported identifiers in the runtime and adapter packages MUST carry doc comments — the runtime API is a public contract for downstream users.
- Compiler internals live under `internal/` unless there is a deliberate decision to expose them; the public API surface is kept intentionally small.
- Errors are wrapped with context and carry source positions where they originate from user input (see P4). The compiler does not panic on user input — panics are reserved for genuine internal invariant violations.
- Public behaviour changes MUST be accompanied by a changelog entry (see stability contract).
- No exported symbol is added to the runtime without considering that it becomes a compatibility obligation.

## Security Constraints

### S1 — Context-aware output escaping is mandatory

Escaping MUST be selected based on the output context — HTML text, attribute values, URLs, JavaScript, and CSS each receive appropriate treatment. A single generic HTML-escape applied everywhere is insufficient and non-compliant.

#### S1.1 — Escaping contract for route-bound `hx-*` URLs

Route-aware bindings make `hx-get`, `hx-post`, `hx-put`, `hx-patch`, and `hx-delete` attribute values into **compiler-generated URLs**, which is the primary path by which application values reach a URL context. The contract is therefore fixed by the engine, not by the template author:

- Path and query parameters interpolated into a generated `hx-*` URL MUST be **URL-escaped by the engine**, using percent-encoding appropriate to the component being written (path segment vs. query value).
- The escaping context for htmx attribute values is **determined by the engine from the attribute's declared type**, and MUST NOT be selectable or overridable by the template author at the binding site.
- URL escaping composes with, and is applied before, the surrounding HTML attribute-value escaping. Neither may be skipped.

This clause defines the escaping contract only. Broader URL sanitization — scheme allow-lists, rejection of `javascript:`/`data:` URIs — remains out of scope for the MVP.

### S2 — CSRF-safe htmx request helpers

The runtime MUST provide first-class support for attaching CSRF tokens to htmx requests (via `hx-headers` or equivalent), with safe-by-default behaviour for state-changing verbs (`hx-post`, `hx-put`, `hx-patch`, `hx-delete`). Making a state-changing binding CSRF-safe MUST NOT require the author to hand-write header plumbing.

### S3 — Dependency vulnerability scanning in CI

`govulncheck` runs on every CI build. A known vulnerability in the dependency graph blocks release until resolved or explicitly accepted with a documented rationale.

## Performance Targets

These are binding and enforced via T5.

| Target | Threshold |
| --- | --- |
| **Full project regeneration** | Under **1 second** for a project of ~100 templates. Codegen must not become the slow part of the edit loop. |
| **Render throughput** | No more than **5% regression** against the recorded in-repo baseline (see below). |
| **LSP responsiveness** | Completion and diagnostics respond in under **100 ms** on a typical project. |

### Benchmark baseline and methodology

- A **named benchmark corpus is maintained in-repo** and is the sole input for all performance gates. It covers representative page, fragment, and route-binding workloads.
- At fork time, a **one-time reference measurement** is taken against a **specific pinned `templ` version** on that corpus. The pinned version and measured figures are recorded in-repo as the initial baseline.
- CI gates on the **recorded in-repo baseline only** — never on a live comparison against another project. This makes the gate deterministic and self-contained.
- The `templ` comparison remains a **documented benchmark**, published for transparency and re-run deliberately, but it is **not a build gate**.
- Baseline revisions require an explicit, reviewed commit recording the new figures and the justification.

Targets not currently binding (may be adopted later): end-to-end dev-loop latency and per-render allocation budgets.

## Integration Points

### htmx (the JavaScript library) — version compatibility contract

`.ghtmx` targets a **pinned htmx version**, declared explicitly in the documentation and validated by the runtime's script-inclusion helper. Because the compiler type-checks `hx-*` attributes against htmx's attribute surface, the supported htmx version is part of the project's public contract. An htmx major-version upgrade is a deliberate, documented, changelog-recorded change — never an implicit one.

### HTTP framework adapters

First-party adapter packages for **`net/http`, chi, echo, gin, and fiber**. Adapters are thin: they bridge the framework's handler and routing types into the route-discovery pass and the runtime's render entry points. Core functionality MUST NOT depend on any adapter, and an application using none of them must remain fully supported.

### Supporting integrations

The Go toolchain (`golang.org/x/tools` package loading for route discovery, `gofmt`, `go vet`) and the Language Server Protocol are foundational to the build and tooling architecture (see A3, A4, and MVP deliverables) rather than external systems the product integrates with.

## Stability and Versioning Contract

**Pre-1.0: breaking changes are allowed and changelog-driven.**

- Language syntax, generated-code shape, and runtime API MAY change between minor versions before v1.0.
- **Every** breaking change MUST be documented in the changelog with a migration note.
- The compiler and runtime ship from one module at one version (A1), so version-skew between generated code and runtime is structurally impossible.
- Deprecation grace periods, a strict SemVer commitment, and a runtime/compiler compatibility window are deferred until the v1.0 stabilization effort.

## License

**Apache License 2.0** — chosen for its **explicit patent grant** and defensive patent-termination clause, which MIT does not provide. Because `.ghtmx` generates code that is compiled into downstream applications, an express patent licence materially lowers adoption risk for organizations with a formal open source review process. The trade-off accepted is slightly higher compliance overhead (licence header retention and change notices) in exchange for that legal clarity. All contributions are accepted under Apache-2.0 on an inbound-equals-outbound basis.
