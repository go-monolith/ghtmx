package htmx4status

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func post(t *testing.T, form string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	Routes().ServeHTTP(rec, req)
	return rec
}

// TestPageIsHtmx4: the example pins 4.0.0 in its own ghtmx.json, so
// the generated HTMXScript() serves that build, and the form carries
// the htmx 4 status routing the compiler validated against that pin.
func TestPageIsHtmx4(t *testing.T) {
	rec := httptest.NewRecorder()
	Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/signup", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /signup = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js"`,
		`integrity="sha384-`,
		`hx-post="/signup"`,
		`hx-status:422="target:#errors"`,
		`hx-status:5xx="swap:none"`,
		`hx-disable="find button"`,
		`id="errors"`, `id="result"`, `id="last-signup"`,
		"<style>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q:\n%s", want, body)
		}
	}
}

// TestInvalidSignupIs422: the validation problems travel as a 422 whose
// body is the error list — the status the markup routes to #errors.
func TestInvalidSignupIs422(t *testing.T) {
	rec := post(t, "email=nope&handle=two+words")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid signup = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`<ul class="errors">`, "email needs an @", "handle cannot contain spaces"} {
		if !strings.Contains(body, want) {
			t.Errorf("422 body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "hx-partial") || strings.Contains(body, "<html") {
		t.Errorf("422 body must be the bare error list:\n%s", body)
	}
}

// TestValidSignupCarriesPartials: the 200 body is the welcome for
// #result plus <hx-partial> elements that update #errors and
// #last-signup from the same response.
func TestValidSignupCarriesPartials(t *testing.T) {
	rec := post(t, "email=ada%40example.com&handle=ada")
	if rec.Code != http.StatusOK {
		t.Fatalf("valid signup = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Welcome, <strong>ada</strong>",
		"ada@example.com",
		`<hx-partial hx-target="#errors" hx-swap="innerHTML">`,
		`<hx-partial hx-target="#last-signup" hx-swap="innerHTML">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("200 body missing %q:\n%s", want, body)
		}
	}
}

// TestOutageIs500: the simulated outage answers 500 with a body the
// markup's hx-status:5xx="swap:none" keeps off the page.
func TestOutageIs500(t *testing.T) {
	rec := post(t, "email=ada%40example.com&handle=ada&outage=on")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("outage = %d, want 500: %s", rec.Code, rec.Body.String())
	}
}

// TestEscaping: the handle and email are interpolated, so markup in
// them arrives escaped in both the welcome and the partial.
func TestEscaping(t *testing.T) {
	rec := post(t, "email=a%40b&handle=%3Cb%3Ex%3C%2Fb%3E")
	if strings.Contains(rec.Body.String(), "<b>x</b>") {
		t.Errorf("handle was not escaped:\n%s", rec.Body.String())
	}
}
