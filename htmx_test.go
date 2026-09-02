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

// familySurfaces resolves the newest version of every embedded family.
func familySurfaces(t *testing.T) []*htmxsurface.Surface {
	t.Helper()
	var out []*htmxsurface.Surface
	for _, f := range htmxsurface.Families() {
		s, err := htmxsurface.ForVersion(f.Versions[len(f.Versions)-1])
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// TestSwapStylesMatchSurfaceData: the typed constants and the embedded
// surfaces describe the same set — every style (or alias) of every family
// has a constant, and every constant is a style of at least one family. A
// surface update that adds or removes a style fails here until the
// constants follow.
func TestSwapStylesMatchSurfaceData(t *testing.T) {
	constants := []SwapStyle{
		SwapInnerHTML, SwapOuterHTML, SwapTextContent, SwapBeforeBegin,
		SwapAfterBegin, SwapBeforeEnd, SwapAfterEnd, SwapDelete, SwapNone,
		SwapInnerMorph, SwapOuterMorph, SwapOuterSync,
		SwapBefore, SwapPrepend, SwapAppend, SwapAfter,
	}
	// Set comparison in both directions: count checks alone are blind to
	// duplicates masking a drift.
	have := map[string]bool{}
	for _, c := range constants {
		have[string(c)] = true
	}
	inSurface := map[string]bool{}
	for _, surface := range familySurfaces(t) {
		styles := surface.SwapStyles()
		for alias := range surface.SwapStyleAliases() {
			styles = append(styles, alias)
		}
		for _, s := range styles {
			inSurface[s] = true
			if !have[s] {
				t.Errorf("htmx %s style %q has no typed constant", surface.Version(), s)
			}
		}
	}
	for c := range have {
		if !inSurface[c] {
			t.Errorf("constant %q is in no family's surface", c)
		}
	}
}

// TestSwapModifiersMatchSurfaceData: every modifier of every family has a
// typed constructor here, and no constructor outlives its modifier.
func TestSwapModifiersMatchSurfaceData(t *testing.T) {
	constructors := map[string]bool{
		"transition": true, "swap": true, "settle": true, "ignoreTitle": true,
		"scroll": true, "show": true, "focus-scroll": true,
		"focusScroll": true, "scrollTarget": true, "showTarget": true,
		"strip": true, "swapEmpty": true, "target": true,
	}
	inSurface := map[string]bool{}
	for _, surface := range familySurfaces(t) {
		for _, name := range surface.SwapModifierNames() {
			inSurface[name] = true
			if !constructors[name] {
				t.Errorf("htmx %s swap modifier %q has no typed constructor", surface.Version(), name)
			}
		}
	}
	for name := range constructors {
		if !inSurface[name] {
			t.Errorf("constructor for %q has no surface modifier: remove it", name)
		}
	}
}

func TestSetReswapHtmx4Modifiers(t *testing.T) {
	rec := httptest.NewRecorder()
	SetReswap(rec, SwapInnerMorph, SwapShow("top"), SwapShowTarget("#other"), SwapFocusScrollV4(false), SwapStrip(true), SwapEmpty(true), SwapTarget("#out"))
	want := "innerMorph show:top showTarget:#other focusScroll:false strip:true swapEmpty:true target:#out"
	if got := rec.Header().Get("HX-Reswap"); got != want {
		t.Errorf("HX-Reswap = %q, want %q", got, want)
	}
	surface, err := htmxsurface.ForVersion("4.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if verr := surface.ValidateValue("hx-swap", want); verr != nil {
		t.Errorf("the header the helpers build must validate under htmx 4: %v", verr)
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
