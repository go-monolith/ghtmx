# Getting started

From install to a rendered fragment in five steps. (This walkthrough
is compiled and run by the repository's test suite — it cannot rot.)

## 1. Install

```sh
go install github.com/go-monolith/ghtmx/cmd/ghtmx@latest
go install golang.org/x/tools/gopls@latest   # embedded-Go support in the LSP
```

No Go toolchain, or you would rather take the release binary? On Linux,
macOS, and WSL, `scripts/install.sh` downloads it, checks it against
`checksums.txt`, and adds `gopls` when `go` is available:

```sh
curl -fsSL https://raw.githubusercontent.com/go-monolith/ghtmx/main/scripts/install.sh | bash
```

On VS Code that one line is also the editor setup: the script finds the
`code` CLI, and if the ghtmx extension is missing it asks before
installing the `.vsix` from the same release.

```text
Install the ghtmx VS Code extension (syntax highlighting, diagnostics, completion)? [y/N]
```

Nothing is installed unless you answer `y`, and a run with no terminal
to ask on skips the question. `--no-interactive` turns off every prompt,
`GHTMX_INSTALL_VSCODE=1` answers yes in advance, and
`GHTMX_SKIP_VSCODE=1` leaves the editor alone.

## 2. Create a module

```sh
mkdir hello && cd hello
go mod init example.com/hello
```

## 3. Write the app

Create `main.go` — routes are ordinary `net/http` registrations; the
route discoverer reads them at build time:

```go
package main

import (
	"log"
	"net/http"
)

func Index(w http.ResponseWriter, r *http.Request) {
	if err := page("world").Render(r.Context(), w); err != nil {
		log.Printf("render: %v", err)
	}
}

func Greeting(w http.ResponseWriter, r *http.Request) {
	if err := greetingFragment("htmx").RenderFragment(r.Context(), w); err != nil {
		log.Printf("render: %v", err)
	}
}

func routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", Index)
	mux.HandleFunc("GET /greeting", Greeting)
	return mux
}

func main() {
	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", routes()))
}
```

Create `page.ghtmx` — the button binds the `Greeting` handler by
symbol (the compiler folds its route path in statically), and the
greeting is a fragment with both an inline and a standalone entry
point:

```templ
package main

import "example.com/hello/ghtmxgen"

templ page(name string) {
	<!DOCTYPE html>
	<html>
		<head>
			@ghtmxgen.HTMXScript()
		</head>
		<body>
			<button hx-get={ Greeting } hx-target="#greeting" hx-swap="outerHTML">Greet</button>
			@greeting(name)
		</body>
	</html>
}

fragment greeting(name string) {
	<p id="greeting">Hello, { name }!</p>
}
```

## 4. Generate and tidy

```sh
ghtmx generate
go mod tidy
```

Generation writes `page_ghtmx.go` beside the template and the central
`ghtmxgen` package (the pinned htmx script helper, and — for
parameterised routes — typed URL constructors). If you rename
`Greeting` later, both the registration and the binding break the
build: that is the point.

Two expected notices on this first run: a version-check note about
ghtmx not being in `go.mod` yet (the tidy that follows fixes it), and
`GHTMX-W0104` for the `GET /` page route, which is navigated to rather
than bound — full-page routes always warn like this.

## 5. Run it — and render a fragment

```sh
go run .
```

A browser on `http://localhost:8080` shows the page; clicking
**Greet** swaps in the fragment. The same standalone fragment renders
directly:

```sh
curl -H "HX-Request: true" http://localhost:8080/greeting
```

```html
<p id="greeting">Hello, htmx!</p>
```

That response is byte-identical to the fragment embedded in the full
page — the compile-time fragment guarantee.

## Where next

- `ghtmx generate -watch -cmd 'go run .' -proxy http://localhost:8080`
  gives live reload with a dev proxy.
- The [Syntax](/docs/syntax) specification covers every construct;
  `examples/crud` in the repository is the full reference application
  with events, typed constructors, and inline editing.
