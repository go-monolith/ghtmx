// The events example: event declarations in page.ghtmx generate the only
// emission symbols (ghtmxgen.EmitItemSaved, ghtmxgen.EmitCartCleared) —
// an undeclared event has no symbol and cannot compile (FR-037). The save
// handler emits both events in one response; they merge into a single
// HX-Trigger header.
package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/go-monolith/ghtmx/ghtmxgen"
)

// Item is a row of the demo table.
type Item struct {
	ID   string
	Name string
}

var eventItems = []Item{
	{ID: "1", Name: "Alpha"},
	{ID: "2", Name: "Beta"},
}

func eventsHome(w http.ResponseWriter, r *http.Request) {
	// The application supplies the token (from its session layer); the
	// engine only attaches it (FR-092).
	csrf := demoCSRFToken(r)
	if err := eventsPage(eventItems, csrf).Render(r.Context(), w); err != nil {
		http.Error(w, "failed to render", http.StatusInternalServerError)
	}
}

// saveItem emits two declared events in one response: the header carries
// both, merged into one correctly-serialized HX-Trigger value.
func saveItem(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, it := range eventItems {
		if it.ID != id {
			continue
		}
		if err := ghtmxgen.EmitItemSaved(w, ghtmxgen.ItemSavedPayload{Id: it.ID, Name: it.Name}); err != nil {
			http.Error(w, "failed to emit", http.StatusInternalServerError)
			return
		}
		if err := ghtmxgen.EmitCartCleared(w); err != nil {
			http.Error(w, "failed to emit", http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `<tr id="item-%s"><td>%s (saved)</td><td></td></tr>`, it.ID, it.Name)
		return
	}
	http.NotFound(w, r)
}

// demoCSRFToken stands in for the application's real session-bound token
// source.
func demoCSRFToken(r *http.Request) string {
	if c, err := r.Cookie("csrf"); err == nil {
		return c.Value
	}
	return "demo-token"
}

func eventsMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", eventsHome)
	mux.HandleFunc("POST /events/items/{id}", saveItem)
	return mux
}

func main() {
	addr := "127.0.0.1:8083"
	fmt.Printf("Listening on http://%s/events\n", addr)
	if err := http.ListenAndServe(addr, eventsMux()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
