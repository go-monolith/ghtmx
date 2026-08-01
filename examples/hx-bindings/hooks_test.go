package hxbindings

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHooksInstalledByInit pins the other half of the wiring
// contract: importing this package installs the render hooks, so the
// handlers serve the real templates.
func TestHooksInstalledByInit(t *testing.T) {
	rec := httptest.NewRecorder()
	Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /items = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `hx-get="/items"`) || !strings.Contains(body, "<html>") {
		t.Errorf("installed hook must render the full bound page, got %q", body)
	}

	rec = httptest.NewRecorder()
	Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items/alpha", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "alpha") {
		t.Errorf("GET /items/alpha = %d %q, want the rendered detail", rec.Code, rec.Body.String())
	}
}
