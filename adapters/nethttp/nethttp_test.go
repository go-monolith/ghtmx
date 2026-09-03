package nethttp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx"
)

// recorder is a bare ResponseWriter that records whether WriteHeader was
// called explicitly and snapshots the headers at that moment, so tests
// can prove both the implicit-200 default and the headers-before-status
// ordering. httptest.ResponseRecorder can distinguish neither.
type recorder struct {
	header      http.Header
	body        bytes.Buffer
	status      int
	explicit    bool
	statusCalls int
	atStatus    http.Header
}

func newRecorder() *recorder {
	return &recorder{header: http.Header{}}
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(p)
}

func (r *recorder) WriteHeader(code int) {
	r.statusCalls++
	if r.explicit {
		return
	}
	r.explicit = true
	r.status = code
	r.atStatus = r.header.Clone()
}

// pagedFixture renders distinct markup per mode so a test can tell which
// mode ran.
func pagedFixture() ghtmx.Fragment {
	page := ghtmx.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "<html>page</html>")
		return err
	})
	row := fixedFragment{markup: "<tr>row</tr>"}
	return WithPage(page, row)
}

// fixedFragment renders the same markup in both modes, like a generated
// standalone fragment.
type fixedFragment struct{ markup string }

func (f fixedFragment) Render(ctx context.Context, w io.Writer) error {
	_, err := io.WriteString(w, f.markup)
	return err
}

func (f fixedFragment) RenderFragment(ctx context.Context, w io.Writer) error {
	return f.Render(ctx, w)
}

// failingFragment returns the sentinel unchanged.
type failingFragment struct{ err error }

func (f failingFragment) Render(ctx context.Context, w io.Writer) error         { return f.err }
func (f failingFragment) RenderFragment(ctx context.Context, w io.Writer) error { return f.err }

func request(htmx string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/items", nil)
	if htmx != "" {
		r.Header.Set("HX-Request", htmx)
	}
	return r
}

// historyRestoreRequest is what htmx sends when it refetches a page for
// back/forward navigation: HX-Request plus HX-History-Restore-Request
// (whose literal value is the argument).
func historyRestoreRequest(value string) *http.Request {
	r := request("true")
	r.Header.Set("HX-History-Restore-Request", value)
	return r
}

// TestAutomaticModeSelection: FR-035 — an htmx request renders
// standalone, a normal request renders the full page, and only the
// literal HX-Request: true opts a request into the standalone mode.
// The one htmx request that gets the page is a history restore (the
// literal HX-History-Restore-Request: true), which htmx selects its
// [hx-history-elt] out of.
func TestAutomaticModeSelection(t *testing.T) {
	tests := []struct {
		name     string
		hxHeader string
		history  string // HX-History-Restore-Request value, if any
		opts     []Option
		want     string
	}{
		{name: "htmx request renders standalone", hxHeader: "true", want: "<tr>row</tr>"},
		{name: "plain request renders the full page", hxHeader: "", want: "<html>page</html>"},
		{name: "HX-Request false is not an htmx request", hxHeader: "false", want: "<html>page</html>"},
		{name: "ModeFull overrides an htmx request", hxHeader: "true", opts: []Option{Mode(ModeFull)}, want: "<html>page</html>"},
		{name: "ModeStandalone overrides a plain request", hxHeader: "", opts: []Option{Mode(ModeStandalone)}, want: "<tr>row</tr>"},
		// htmx selects [hx-history-elt] out of a history-restore
		// response (htmx 4 swaps nothing when it is missing), so the
		// restore gets the document that element lives in.
		{name: "history restore renders the full page", history: "true", want: "<html>page</html>"},
		{name: "HX-History-Restore-Request false is not a restore", history: "false", want: "<tr>row</tr>"},
		{name: "ModeStandalone overrides a history restore", history: "true", opts: []Option{Mode(ModeStandalone)}, want: "<tr>row</tr>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newRecorder()
			r := request(tt.hxHeader)
			if tt.history != "" {
				r = historyRestoreRequest(tt.history)
			}
			if err := Render(w, r, pagedFixture(), tt.opts...); err != nil {
				t.Fatalf("Render failed: %v", err)
			}
			if got := w.body.String(); got != tt.want {
				t.Errorf("rendered %q, want %q", got, tt.want)
			}
		})
	}
}

// TestStatusDefaultsAndOverride: the status defaults to 200 through the
// implicit first write; Status sets it explicitly, after the headers.
func TestStatusDefaultsAndOverride(t *testing.T) {
	w := newRecorder()
	if err := Render(w, request("true"), pagedFixture()); err != nil {
		t.Fatal(err)
	}
	if w.explicit || w.status != http.StatusOK {
		t.Errorf("default must be the implicit 200, got explicit=%v status=%d", w.explicit, w.status)
	}

	w = newRecorder()
	err := Render(w, request("true"), pagedFixture(),
		Status(http.StatusUnprocessableEntity), Retarget("#errors"))
	if err != nil {
		t.Fatal(err)
	}
	if !w.explicit || w.status != http.StatusUnprocessableEntity {
		t.Errorf("Status must write the given code, got explicit=%v status=%d", w.explicit, w.status)
	}
	if w.statusCalls != 1 {
		t.Errorf("WriteHeader must run exactly once, ran %d times", w.statusCalls)
	}
	// Headers set after WriteHeader are silently discarded by net/http,
	// so they must already be present when the status goes out.
	if w.atStatus.Get("HX-Retarget") != "#errors" {
		t.Error("htmx headers must be set before the status is written")
	}
	if !strings.HasPrefix(w.atStatus.Get("Content-Type"), "text/html") {
		t.Error("the Content-Type default must be set before the status is written")
	}
	if got := w.body.String(); got != "<tr>row</tr>" {
		t.Errorf("the body must still render after an explicit status, got %q", got)
	}
}

// TestHtmxHeaderOptions: the swap-shaping options set their headers on
// htmx requests and stay off plain requests, where htmx never reads
// them.
func TestHtmxHeaderOptions(t *testing.T) {
	opts := []Option{
		Retarget("#list"),
		Reswap(ghtmx.SwapOuterHTML, ghtmx.SwapTransition()),
		Reselect("#part"),
		PushURL("/items"),
		ReplaceURL("/replaced"),
	}
	want := map[string]string{
		"HX-Retarget":    "#list",
		"HX-Reswap":      "outerHTML transition:true",
		"HX-Reselect":    "#part",
		"HX-Push-Url":    "/items",
		"HX-Replace-Url": "/replaced",
	}

	w := newRecorder()
	if err := Render(w, request("true"), pagedFixture(), opts...); err != nil {
		t.Fatal(err)
	}
	for header, value := range want {
		if got := w.header.Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}

	w = newRecorder()
	if err := Render(w, request(""), pagedFixture(), opts...); err != nil {
		t.Fatal(err)
	}
	for header := range want {
		if got := w.header.Get(header); got != "" {
			t.Errorf("%s = %q on a plain request, want unset", header, got)
		}
	}

	// A Mode override changes what renders, never the header gating: an
	// htmx request keeps its headers under ModeFull, and a plain request
	// stays header-free under ModeStandalone.
	w = newRecorder()
	if err := Render(w, request("true"), pagedFixture(), Mode(ModeFull), Retarget("#list")); err != nil {
		t.Fatal(err)
	}
	if w.header.Get("HX-Retarget") != "#list" || w.body.String() != "<html>page</html>" {
		t.Errorf("ModeFull on an htmx request must keep headers and render the page, got %q %q",
			w.header.Get("HX-Retarget"), w.body.String())
	}
	w = newRecorder()
	if err := Render(w, request(""), pagedFixture(), Mode(ModeStandalone), Retarget("#list")); err != nil {
		t.Fatal(err)
	}
	if w.header.Get("HX-Retarget") != "" || w.body.String() != "<tr>row</tr>" {
		t.Errorf("ModeStandalone on a plain request must drop headers and render the fragment, got %q %q",
			w.header.Get("HX-Retarget"), w.body.String())
	}
}

// TestHistoryRestoreKeepsHtmxHeaders: a history restore is still an
// htmx request — it renders the page, but the htmx response headers
// it opted into are sent, unlike on a browser's own full-page load.
func TestHistoryRestoreKeepsHtmxHeaders(t *testing.T) {
	w := newRecorder()
	if err := Render(w, historyRestoreRequest("true"), pagedFixture(), Retarget("#list")); err != nil {
		t.Fatal(err)
	}
	if w.header.Get("HX-Retarget") != "#list" || w.body.String() != "<html>page</html>" {
		t.Errorf("history restore: HX-Retarget=%q body=%q, want the header and the full page",
			w.header.Get("HX-Retarget"), w.body.String())
	}
}

// TestURLHistoryDisabledOptions: the disabled variants send the literal
// false htmx expects.
func TestURLHistoryDisabledOptions(t *testing.T) {
	w := newRecorder()
	if err := Render(w, request("true"), pagedFixture(), PushURLDisabled(), ReplaceURLDisabled()); err != nil {
		t.Fatal(err)
	}
	if got := w.header.Get("HX-Push-Url"); got != "false" {
		t.Errorf("HX-Push-Url = %q, want false", got)
	}
	if got := w.header.Get("HX-Replace-Url"); got != "false" {
		t.Errorf("HX-Replace-Url = %q, want false", got)
	}
}

// TestContentTypeDefaultAndOverride: text/html is filled in only when
// the handler has not chosen a Content-Type itself.
func TestContentTypeDefaultAndOverride(t *testing.T) {
	w := newRecorder()
	if err := Render(w, request(""), pagedFixture()); err != nil {
		t.Fatal(err)
	}
	if got := w.header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want the text/html default", got)
	}

	w = newRecorder()
	w.header.Set("Content-Type", "application/xhtml+xml")
	if err := Render(w, request(""), pagedFixture()); err != nil {
		t.Fatal(err)
	}
	if got := w.header.Get("Content-Type"); got != "application/xhtml+xml" {
		t.Errorf("a handler-set Content-Type must be preserved, got %q", got)
	}
}

// TestRenderErrorReturnedUnchanged: the adapter adds nothing around the
// runtime's error.
func TestRenderErrorReturnedUnchanged(t *testing.T) {
	sentinel := errors.New("render exploded")
	err := Render(newRecorder(), request("true"), failingFragment{err: sentinel})
	// Identity, not errors.Is: wrapping would already violate the
	// adapters-return-errors-unchanged rule.
	if err != sentinel {
		t.Errorf("error must be returned unchanged, got %v", err)
	}
}

// TestWithPageRejectsNil: the nil footgun fails at construction, not
// mid-request.
func TestWithPageRejectsNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("WithPage(nil, nil) must panic at the construction site")
		}
	}()
	WithPage(nil, nil)
}

// TestWithPageModes: the pairing routes each mode to its component.
func TestWithPageModes(t *testing.T) {
	f := pagedFixture()
	ctx := context.Background()

	var full, standalone bytes.Buffer
	if err := f.Render(ctx, &full); err != nil {
		t.Fatal(err)
	}
	if err := f.RenderFragment(ctx, &standalone); err != nil {
		t.Fatal(err)
	}
	if full.String() != "<html>page</html>" || standalone.String() != "<tr>row</tr>" {
		t.Errorf("WithPage modes diverged: full=%q standalone=%q", full.String(), standalone.String())
	}
}

// TestGeneratedShapeFragment: a fragment whose modes agree (the shape
// the compiler generates) works through the adapter in both modes.
func TestGeneratedShapeFragment(t *testing.T) {
	for _, hx := range []string{"", "true"} {
		w := newRecorder()
		if err := Render(w, request(hx), fixedFragment{markup: "<tr>only</tr>"}); err != nil {
			t.Fatal(err)
		}
		if got := w.body.String(); got != "<tr>only</tr>" {
			t.Errorf("HX-Request=%q rendered %q", hx, got)
		}
	}
}
