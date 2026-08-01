//go:build !js

// The official ghtmx documentation site: a chi-routed, ghtmx-templated
// server over the repository's single-source documents and examples,
// compiled either natively (this entry point) or to WebAssembly for
// Cloudflare Workers (main_wasm.go).
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/go-monolith/ghtmx/docs/official/site"
)

func main() {
	srv := &http.Server{
		Addr:              "127.0.0.1:8090",
		Handler:           site.NewRouter(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Println("ghtmx docs on http://127.0.0.1:8090")
	log.Fatal(srv.ListenAndServe())
}
