// The hx-bindings example exercises route-aware hx-* bindings end to end:
// a symbol binding folds a registered path into static markup, and a typed
// route constructor substitutes URL-escaped parameters (FR-020, FR-021,
// FR-023). Its test is the mandatory route-binding case of the escaping
// conformance suite (NFR-007).
package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/go-monolith/ghtmx/examples/hx-bindings/handlers"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", handlers.ListItems)
	// The GetItem constructor this registration generates is also the
	// conformance suite's mandatory FR-023 route-binding case
	// (conformance/conformance.ghtmx) — rename in both places.
	mux.HandleFunc("GET /items/{id}", handlers.GetItem)

	addr := "127.0.0.1:8081"
	fmt.Printf("Listening on http://%s\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
