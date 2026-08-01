// The fragments example: one fragment declaration serves both render
// modes. Pages compose it inline via @ItemRow(...); the row's refresh link
// binds a typed route constructor whose handler renders only the fragment
// via ItemRowFragment(...).RenderFragment — no page around it (FR-031,
// FR-034).
package main

import (
	"fmt"
	"net/http"
	"os"
)

// Item is a row of the demo table.
type Item struct {
	ID   string
	Name string
}

var items = []Item{
	{ID: "1", Name: "Alpha"},
	{ID: "2", Name: "Beta"},
	{ID: "3", Name: "Gamma"},
}

func itemByID(id string) (Item, bool) {
	for _, it := range items {
		if it.ID == id {
			return it, true
		}
	}
	return Item{}, false
}

func fragmentsHome(w http.ResponseWriter, r *http.Request) {
	if err := listPage(items).Render(r.Context(), w); err != nil {
		http.Error(w, "failed to render", http.StatusInternalServerError)
	}
}

// rowDetail answers the row's hx-get swap with only the fragment's markup.
func rowDetail(w http.ResponseWriter, r *http.Request) {
	it, ok := itemByID(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := ItemRowFragment(it.ID, it.Name).RenderFragment(r.Context(), w); err != nil {
		http.Error(w, "failed to render", http.StatusInternalServerError)
	}
}

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /fragments", fragmentsHome)
	mux.HandleFunc("GET /fragments/rows/{id}", rowDetail)
	return mux
}

func main() {
	mux := newMux()

	addr := "127.0.0.1:8082"
	fmt.Printf("Listening on http://%s/fragments\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
