package site

import (
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/go-monolith/ghtmx"
	chiadapter "github.com/go-monolith/ghtmx/adapters/chi"
	"github.com/go-monolith/ghtmx/docs/official/ghtmxgen"
	"github.com/go-monolith/ghtmx/examples/crud"
	"github.com/go-monolith/ghtmx/examples/events"
	"github.com/go-monolith/ghtmx/examples/fragments"
	helloworld "github.com/go-monolith/ghtmx/examples/hello-world"
	hxbindings "github.com/go-monolith/ghtmx/examples/hx-bindings"
)

// NewRouter builds the site's chi router. Every route uses a named
// package-level handler: route discovery binds the templates' hx-*
// attributes against these registrations, and the handler names become
// the ghtmxgen constructor names.
func NewRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", Home)
	r.Get("/getting-started", GettingStarted)
	r.Get("/docs/syntax/{section}", SyntaxSection)
	r.Get("/docs/{slug}", DocPage)
	r.Get("/examples", ExamplesIndex)
	r.Get("/examples/{name}", ExampleDetail)

	// Live demos: the real example applications, compiled into this
	// binary and served at their native paths (route bindings bake
	// absolute URLs, so the demos cannot be remounted under a prefix).
	// The docs router matches first; crud's own "GET /{$}" is thereby
	// shadowed by Home — /todos serves crud's full page instead.
	// Known divergence: ServeMux.Handler reports no pattern on a
	// method mismatch, so those requests get this router's 404 where
	// the standalone example would answer 405 + Allow.
	demos := []*http.ServeMux{
		crud.Routes(),
		helloworld.Routes(),
		hxbindings.Routes(),
		fragments.Routes(),
		events.Routes(),
	}
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		for _, mux := range demos {
			if _, pattern := mux.Handler(req); pattern != "" {
				// Demo state is per visitor (see demostate.go).
				mux.ServeHTTP(w, withDemoSession(w, req))
				return
			}
		}
		http.NotFound(w, req)
	})
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
	pv := NewPageView("getting-started", "Getting started", body)
	render(w, r, chiadapter.WithPage(docPage(pv), docBodyFragment(pv)))
}

// DocPage serves one reference document and announces the view through
// the DocViewed event contract (headers must be set before the render
// writes the response). The syntax document renders as a category
// overview whose sections are individual sub-pages.
func DocPage(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	d, ok := DocBySlug(slug)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if ghtmx.IsHTMXRequest(r) {
		if err := ghtmxgen.EmitDocViewed(w, ghtmxgen.DocViewedPayload{Slug: slug}); err != nil {
			log.Printf("emit doc-viewed: %v", err)
		}
	}
	if slug == "syntax" {
		sections, intro, err := SyntaxSections()
		if err != nil {
			serverError(w, r, err)
			return
		}
		pv := NewPageView("syntax", d.Title, intro)
		render(w, r, chiadapter.WithPage(syntaxIndexPage(pv, sections), syntaxIndexBodyFragment(pv, sections)))
		return
	}
	body, err := DocHTML(d)
	if err != nil {
		serverError(w, r, err)
		return
	}
	pv := NewPageView(slug, d.Title, body)
	render(w, r, chiadapter.WithPage(docPage(pv), docBodyFragment(pv)))
}

// SyntaxSection serves one H2 slice of the syntax specification as
// its own page.
func SyntaxSection(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "section")
	s, ok := SyntaxSectionByID(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if ghtmx.IsHTMXRequest(r) {
		if err := ghtmxgen.EmitDocViewed(w, ghtmxgen.DocViewedPayload{Slug: "syntax/" + id}); err != nil {
			log.Printf("emit doc-viewed: %v", err)
		}
	}
	pv := NewPageView("syntax/"+id, s.Title, s.Body)
	pv.Category = "Syntax and usage"
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
