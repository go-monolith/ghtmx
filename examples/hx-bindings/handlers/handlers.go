// Package handlers holds the hx-bindings example's HTTP handlers.
//
// The templates bind these symbols (hx-get={ handlers.ListItems }),
// so this package must not import the template package back — the
// example installs the render bodies below from its own init instead.
package handlers

import "net/http"

// ListItemsBody and GetItemBody are installed by the example package
// so the handlers can render its templates without an import cycle.
var (
	ListItemsBody func(http.ResponseWriter, *http.Request)
	GetItemBody   func(http.ResponseWriter, *http.Request)
)

func ListItems(w http.ResponseWriter, r *http.Request) {
	if ListItemsBody != nil {
		ListItemsBody(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func GetItem(w http.ResponseWriter, r *http.Request) {
	if GetItemBody != nil {
		GetItemBody(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
}
