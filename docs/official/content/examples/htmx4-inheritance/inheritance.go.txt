// The htmx4-inheritance example: htmx 4 inherits no attribute unless the
// ancestor says so. A panel declares the swap target and the included
// field once with :inherited, a nested group extends the included
// fields with :inherited:append, and the buttons beneath carry nothing
// but their bound hx-post. The compiler validates the modifiers against
// the 4.0.0 pin in ghtmx.json and reports a wrapper that silently lost
// its reach (GHTMX-W0202) when :inherited is dropped.
//
// Run it with:
//
//	ghtmx generate && go run ./cmd
package htmx4inheritance

import (
	_ "embed"
	"net/http"
	"sort"

	"github.com/go-monolith/ghtmx"
)

//go:embed inheritance.css
var styleCSS string

// styleSheet inlines inheritance.css into the page head. The rules live
// in their own file so the template shows markup, not presentation.
func styleSheet() ghtmx.Component {
	return ghtmx.Raw("<style>" + styleCSS + "</style>")
}

// Field is one form value the assign handler received.
type Field struct {
	Name  string
	Value string
}

func inheritHome(w http.ResponseWriter, r *http.Request) {
	if err := page().Render(r.Context(), w); err != nil {
		http.Error(w, "failed to render", http.StatusInternalServerError)
	}
}

// Assign echoes which fields arrived: the same handler serves every
// button, so the differences between responses are entirely what the
// markup around each button chose to include.
func Assign(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	fields := make([]Field, 0, len(r.PostForm))
	for name, values := range r.PostForm {
		if len(values) > 0 {
			fields = append(fields, Field{Name: name, Value: values[0]})
		}
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
	if err := receivedFragment(fields).RenderFragment(r.Context(), w); err != nil {
		http.Error(w, "failed to render", http.StatusInternalServerError)
	}
}

// Routes builds the example's router; the official docs site mounts
// it as a live demo.
func Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /inherit", inheritHome)
	mux.HandleFunc("POST /inherit/assign", Assign)
	return mux
}
