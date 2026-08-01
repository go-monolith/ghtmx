//go:build js && wasm

package main

import (
	"github.com/syumai/workers"

	"github.com/go-monolith/ghtmx/docs/official/site"
)

// The Cloudflare Workers entry point: workers.Serve bridges the
// Workers fetch event to the same chi router the native binary uses.
func main() {
	workers.Serve(site.NewRouter())
}
