package site

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewRouter())
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string, htmx bool) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(data)
}

// navRoutes is every route the sidebar and index pages link to.
func navRoutes() []string {
	routes := []string{"/", "/getting-started", "/examples"}
	for _, d := range Docs {
		routes = append(routes, "/docs/"+d.Slug)
	}
	for _, e := range Examples {
		routes = append(routes, "/examples/"+e.Name)
	}
	return routes
}

// TestEveryNavRouteFullPage: a browser navigation to any route gets a
// complete document — chrome, content region, and the pinned htmx
// script tag.
func TestEveryNavRouteFullPage(t *testing.T) {
	srv := serve(t)
	for _, path := range navRoutes() {
		resp, body := get(t, srv, path, false)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
			continue
		}
		for _, marker := range []string{"<nav>", `id="content"`, "https://cdn.jsdelivr.net/npm/htmx.org@"} {
			if !strings.Contains(body, marker) {
				t.Errorf("GET %s: full page missing %q", path, marker)
			}
		}
	}
}

// TestEveryNavRouteFragment: the same routes with HX-Request serve the
// bare fragment, and that fragment is byte-identical to its slice of
// the full page (the compile-time fragment guarantee, end to end).
func TestEveryNavRouteFragment(t *testing.T) {
	srv := serve(t)
	for _, path := range navRoutes() {
		resp, fragment := get(t, srv, path, true)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s (htmx) = %d, want 200", path, resp.StatusCode)
			continue
		}
		for _, forbidden := range []string{"<html", "<nav>"} {
			if strings.Contains(fragment, forbidden) {
				t.Errorf("GET %s (htmx): fragment contains %q — page chrome leaked", path, forbidden)
			}
		}
		_, page := get(t, srv, path, false)
		if !strings.Contains(page, fragment) {
			t.Errorf("GET %s (htmx): fragment is not a byte-identical slice of the full page", path)
		}
	}
}

// TestDocPagesContainMarkers: rendered reference documents carry
// content that could only come from their single-source files.
func TestDocPagesContainMarkers(t *testing.T) {
	srv := serve(t)
	cases := map[string]string{
		"/docs/syntax":      "Syntax Specification",
		"/docs/config":      "routeScope",
		"/docs/diagnostics": "GHTMX-",
		"/docs/overview":    "ghtmx",
		"/getting-started":  "go install",
	}
	for path, marker := range cases {
		resp, body := get(t, srv, path, false)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
			continue
		}
		if !strings.Contains(body, marker) {
			t.Errorf("GET %s: missing marker %q", path, marker)
		}
	}
}

// TestExamplesIndexListsAllExamples: the index links every example.
func TestExamplesIndexListsAllExamples(t *testing.T) {
	srv := serve(t)
	_, body := get(t, srv, "/examples", false)
	for _, e := range Examples {
		if !strings.Contains(body, `href="/examples/`+e.Name+`"`) {
			t.Errorf("examples index missing link to %s", e.Name)
		}
	}
}

// TestExampleDetailShowsSource: an example page shows its template
// source escaped, not interpreted as markup.
func TestExampleDetailShowsSource(t *testing.T) {
	srv := serve(t)
	_, body := get(t, srv, "/examples/crud", false)
	if !strings.Contains(body, "crud.ghtmx") {
		t.Error("example detail missing file name crud.ghtmx")
	}
	if !strings.Contains(body, "fragment todoList(todos []Todo)") {
		t.Error("example detail missing template source text")
	}
	if !strings.Contains(body, "&lt;table id=&#34;todo-list&#34;&gt;") {
		t.Error("example source markup is not escaped")
	}
}

// TestUnknownSlugs404: unknown documents and examples are not pages.
func TestUnknownSlugs404(t *testing.T) {
	srv := serve(t)
	for _, path := range []string{"/docs/nope", "/examples/nope"} {
		resp, _ := get(t, srv, path, false)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, resp.StatusCode)
		}
	}
}

// TestDocViewedEmitsTrigger: viewing a reference document announces
// the DocViewed event through the generated emitter.
func TestDocViewedEmitsTrigger(t *testing.T) {
	srv := serve(t)
	resp, _ := get(t, srv, "/docs/syntax", true)
	trigger := resp.Header.Get("HX-Trigger")
	if !strings.Contains(trigger, `"doc-viewed"`) || !strings.Contains(trigger, `"slug":"syntax"`) {
		t.Errorf("HX-Trigger = %q, want doc-viewed with slug syntax", trigger)
	}
}
