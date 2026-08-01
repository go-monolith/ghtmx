package ghtmx

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-monolith/ghtmx/internal/htmxsurface"
)

func TestIsHTMXRequest(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if IsHTMXRequest(r) {
		t.Error("a plain request is not an htmx request")
	}
	r.Header.Set("HX-Request", "true")
	if !IsHTMXRequest(r) {
		t.Error("HX-Request: true marks an htmx request")
	}
}

func TestTypedHeaderHelpers(t *testing.T) {
	tests := []struct {
		name   string
		set    func(w http.ResponseWriter)
		header string
		want   string
	}{
		{"retarget", func(w http.ResponseWriter) { SetRetarget(w, "#errors") }, "HX-Retarget", "#errors"},
		{"reswap style only", func(w http.ResponseWriter) { SetReswap(w, SwapOuterHTML) }, "HX-Reswap", "outerHTML"},
		{
			"reswap with modifiers",
			func(w http.ResponseWriter) {
				SetReswap(w, SwapInnerHTML, SwapTransition(), SwapDelay(250*time.Millisecond), SwapShow("#list:top"))
			},
			"HX-Reswap", "innerHTML transition:true swap:250ms show:#list:top",
		},
		{"reswap skips zero modifier", func(w http.ResponseWriter) { SetReswap(w, SwapNone, SwapModifier{}) }, "HX-Reswap", "none"},
		{"redirect", func(w http.ResponseWriter) { SetRedirect(w, "/login") }, "HX-Redirect", "/login"},
		{"reselect", func(w http.ResponseWriter) { SetReselect(w, "#part") }, "HX-Reselect", "#part"},
		{"push url", func(w http.ResponseWriter) { SetPushURL(w, "/items/1") }, "HX-Push-Url", "/items/1"},
		{"push url disabled", SetPushURLDisabled, "HX-Push-Url", "false"},
		{"replace url", func(w http.ResponseWriter) { SetReplaceURL(w, "/items") }, "HX-Replace-Url", "/items"},
		{"replace url disabled", SetReplaceURLDisabled, "HX-Replace-Url", "false"},
		{"refresh", SetRefresh, "HX-Refresh", "true"},
		{"location", func(w http.ResponseWriter) { SetLocation(w, "/next") }, "HX-Location", "/next"},
		{"settle and focus", func(w http.ResponseWriter) {
			SetReswap(w, SwapAfterEnd, SwapSettle(time.Second), SwapFocusScroll(false), SwapIgnoreTitle(), SwapScroll("bottom"))
		}, "HX-Reswap", "afterend settle:1000ms focus-scroll:false ignoreTitle:true scroll:bottom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.set(rec)
			if got := rec.Header().Get(tt.header); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

// TestSwapStylesMatchSurfaceData: the typed constants and the embedded
// surface for the pinned htmx version describe the same set — a surface
// update that adds or removes a style fails here until the constants
// follow.
func TestSwapStylesMatchSurfaceData(t *testing.T) {
	constants := []SwapStyle{
		SwapInnerHTML, SwapOuterHTML, SwapTextContent, SwapBeforeBegin,
		SwapAfterBegin, SwapBeforeEnd, SwapAfterEnd, SwapDelete, SwapNone,
	}
	surface, err := htmxsurface.ForVersion("2.0.10")
	if err != nil {
		t.Fatal(err)
	}
	styles := surface.SwapStyles()
	// Set comparison in both directions: count checks alone are blind to
	// duplicates masking a drift.
	have := map[string]bool{}
	for _, c := range constants {
		have[string(c)] = true
	}
	inSurface := map[string]bool{}
	for _, s := range styles {
		inSurface[s] = true
		if !have[s] {
			t.Errorf("surface style %q has no typed constant", s)
		}
	}
	for c := range have {
		if !inSurface[c] {
			t.Errorf("constant %q is not in the pinned surface", c)
		}
	}
}

// TestSwapModifiersMatchSurfaceData: every modifier of the pinned surface
// has a typed constructor here.
func TestSwapModifiersMatchSurfaceData(t *testing.T) {
	constructors := map[string]bool{
		"transition": true, "swap": true, "settle": true, "ignoreTitle": true,
		"scroll": true, "show": true, "focus-scroll": true,
	}
	surface, err := htmxsurface.ForVersion("2.0.10")
	if err != nil {
		t.Fatal(err)
	}
	names := surface.SwapModifierNames()
	inSurface := map[string]bool{}
	for _, name := range names {
		inSurface[name] = true
		if !constructors[name] {
			t.Errorf("surface swap modifier %q has no typed constructor", name)
		}
	}
	for name := range constructors {
		if !inSurface[name] {
			t.Errorf("constructor for %q has no surface modifier: remove it", name)
		}
	}
}

func TestSwapSpecModifiersRejectWhitespace(t *testing.T) {
	rec := httptest.NewRecorder()
	SetReswap(rec, SwapInnerHTML, SwapScroll("#a .b:top"), SwapShow(""))
	if got := rec.Header().Get("HX-Reswap"); got != "innerHTML" {
		t.Errorf("unrepresentable specs must contribute nothing, got %q", got)
	}
	rec = httptest.NewRecorder()
	SetReswap(rec, SwapInnerHTML, SwapDelay(-time.Second))
	if got := rec.Header().Get("HX-Reswap"); got != "innerHTML swap:0ms" {
		t.Errorf("negative durations clamp to zero, got %q", got)
	}
}
