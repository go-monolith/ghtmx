# Official ghtmx documentation site

The documentation site for ghtmx, built with ghtmx: chi routes, `.ghtmx`
templates with route-aware bindings, compile-time fragments (every page
works as a full document and as an htmx partial via `WithPage`), and the
`DocViewed` event contract. All reference documents and example sources
are embedded with `embed.FS`, so the compiled binary is the whole site.

This directory is its own Go module so it can depend on chi and the chi
adapter (the root module is gate-checked to stay framework-free).

## Content copies

Everything under `content/` is a committed copy of a repository
single-source document or example, maintained by:

```sh
go run ./internal/sync
```

`internal/sync/drift_test.go` fails CI whenever a copy is stale, so the
site cannot drift from `SYNTAX.md` and friends. Example `.go`/`.ghtmx`
copies carry a `.txt` suffix so the Go toolchain, gofmt, and
`ghtmx generate` never treat them as sources.

## Develop

```sh
# regenerate templates after editing site/*.ghtmx (from the repo root)
go run ./cmd/ghtmx generate -path docs/official

# run natively
go run .        # http://127.0.0.1:8090

# test
go test ./...
```

## Build for WebAssembly

```sh
GOOS=js GOARCH=wasm go build -ldflags="-s -w" -trimpath -o build/app.wasm .
```

The same binary logic serves both targets: `main_native.go` uses
`net/http`, `main_wasm.go` hands the identical chi router to
[`syumai/workers`](https://github.com/syumai/workers) for Cloudflare
Workers.

## Deploy to Cloudflare Workers

Deployment uses wrangler (Node tooling — deploy-only; the Go build and
CI never invoke it):

```sh
npx wrangler dev      # local Workers emulation
npx wrangler deploy
```

`wrangler.toml`'s build command generates the JS shim
(`workers-assets-gen`) and compiles the wasm. Mind the Workers size
limits (3 MB gzipped free tier, 10 MB paid): this stdlib-heavy binary
gzips to roughly 3 MB. Other wasm hosts work the same way — swap
`workers.Serve` for the host's `http.Handler` bridge (e.g. Spin's
`spinhttp.Handle`).

The htmx script itself is served by `@ghtmxgen.HTMXScript()` — the
pinned-version CDN tag with SRI. To self-host it instead, pass
`ghtmx.WithScriptSrc` at the call site.
