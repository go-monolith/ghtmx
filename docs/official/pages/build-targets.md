# Supported build targets

## Go and operating systems

CI builds and tests every commit on Linux, macOS, and Windows with the
two most recent Go releases — `.github/workflows/ci.yml` is the
authoritative matrix. The runtime package is standard-library only,
enforced by an import-isolation gate on every build.

## htmx versions

The engine pins one htmx version per project and validates every
`hx-*` attribute against that version's surface.

| | |
| --- | --- |
| Default pinned version | **2.0.10** |
| Supported range | **2.0.0 – 2.0.10** (each with a pinned script asset and subresource-integrity hash) |
| Configure | `htmxVersion` in `ghtmx.json` or `-htmx-version` |

htmx 1.x is not supported. A version outside the range fails fast with
`GHTMX-E0502`; constructs newer than the pinned version report
`GHTMX-E0501`.

## WebAssembly (NFR-014)

An application importing the runtime, its generated components, and a
first-party router adapter is **guaranteed to compile** for:

- `GOOS=js GOARCH=wasm`
- `GOOS=wasip1 GOARCH=wasm`

The guarantee is enforced on every build: a fixture application
(runtime + generated components + the chi adapter) and every included
adapter compile for both targets in CI, so a dependency unavailable on
a WASM target is caught at the point of introduction.

### Scope limits

- **Compile-time guarantee only.** Runtime behavior on WASM depends on
  the host: `net/http` serving is unavailable under `js/wasm` (browser
  hosts have no listening sockets), and `wasip1` socket support
  depends on the WASI host. Rendering to any `io.Writer` works
  everywhere.
- **Adapter matrix.** `nethttp`, `chi`, `echo`, and `gin` compile for
  both targets. **`fiber` is excluded** on both: its fasthttp engine
  uses raw socket syscalls the WASM ports lack
  (`SOCK_NONBLOCK`/`SOCK_CLOEXEC` on js; `ForkLock` on wasip1).
  **`fiberv3` compiles for `js/wasm`** — its newer fasthttp carries the
  needed port — **but stays excluded on `wasip1`**, where fasthttp's
  tcplisten is excluded by build constraints. The exclusion record is
  self-honest and per-target — when an upstream gains a port, the
  matrix test demands the entry be updated.
- **Tooling is out of scope.** The `ghtmx` CLI, LSP, and dev server
  target the native platforms above, not WASM.

## Performance envelope

The regeneration gate holds ~100 templates under 1 second, and the
render-throughput gate holds allocation counts exactly stable against
a recorded baseline (`benchmarks/corpus/BASELINE.md`).
