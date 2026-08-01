// Package handlers holds the hx-bindings example's HTTP handlers.
//
// The templates bind these symbols (hx-get={ handlers.ListItems }),
// so this package must not import the template package back — the
// example installs the render bodies below from its own init instead.
// Serving these handlers without importing the example package is a
// wiring bug, and they fail loudly rather than masking it with an
// empty 200.
package handlers

import "net/http"

// ListItemsBody and GetItemBody are installed by the example package
// so the handlers can render its templates without an import cycle.
var (
	ListItemsBody func(http.ResponseWriter, *http.Request)
	GetItemBody   func(http.ResponseWriter, *http.Request)
)

func ListItems(w http.ResponseWriter, r *http.Request) {
	if ListItemsBody == nil {
		http.Error(w, "hx-bindings: handlers.ListItemsBody hook is not installed — import the example package", http.StatusInternalServerError)
		return
	}
	ListItemsBody(w, r)
}

func GetItem(w http.ResponseWriter, r *http.Request) {
	if GetItemBody == nil {
		http.Error(w, "hx-bindings: handlers.GetItemBody hook is not installed — import the example package", http.StatusInternalServerError)
		return
	}
	GetItemBody(w, r)
}
