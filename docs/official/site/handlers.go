package site

import (
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/go-monolith/ghtmx"
	chiadapter "github.com/go-monolith/ghtmx/adapters/chi"
	"github.com/go-monolith/ghtmx/docs/official/ghtmxgen"
)

// NewRouter builds the site's chi router. Every route uses a named
// package-level handler: route discovery binds the templates' hx-*
// attributes against these registrations, and the handler names become
// the ghtmxgen constructor names.
func NewRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", Home)
	r.Get("/getting-started", GettingStarted)
	r.Get("/docs/{slug}", DocPage)
	r.Get("/examples", ExamplesIndex)
	r.Get("/examples/{name}", ExampleDetail)
	return r
}

// render serves the page/fragment pair: browsers get the full page,
// htmx requests get the bare fragment (FR-035 via the chi adapter).
// A render error may leave the response partially written, so it is
// logged rather than turned into http.Error.
func render(w http.ResponseWriter, r *http.Request, f ghtmx.Fragment, opts ...chiadapter.Option) {
	if err := chiadapter.Render(w, r, f, opts...); err != nil {
		log.Printf("render %s: %v", r.URL.Path, err)
	}
}

// Home serves the landing page.
func Home(w http.ResponseWriter, r *http.Request) {
	intro, err := PageHTML("index.md")
	if err != nil {
		serverError(w, r, err)
		return
	}
	render(w, r, chiadapter.WithPage(homePage(intro), homeBodyFragment(intro)))
}

// GettingStarted serves the install-to-first-fragment walkthrough.
func GettingStarted(w http.ResponseWriter, r *http.Request) {
	body, err := PageHTML("getting-started.md")
	if err != nil {
		serverError(w, r, err)
		return
	}
	pv := NewPageView("getting-started", body)
	render(w, r, chiadapter.WithPage(docPage(pv), docBodyFragment(pv)))
}

// DocPage serves one reference document and announces the view through
// the DocViewed event contract (headers must be set before the render
// writes the response).
func DocPage(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	d, ok := DocBySlug(slug)
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, err := DocHTML(d)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if ghtmx.IsHTMXRequest(r) {
		if err := ghtmxgen.EmitDocViewed(w, ghtmxgen.DocViewedPayload{Slug: slug}); err != nil {
			log.Printf("emit doc-viewed: %v", err)
		}
	}
	pv := NewPageView(slug, body)
	render(w, r, chiadapter.WithPage(docPage(pv), docBodyFragment(pv)))
}

// ExamplesIndex lists every example application.
func ExamplesIndex(w http.ResponseWriter, r *http.Request) {
	render(w, r, chiadapter.WithPage(examplesPage(Examples), examplesBodyFragment(Examples)))
}

// ExampleDetail shows one example's sources and README.
func ExampleDetail(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	e, ok := ExampleByName(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	files, readme, err := ExampleFiles(name)
	if err != nil {
		serverError(w, r, err)
		return
	}
	render(w, r, chiadapter.WithPage(examplePage(e, files, readme), exampleBodyFragment(e, files, readme)))
}

func serverError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("%s: %v", r.URL.Path, err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
