// The htmx4-query example: htmx 4 adds the QUERY method — safe and
// idempotent like GET, with the parameters in the body like POST — and
// morph swaps that update a region in place instead of replacing it. A
// search box issues QUERY requests as you type (hx-query, the sixth
// route-bindable verb) and morphs the result list: entries present in
// both responses keep their DOM nodes (only newcomers run the appear
// animation), and with "open" in htmx.config.morphIgnore the <details>
// a visitor expanded stay expanded while the list around them changes.
// The compiler binds hx-query against the "QUERY /search" registration
// and validates innerMorph against the 4.0.0 pin in ghtmx.json.
//
// Run it with:
//
//	ghtmx generate && go run ./cmd
package htmx4query

import (
	_ "embed"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-monolith/ghtmx"
)

//go:embed query.css
var styleCSS string

// styleSheet inlines query.css into the page head. The rules live in
// their own file so the template shows markup, not presentation.
func styleSheet() ghtmx.Component {
	return ghtmx.Raw("<style>" + styleCSS + "</style>")
}

// Package is one searchable entry: a Go standard-library package.
type Package struct {
	Path string
	Doc  string
}

// stdlib is the static data the search runs over.
var stdlib = []Package{
	{"bufio", "Buffered I/O: wraps io.Reader and io.Writer with buffering and line scanning."},
	{"bytes", "Functions for manipulating byte slices, mirroring the strings package."},
	{"context", "Deadlines, cancellation signals, and request-scoped values across API boundaries."},
	{"encoding/json", "Encoding and decoding of JSON as defined in RFC 7159."},
	{"errors", "Functions to manipulate errors: New, Is, As, Unwrap, and Join."},
	{"fmt", "Formatted I/O with functions analogous to C's printf and scanf."},
	{"io", "Basic interfaces to I/O primitives: Reader, Writer, and their combinators."},
	{"net/http", "HTTP client and server implementations."},
	{"os", "A platform-independent interface to operating system functionality."},
	{"sort", "Primitives for sorting slices and user-defined collections."},
	{"strings", "Functions for manipulating UTF-8 encoded strings."},
	{"sync", "Basic synchronization primitives such as mutual exclusion locks."},
	{"testing", "Support for automated testing of Go packages."},
	{"time", "Functionality for measuring and displaying time."},
}

// matches returns the packages whose path or doc contains q, all of
// them when q is empty.
func matches(q string) []Package {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return stdlib
	}
	var out []Package
	for _, p := range stdlib {
		if strings.Contains(p.Path, q) || strings.Contains(strings.ToLower(p.Doc), q) {
			out = append(out, p)
		}
	}
	return out
}

func searchHome(w http.ResponseWriter, r *http.Request) {
	if err := page(stdlib).Render(r.Context(), w); err != nil {
		http.Error(w, "failed to render", http.StatusInternalServerError)
	}
}

// Search answers a QUERY request. Its parameters travel in the body,
// form-encoded like a POST, but net/http parses bodies only for POST,
// PUT, and PATCH — so the handler reads and parses the body itself.
func Search(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	params, err := url.ParseQuery(string(raw))
	if err != nil {
		http.Error(w, "bad query", http.StatusBadRequest)
		return
	}
	if err := resultsFragment(matches(params.Get("q"))).RenderFragment(r.Context(), w); err != nil {
		http.Error(w, "failed to render", http.StatusInternalServerError)
	}
}

// Routes builds the example's router; the official docs site mounts
// it as a live demo.
func Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /search", searchHome)
	mux.HandleFunc("QUERY /search", Search)
	return mux
}
