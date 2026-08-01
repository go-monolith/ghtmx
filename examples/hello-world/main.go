// The ghtmx walking skeleton: a minimal net/http application rendering a
// generated component, proving the full path from .ghtmx source to bytes in
// a browser (FR-090). It imports only the ghtmx runtime — no adapter and no
// compiler package (NFR-011, NFR-012).
//
// Run it with:
//
//	ghtmx generate && go run .
package main

import (
	"fmt"
	"net/http"
	"os"
)

func home(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "World"
	}
	if err := page(name).Render(r.Context(), w); err != nil {
		http.Error(w, "failed to render", http.StatusInternalServerError)
	}
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", home)

	addr := "127.0.0.1:8080"
	if v := os.Getenv("GHTMX_EXAMPLE_ADDR"); v != "" {
		addr = v
	}
	fmt.Printf("Listening on http://%s\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
