package helloworld

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRenderedPage seeds the fixture-application corpus: the generated
// component renders through a plain net/http handler with no adapter.
func TestRenderedPage(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/hello", nil)
	if err := page("Skeleton").Render(r.Context(), w); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<h1>Hello, Skeleton!</h1>") {
		t.Errorf("expected greeting in output, got:\n%s", body)
	}
	if !strings.Contains(strings.ToLower(body), "<!doctype html>") {
		t.Errorf("expected a full document, got:\n%s", body)
	}
}

// TestPageEmbedsStyleSheet: the rules live in hello.css and reach the
// page through //go:embed. A missing file is a compile error, so what
// this guards is the wiring the compiler cannot see — a template that
// stops calling @styleSheet(), or a stylesheet emptied by a bad edit.
func TestPageEmbedsStyleSheet(t *testing.T) {
	var sb strings.Builder
	if err := page("World").Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "<style>") || !strings.Contains(out, "</style>") {
		t.Errorf("the page must carry a style element, got:\n%s", out)
	}
	if !strings.Contains(out, ".app {") {
		t.Errorf("hello.css was not inlined into the page, got:\n%s", out)
	}
}

// TestEscaping proves interpolated values are escaped by default (FR-002).
func TestEscaping(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/hello", nil)
	if err := page("<script>alert(1)</script>").Render(r.Context(), w); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	body := w.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("interpolated value was not escaped")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("expected escaped output, got:\n%s", body)
	}
}

// TestPageIncludesConfiguredHtmxScript: FR-091 — the generated
// ghtmxgen.HTMXScript() bakes the configured version into the tag.
func TestPageIncludesConfiguredHtmxScript(t *testing.T) {
	var sb strings.Builder
	if err := page("World").Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, `src="https://cdn.jsdelivr.net/npm/htmx.org@2.0.10/dist/htmx.min.js"`) {
		t.Errorf("the tag must reference the configured version, got:\n%s", out)
	}
	if !strings.Contains(out, `integrity="sha384-`) {
		t.Errorf("the tag must pin the asset via SRI, got:\n%s", out)
	}
}
