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

// navRoutes is every route the sidebar and index pages link to,
// including one page per syntax section.
func navRoutes() []string {
	routes := []string{"/", "/getting-started", "/examples"}
	for _, d := range Docs {
		routes = append(routes, "/docs/"+d.Slug)
	}
	sections, _, err := SyntaxSections()
	if err != nil {
		panic(err)
	}
	for _, s := range sections {
		routes = append(routes, "/docs/syntax/"+s.ID)
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
		for _, marker := range []string{`class="sidebar"`, `class="navbar"`, `id="content"`, "hx-history-elt", "https://cdn.jsdelivr.net/npm/htmx.org@", "highlight.min.js"} {
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
		for _, forbidden := range []string{"<html", `class="sidebar"`, `class="navbar"`} {
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

// TestDocPageHasTOCAndPager: a reference document carries its "On
// this page" contents (from real headings) and reading-order pager.
func TestDocPageHasTOCAndPager(t *testing.T) {
	srv := serve(t)
	_, body := get(t, srv, "/docs/diagnostics", false)
	for _, marker := range []string{
		"On this page",
		`class="toc"`,
		`href="#errors"`,
		`href="#warnings"`,
		`class="pager"`,
		"Previous", "Next",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("/docs/diagnostics: missing %q", marker)
		}
	}
}

// TestSyntaxCategoryPages: the category overview lists every section
// as a sub-page, and a section page carries breadcrumbs back to the
// category plus its own pager.
func TestSyntaxCategoryPages(t *testing.T) {
	srv := serve(t)
	_, index := get(t, srv, "/docs/syntax", false)
	sections, _, err := SyntaxSections()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sections {
		if !strings.Contains(index, `href="/docs/syntax/`+s.ID+`"`) {
			t.Errorf("syntax overview missing link to section %s", s.ID)
		}
	}
	resp, section := get(t, srv, "/docs/syntax/attributes", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /docs/syntax/attributes = %d", resp.StatusCode)
	}
	for _, marker := range []string{
		`class="breadcrumbs"`,
		"Syntax and usage",
		"<h1>Attributes</h1>",
		`class="pager"`,
	} {
		if !strings.Contains(section, marker) {
			t.Errorf("/docs/syntax/attributes: missing %q", marker)
		}
	}
	resp, _ = get(t, srv, "/docs/syntax/nope", false)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /docs/syntax/nope = %d, want 404", resp.StatusCode)
	}
	// The sidebar sub-menu is expanded on section pages.
	if !strings.Contains(section, "<details class=\"sidebar-cat\" open") {
		t.Error("sidebar syntax category is not expanded on a section page")
	}
}

// TestHighlightScriptPinnedWithSRI: the highlighter loads from a
// pinned CDN asset whose subresource integrity the browser enforces.
// Bumping the CDN version without recomputing the hash must fail here
// instead of silently breaking highlighting in the browser.
func TestHighlightScriptPinnedWithSRI(t *testing.T) {
	srv := serve(t)
	_, body := get(t, srv, "/", false)
	want := `<script src="https://cdn.jsdelivr.net/gh/highlightjs/cdn-release@11.11.1/build/highlight.min.js"` +
		` integrity="sha384-RH2xi4eIQ/gjtbs9fUXM68sLSi99C7ZWBRX1vDrVv6GQXRibxXLbwO2NGZB74MbU"` +
		` crossorigin="anonymous" defer></script>`
	if !strings.Contains(body, want) {
		t.Errorf("full page missing the pinned, SRI-checked highlight.js tag:\n%s", want)
	}
}

// TestHistoryRestoreScopedToContent: htmx back/forward restores must
// stay inside #content — the page declares hx-history-elt there, and
// a history-restore request (HX-Request is set on those too) gets the
// bare fragment that belongs in it, never a nested full document.
func TestHistoryRestoreScopedToContent(t *testing.T) {
	srv := serve(t)
	_, page := get(t, srv, "/docs/syntax", false)
	if !strings.Contains(page, "hx-history-elt") {
		t.Error("full page does not scope htmx history with hx-history-elt")
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/docs/syntax", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-History-Restore-Request", "true")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "<html") {
		t.Error("history-restore response contains a full document; it is swapped into #content")
	}
}

// TestLiveDemosServed: every example's real router is compiled into
// the binary and serves at its native paths, and each example page
// links to its demo.
func TestLiveDemosServed(t *testing.T) {
	srv := serve(t)
	markers := map[string]string{
		"hello-world": "Hello",
		"hx-bindings": "hx-get",
		"fragments":   "hx-get",
		"events":      "hx-post",
		"crud":        "Todos",
	}
	for _, e := range Examples {
		if e.DemoPath == "" {
			t.Errorf("example %s has no demo path", e.Name)
			continue
		}
		marker, ok := markers[e.Name]
		if !ok {
			t.Errorf("example %s has no demo marker in this test — add one", e.Name)
			continue
		}
		resp, body := get(t, srv, e.DemoPath, false)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s (%s demo) = %d, want 200", e.DemoPath, e.Name, resp.StatusCode)
			continue
		}
		if !strings.Contains(body, marker) {
			t.Errorf("demo %s: missing marker %q", e.DemoPath, marker)
		}
		_, detail := get(t, srv, "/examples/"+e.Name, false)
		if !strings.Contains(detail, `href="`+e.DemoPath+`"`) {
			t.Errorf("example page %s does not link its live demo %s", e.Name, e.DemoPath)
		}
	}
	// The demo fallback must not swallow the docs' own 404s.
	resp, _ := get(t, srv, "/todos/does/not/exist", false)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /todos/does/not/exist = %d, want 404", resp.StatusCode)
	}
	// Documented divergence: a method mismatch on a demo path is 404
	// here (ServeMux.Handler reports no pattern for mismatches), where
	// the standalone example would answer 405 + Allow.
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	mm, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	mm.Body.Close()
	if mm.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE /hello = %d, want the documented 404", mm.StatusCode)
	}
}

// TestCrudDemoRoundTrip: the crud demo is actually alive — creating a
// todo through the real handler emits the contract event and returns
// the refreshed list fragment. The created todo intentionally leaks
// into the cookie-less "" demo session's store for the rest of the
// test binary; no other test asserts that session's contents.
func TestCrudDemoRoundTrip(t *testing.T) {
	srv := serve(t)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/todos", strings.NewReader("title=demo+todo"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /todos = %d, want 201", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("HX-Trigger"), "todo-created") {
		t.Errorf("HX-Trigger = %q, want todo-created", resp.Header.Get("HX-Trigger"))
	}
	if !strings.Contains(string(data), "demo todo") {
		t.Error("created todo missing from the returned list fragment")
	}
}

// TestDemoStateIsolatedPerSession: two visitors with different demo
// cookies get independent crud stores — one visitor's edits never
// appear in another's list — and a cookie-less first request is
// issued a session cookie.
func TestDemoStateIsolatedPerSession(t *testing.T) {
	srv := serve(t)
	demoReq := func(method, path, cookie string, form string) (*http.Response, string) {
		t.Helper()
		var body io.Reader
		if form != "" {
			body = strings.NewReader(form)
		}
		req, err := http.NewRequest(method, srv.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		if form != "" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req.Header.Set("HX-Request", "true")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "ghtmx_demo", Value: cookie})
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

	resp, _ := demoReq(http.MethodPost, "/todos", "visitor-a", "title=alice+private+task")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("visitor A create = %d", resp.StatusCode)
	}
	_, bList := demoReq(http.MethodGet, "/todos", "visitor-b", "")
	if strings.Contains(bList, "alice private task") {
		t.Error("visitor B sees visitor A's todo — demo state is shared across sessions")
	}
	if !strings.Contains(bList, `class="empty"`) {
		t.Error("visitor B's fresh session should be empty")
	}
	_, aList := demoReq(http.MethodGet, "/todos", "visitor-a", "")
	if !strings.Contains(aList, "alice private task") {
		t.Error("visitor A lost their own todo")
	}

	// A cookie-less demo request is minted a session.
	resp, _ = demoReq(http.MethodGet, "/todos", "", "")
	minted := false
	for _, c := range resp.Cookies() {
		if c.Name == "ghtmx_demo" && c.Value != "" {
			minted = true
		}
	}
	if !minted {
		t.Error("first demo request did not receive a ghtmx_demo session cookie")
	}
	// Docs pages themselves never set the demo cookie.
	resp, _ = get(t, srv, "/docs/syntax", false)
	for _, c := range resp.Cookies() {
		if c.Name == "ghtmx_demo" {
			t.Error("a docs page set the demo session cookie")
		}
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
