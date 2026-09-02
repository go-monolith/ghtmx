# ghtmx

A compiled Go template engine where htmx is a first-class,
compile-checked language concept — a hard fork of
[templ](https://github.com/a-h/templ) that keeps its syntax (see the
fork baseline) and adds three things:

- **Route-aware bindings** — `hx-post={ handlers.CreateUser }` and
  `hx-get={ ghtmxgen.GetUser(id) }` resolve at build time against your
  actual Go routes. No hand-written URL strings; renaming a route
  breaks the build at every binding site.
- **Compile-time fragments** — `fragment` blocks compile to one shared
  body with an inline entry point for full pages and a standalone
  entry point for htmx partial updates, byte-identical by
  construction.
- **A server-driven event contract** — `event` declarations generate
  the only symbols that can emit `HX-Trigger`, with typed payloads;
  undeclared events are Go compile errors.

Everything ships in one Go module with a stdlib-only runtime, a
build-time compiler with live-reload watch mode, an LSP with
route-aware completion, and editor extensions for VS Code, Neovim, and
JetBrains. Every `hx-*` attribute is checked against the htmx version
the project pins — any 2.0.x release, or htmx 4.0.0 with its explicit
inheritance, `hx-status`, and morph swaps — and a template that mixes
generations is reported with the replacement to use; see
[htmx versions](/docs/htmx-versions).

Start with [Getting started](/getting-started), then the
[Syntax](/docs/syntax) specification. The
[Diagnostics](/docs/diagnostics) catalogue explains every error the
toolchain can raise, [Configuration](/docs/config) covers `ghtmx.json`
and the CLI, and [Build targets](/docs/build-targets) states what is
supported where — including the WebAssembly guarantee.

## Stability (pre-1.0)

ghtmx is pre-1.0: breaking changes to language syntax, generated-code
shape, and the runtime API may land between minor versions. Every
breaking change carries a changelog entry with a migration note — a
build gate enforces the discipline. Pin a version and read the
changelog before upgrading.
