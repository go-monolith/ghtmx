// The ghtmx walking skeleton: a minimal net/http application rendering a
// generated component, proving the full path from .ghtmx source to bytes in
// a browser (FR-090). It imports only the ghtmx runtime — no adapter and no
// compiler package (NFR-011, NFR-012).
//
// Run it with:
//
//	ghtmx generate && go run ./cmd
package helloworld

import (
	_ "embed"
	"net/http"

	"github.com/go-monolith/ghtmx"
)

//go:embed hello.css
var styleCSS string

// styleSheet inlines hello.css into the page head. The rules live in
// their own file so the template shows markup, not presentation.
func styleSheet() ghtmx.Component {
	return ghtmx.Raw("<style>" + styleCSS + "</style>")
}

func home(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "World"
	}
	if err := page(name).Render(r.Context(), w); err != nil {
		http.Error(w, "failed to render", http.StatusInternalServerError)
	}
}

// Routes builds the example's router; the official docs site mounts
// it as a live demo.
func Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", home)
	return mux
}
