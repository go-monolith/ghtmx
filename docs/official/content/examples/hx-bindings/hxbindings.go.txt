// The hx-bindings example exercises route-aware hx-* bindings end to end:
// a symbol binding folds a registered path into static markup, and a typed
// route constructor substitutes URL-escaped parameters (FR-020, FR-021,
// FR-023). Its test is the mandatory route-binding case of the escaping
// conformance suite (NFR-007).
package hxbindings

import (
	_ "embed"
	"log"
	"net/http"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/examples/hx-bindings/handlers"
)

//go:embed items.css
var styleCSS string

// styleSheet inlines items.css into the page head. The rules live in
// their own file so the template shows markup, not presentation.
func styleSheet() ghtmx.Component {
	return ghtmx.Raw("<style>" + styleCSS + "</style>")
}

// demoItems is the in-memory data the live demo lists.
var demoItems = []string{"alpha", "beta", "gamma", "a/b?c#d"}

// The handlers package cannot import this one back (the templates
// bind its symbols), so the render bodies install here.
func init() {
	handlers.ListItemsBody = func(w http.ResponseWriter, r *http.Request) {
		view := itemsPage(demoItems)
		if ghtmx.IsHTMXRequest(r) {
			view = itemList(demoItems)
		}
		if err := view.Render(r.Context(), w); err != nil {
			log.Printf("render items: %v", err)
		}
	}
	handlers.GetItemBody = func(w http.ResponseWriter, r *http.Request) {
		if err := itemDetail(r.PathValue("id")).Render(r.Context(), w); err != nil {
			log.Printf("render item detail: %v", err)
		}
	}
}

// Routes builds the example's router; the official docs site mounts
// it as a live demo.
func Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", handlers.ListItems)
	// The GetItem constructor this registration generates is also the
	// conformance suite's mandatory FR-023 route-binding case
	// (conformance/conformance.ghtmx) — rename in both places.
	mux.HandleFunc("GET /items/{id}", handlers.GetItem)
	return mux
}
