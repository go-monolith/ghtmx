package htmx4query

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func query(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("QUERY", "/search", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	Routes().ServeHTTP(rec, req)
	return rec
}

// TestPageIsHtmx4: the example pins 4.0.0 in its own ghtmx.json, so
// the generated HTMXScript() serves that build, hx-query is bound
// against the QUERY route, and the morph swap the compiler validated
// against that pin is on the input.
func TestPageIsHtmx4(t *testing.T) {
	rec := httptest.NewRecorder()
	Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /search = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js"`,
		`integrity="sha384-`,
		`hx-query="/search"`,
		`hx-swap="innerMorph"`,
		`<meta name="htmx-config" content='{"morphIgnore": ["data-htmx-powered", "open"]}'>`,
		`hx-trigger="input changed delay:300ms, search"`,
		`<li id="pkg-net/http">`,
		"<style>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q:\n%s", want, body)
		}
	}
}

// TestQueryReadsItsBody: a QUERY request carries its parameters in the
// body; the handler parses them itself since net/http will not.
func TestQueryReadsItsBody(t *testing.T) {
	rec := query(t, "q=json")
	if rec.Code != http.StatusOK {
		t.Fatalf("QUERY /search = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<li id="pkg-encoding/json">`) {
		t.Errorf("results missing the json match:\n%s", body)
	}
	if strings.Contains(body, `pkg-fmt"`) || strings.Contains(body, "<html") {
		t.Errorf("results must be the bare matching list:\n%s", body)
	}
}

// TestQueryMatchesDocs: the search covers the one-line docs, not only
// the paths, and an empty query lists everything.
func TestQueryMatchesDocs(t *testing.T) {
	if body := query(t, "q=cancellation").Body.String(); !strings.Contains(body, `pkg-context"`) {
		t.Errorf("doc text did not match:\n%s", body)
	}
	body := query(t, "").Body.String()
	if strings.Count(body, "<li id=") != len(stdlib) {
		t.Errorf("empty query must list all %d packages:\n%s", len(stdlib), body)
	}
	if body := query(t, "q=zzz").Body.String(); !strings.Contains(body, "no packages match") {
		t.Errorf("no-match state missing:\n%s", body)
	}
}

// TestOnlyGetAndQueryAreRouted: GET answers with the page (the full
// list embedded, never the bare results), QUERY with the results, and
// any other method is refused — the QUERY registration does not make
// /search a POST endpoint.
func TestOnlyGetAndQueryAreRouted(t *testing.T) {
	rec := httptest.NewRecorder()
	Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("GET /search = %d, want the full page: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/search", strings.NewReader("q=fmt")))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /search = %d, want 405", rec.Code)
	}
}

// TestOversizedBodyIs413: the body cap is reported as such, not as a
// malformed body.
func TestOversizedBodyIs413(t *testing.T) {
	if rec := query(t, "q="+strings.Repeat("x", 5000)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized QUERY body = %d, want 413", rec.Code)
	}
}
