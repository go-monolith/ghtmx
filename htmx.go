package ghtmx

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Typed htmx response-header helpers (FR-036): each htmx 2.x response
// header is set through a helper whose parameter types make invalid
// values unrepresentable — SwapStyle and SwapModifier are the only
// accepted types for HX-Reswap, so a value outside the pinned version's
// surface is a Go compile error (FR-052), not a runtime surprise.
//
// Call the helpers before writing the response status or body: net/http
// silently discards headers set afterwards.

// IsHTMXRequest reports whether the request was issued by htmx (the
// HX-Request header). Adapters use it for automatic render-mode selection
// (FR-035); handler code can branch on it explicitly.
func IsHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// SetRetarget sets HX-Retarget: a CSS selector overriding the swap target
// for this response.
func SetRetarget(w http.ResponseWriter, selector string) {
	w.Header().Set("HX-Retarget", selector)
}

// SwapStyle is an hx-swap swap style of the pinned htmx surface. Using
// the typed constants keeps invalid styles out of HX-Reswap at compile
// time; the set is asserted against the embedded surface data in tests.
type SwapStyle string

const (
	SwapInnerHTML   SwapStyle = "innerHTML"
	SwapOuterHTML   SwapStyle = "outerHTML"
	SwapTextContent SwapStyle = "textContent"
	SwapBeforeBegin SwapStyle = "beforebegin"
	SwapAfterBegin  SwapStyle = "afterbegin"
	SwapBeforeEnd   SwapStyle = "beforeend"
	SwapAfterEnd    SwapStyle = "afterend"
	SwapDelete      SwapStyle = "delete"
	SwapNone        SwapStyle = "none"
)

// SwapModifier is one hx-swap modifier, constructed only through the
// Swap* modifier functions below.
type SwapModifier struct{ s string }

// SwapTransition requests a view transition for the swap.
func SwapTransition() SwapModifier { return SwapModifier{s: "transition:true"} }

// SwapDelay delays the swap after the response is received. Durations
// are truncated to whole milliseconds; negative values clamp to zero.
func SwapDelay(d time.Duration) SwapModifier {
	return SwapModifier{s: fmt.Sprintf("swap:%dms", max(d.Milliseconds(), 0))}
}

// SwapSettle delays the settle step after the swap. Durations are
// truncated to whole milliseconds; negative values clamp to zero.
func SwapSettle(d time.Duration) SwapModifier {
	return SwapModifier{s: fmt.Sprintf("settle:%dms", max(d.Milliseconds(), 0))}
}

// SwapIgnoreTitle keeps the page title unchanged even when the response
// contains a <title> element.
func SwapIgnoreTitle() SwapModifier { return SwapModifier{s: "ignoreTitle:true"} }

// SwapScroll scrolls the target after the swap: "top" or "bottom",
// optionally prefixed with a selector ("#list:bottom"). The spec must not
// contain whitespace — hx-swap specs are space-separated, so htmx itself
// cannot express such selectors; whitespace here yields a zero modifier.
func SwapScroll(spec string) SwapModifier { return swapSpecModifier("scroll", spec) }

// SwapShow scrolls the viewport to show the target after the swap: "top"
// or "bottom", optionally prefixed with a selector. The spec must not
// contain whitespace (see SwapScroll).
func SwapShow(spec string) SwapModifier { return swapSpecModifier("show", spec) }

func swapSpecModifier(name, spec string) SwapModifier {
	if spec == "" || strings.ContainsAny(spec, " \t\r\n") {
		return SwapModifier{} // Unrepresentable in a space-separated spec.
	}
	return SwapModifier{s: name + ":" + spec}
}

// SwapFocusScroll overrides focus preservation scrolling for the swap.
func SwapFocusScroll(enabled bool) SwapModifier {
	return SwapModifier{s: fmt.Sprintf("focus-scroll:%t", enabled)}
}

// SetReswap sets HX-Reswap: the swap style (and modifiers) overriding
// hx-swap for this response.
func SetReswap(w http.ResponseWriter, style SwapStyle, modifiers ...SwapModifier) {
	parts := make([]string, 0, len(modifiers)+1)
	parts = append(parts, string(style))
	for _, m := range modifiers {
		if m.s == "" {
			continue // A zero-value modifier adds nothing.
		}
		parts = append(parts, m.s)
	}
	w.Header().Set("HX-Reswap", strings.Join(parts, " "))
}

// SetRedirect sets HX-Redirect: htmx performs a full page load of url.
func SetRedirect(w http.ResponseWriter, url string) {
	w.Header().Set("HX-Redirect", url)
}

// SetReselect sets HX-Reselect: a CSS selector choosing the part of the
// response to swap in, overriding hx-select.
func SetReselect(w http.ResponseWriter, selector string) {
	w.Header().Set("HX-Reselect", selector)
}

// SetPushURL sets HX-Push-Url: pushes url into the browser history.
// SetPushURLDisabled prevents a push the request would otherwise perform.
func SetPushURL(w http.ResponseWriter, url string) {
	w.Header().Set("HX-Push-Url", url)
}

// SetPushURLDisabled sets HX-Push-Url: false.
func SetPushURLDisabled(w http.ResponseWriter) {
	w.Header().Set("HX-Push-Url", "false")
}

// SetReplaceURL sets HX-Replace-Url: replaces the current history entry.
// SetReplaceURLDisabled prevents a replacement.
func SetReplaceURL(w http.ResponseWriter, url string) {
	w.Header().Set("HX-Replace-Url", url)
}

// SetReplaceURLDisabled sets HX-Replace-Url: false.
func SetReplaceURLDisabled(w http.ResponseWriter) {
	w.Header().Set("HX-Replace-Url", "false")
}

// SetRefresh sets HX-Refresh: htmx performs a full page refresh.
func SetRefresh(w http.ResponseWriter) {
	w.Header().Set("HX-Refresh", "true")
}

// SetLocation sets HX-Location in its simple string form: htmx navigates
// to path with an ajax request, swapping into the body without a full
// page load. htmx also accepts a JSON object form (path plus target,
// swap, and other context fields), which this helper does not cover — a
// path starting with "{" would be parsed as that JSON form by htmx.
func SetLocation(w http.ResponseWriter, path string) {
	w.Header().Set("HX-Location", path)
}
