package htmx4inheritance

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPageIsHtmx4: the example pins 4.0.0 in its own ghtmx.json, so
// the generated HTMXScript() serves that build, and the markup uses the
// htmx 4 name modifiers the compiler validated against that pin.
func TestPageIsHtmx4(t *testing.T) {
	rec := httptest.NewRecorder()
	Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/inherit", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /inherit = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js"`,
		`integrity="sha384-`,
		`hx-target:inherited="#out"`,
		`hx-include:inherited="#team"`,
		`hx-include:inherited:append="#role"`,
		`hx-post="/inherit/assign"`,
		"<style>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q:\n%s", want, body)
		}
	}
}

// TestAssignEchoesWhatArrived: the handler is the same for every
// button; the response differs only by what the markup included.
func TestAssignEchoesWhatArrived(t *testing.T) {
	cases := []struct {
		name   string
		form   string
		want   []string
		absent []string
	}{
		{name: "team only", form: "team=core", want: []string{"<code>team</code> = core"}, absent: []string{"role"}},
		{name: "team and role", form: "team=core&role=admin", want: []string{"<code>team</code> = core", "<code>role</code> = admin"}},
		{name: "nothing", form: "", want: []string{"received no fields"}, absent: []string{"<li>"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/inherit/assign", strings.NewReader(tc.form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("HX-Request", "true")
			Routes().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("POST /inherit/assign = %d: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("response missing %q:\n%s", w, body)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(body, a) {
					t.Errorf("response must not contain %q:\n%s", a, body)
				}
			}
			if strings.Contains(body, "<html") {
				t.Errorf("assign must answer with the bare fragment, got a document:\n%s", body)
			}
		})
	}
}

// TestAssignEscapes: echoed values are interpolated, so markup in a
// field value arrives escaped.
func TestAssignEscapes(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/inherit/assign", strings.NewReader("team=%3Cscript%3Ealert(1)%3C%2Fscript%3E"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	Routes().ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Errorf("value was not escaped:\n%s", rec.Body.String())
	}
}
