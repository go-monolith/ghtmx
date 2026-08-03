package fragments

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPageEmbedsStyleSheet: the rules live in rows.css and reach the
// page through //go:embed. A missing file is a compile error, so what
// this guards is the wiring the compiler cannot see — a template that
// stops calling @styleSheet(), or a stylesheet emptied by a bad edit.
func TestPageEmbedsStyleSheet(t *testing.T) {
	var sb strings.Builder
	if err := listPage(items).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "<style>") || !strings.Contains(out, "</style>") {
		t.Errorf("the page must carry a style element, got:\n%s", out)
	}
	if !strings.Contains(out, ".app {") {
		t.Errorf("rows.css was not inlined into the page, got:\n%s", out)
	}
}

// TestInlineAndStandaloneByteIdentical: FR-031 — for identical inputs, the
// inline component entry point and the standalone fragment entry point
// produce byte-identical markup, because both are wrappers over the same
// generated body function.
func TestInlineAndStandaloneByteIdentical(t *testing.T) {
	ctx := context.Background()
	it := Item{ID: "1", Name: "Alpha"}

	var inline strings.Builder
	if err := ItemRow(it.ID, it.Name).Render(ctx, &inline); err != nil {
		t.Fatal(err)
	}
	var standalone strings.Builder
	if err := ItemRowFragment(it.ID, it.Name).RenderFragment(ctx, &standalone); err != nil {
		t.Fatal(err)
	}
	if inline.String() != standalone.String() {
		t.Errorf("render modes diverged:\ninline:     %q\nstandalone: %q", inline.String(), standalone.String())
	}
	if !strings.Contains(standalone.String(), `<tr id="item-1">`) {
		t.Errorf("fragment markup missing, got %q", standalone.String())
	}
}

// TestFragmentRenderAndRenderFragmentAgree: on a generated Fragment the
// two methods are equivalent — a standalone wrapper carries no page.
func TestFragmentRenderAndRenderFragmentAgree(t *testing.T) {
	ctx := context.Background()
	f := ItemRowFragment("2", "Beta")

	var a, b strings.Builder
	if err := f.Render(ctx, &a); err != nil {
		t.Fatal(err)
	}
	if err := f.RenderFragment(ctx, &b); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Errorf("Render and RenderFragment diverged:\n%q\n%q", a.String(), b.String())
	}
}

// TestStandaloneBytesAppearInsidePages: the standalone render is exactly
// the bytes each referencing page embeds for the same inputs, and it is
// independent of which page references the fragment (FR-031, FR-032).
func TestStandaloneBytesAppearInsidePages(t *testing.T) {
	ctx := context.Background()
	it := Item{ID: "2", Name: "Beta"}

	var fragment strings.Builder
	if err := ItemRowFragment(it.ID, it.Name).RenderFragment(ctx, &fragment); err != nil {
		t.Fatal(err)
	}

	var list, solo strings.Builder
	if err := listPage(items).Render(ctx, &list); err != nil {
		t.Fatal(err)
	}
	if err := soloPage(it).Render(ctx, &solo); err != nil {
		t.Fatal(err)
	}
	for name, page := range map[string]string{"listPage": list.String(), "soloPage": solo.String()} {
		if !strings.Contains(page, fragment.String()) {
			t.Errorf("%s must embed the standalone bytes verbatim:\nfragment: %q\npage:     %q", name, fragment.String(), page)
		}
	}
}

// TestRowBindsConstructorURL: the refresh link's hx-get is produced by the
// generated typed route constructor for GET /fragments/rows/{id}.
func TestRowBindsConstructorURL(t *testing.T) {
	var sb strings.Builder
	if err := ItemRow("3", "Gamma").Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), `hx-get="/fragments/rows/3"`) {
		t.Errorf("expected the constructor URL, got %q", sb.String())
	}
}

// bareResponseWriter records exactly what handler and engine do to the
// ResponseWriter, with none of httptest.ResponseRecorder's transport
// simulation (Content-Type sniffing, implicit WriteHeader on first Write).
type bareResponseWriter struct {
	header      http.Header
	body        strings.Builder
	wroteHeader bool
	flushCalls  int
}

func (w *bareResponseWriter) Flush() { w.flushCalls++ }

func (w *bareResponseWriter) Header() http.Header { return w.header }

func (w *bareResponseWriter) Write(p []byte) (int, error) { return w.body.Write(p) }

func (w *bareResponseWriter) WriteHeader(int) { w.wroteHeader = true }

// TestHandlerRendersFragmentStandalone: FR-034, FR-090 — a plain net/http
// handler renders the fragment's standalone entry point directly against
// the http.ResponseWriter. This test imports no adapter: on the explicit
// path neither the engine nor the handler writes a status code or any
// response header — the body is exactly the standalone render.
func TestHandlerRendersFragmentStandalone(t *testing.T) {
	rec := &bareResponseWriter{header: http.Header{}}
	req := httptest.NewRequest(http.MethodGet, "/fragments/rows/2", nil)
	Routes().ServeHTTP(rec, req)

	if rec.wroteHeader {
		t.Error("no explicit status code may be written on the handler-explicit path")
	}
	if len(rec.header) != 0 {
		t.Errorf("no unrequested response headers may be set, got %v", rec.header)
	}
	if rec.flushCalls != 0 {
		t.Errorf("the engine must not flush the ResponseWriter (a flush commits headers and forces chunked encoding), got %d Flush() calls", rec.flushCalls)
	}
	var want strings.Builder
	if err := ItemRowFragment("2", "Beta").RenderFragment(context.Background(), &want); err != nil {
		t.Fatal(err)
	}
	if rec.body.String() != want.String() {
		t.Errorf("body must be exactly the standalone render:\ngot:  %q\nwant: %q", rec.body.String(), want.String())
	}
}

// TestHandlerKeepsControlOfStatusAndHeaders: the handler-explicit path
// leaves status and headers to the handler — here the not-found branch.
func TestHandlerKeepsControlOfStatusAndHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/fragments/rows/does-not-exist", nil)
	Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("the handler's own status must reach the client, got %d", rec.Code)
	}
}

// TestCanceledContextStopsStandaloneRender: the generated body checks the
// context before writing, so a canceled request produces the error and no
// partial output.
func TestCanceledContextStopsStandaloneRender(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var sb strings.Builder
	err := ItemRowFragment("1", "Alpha").RenderFragment(ctx, &sb)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if sb.String() != "" {
		t.Errorf("no bytes may be written after cancellation, got %q", sb.String())
	}
}
