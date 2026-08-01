// The NFR-014 WASM fixture: a real application shape — the ghtmx
// runtime, generated components with route bindings through ghtmxgen,
// and the chi adapter — that must compile for GOOS=js GOARCH=wasm and
// GOOS=wasip1 GOARCH=wasm. internal/wasmcheck builds it for both
// targets on every test run.
package main

import (
	"log"
	"net/http"

	chirouter "github.com/go-chi/chi/v5"

	"github.com/go-monolith/ghtmx"
	chiadapter "github.com/go-monolith/ghtmx/adapters/chi"
	"github.com/go-monolith/ghtmx/benchmarks/corpus"
	ghtmxruntime "github.com/go-monolith/ghtmx/runtime"
)

// The NFR-014 triple is imported explicitly — runtime (root and
// generated-code plumbing), generated components, adapter — so a
// refactor of the transitive graph cannot silently drop a piece from
// the WASM matrix.
func main() {
	r := chirouter.NewRouter()
	items := corpus.MakeItems(3)
	r.Get("/items", func(w http.ResponseWriter, req *http.Request) {
		if ghtmx.IsHTMXRequest(req) {
			if err := ghtmxruntime.AppendTrigger(w, "wasm-ready", nil); err != nil {
				log.Printf("trigger: %v", err)
			}
		}
		page := corpus.PageWorkload("Catalogue", items)
		row := corpus.BenchRowFragment(items[0])
		if err := chiadapter.Render(w, req, chiadapter.WithPage(page, row)); err != nil {
			log.Printf("render /items: %v", err)
		}
	})
	r.Get("/bindings", func(w http.ResponseWriter, req *http.Request) {
		if err := corpus.BindingWorkload(items).Render(req.Context(), w); err != nil {
			log.Printf("render /bindings: %v", err)
		}
	})
	log.Fatal(http.ListenAndServe(":8080", r))
}
