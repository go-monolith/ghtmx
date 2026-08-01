// Package chi is the ghtmx adapter for the chi router, providing opt-in
// automatic render-mode selection (FR-035). chi handlers are plain
// net/http handlers, so the adapter delegates to the nethttp adapter —
// mode selection, status, and header behavior are identical by
// construction; see that package for the documented defaults and
// overrides. The package exists so a chi application needs one adapter
// import, and no core package depends on it (constitution A5).
package chi

import (
	"net/http"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/adapters/nethttp"
)

// Option configures one Render call. It is the nethttp adapter's option
// type, so the two packages' options mix freely.
type Option = nethttp.Option

// RenderMode is the render mode Render uses for a request.
type RenderMode = nethttp.RenderMode

const (
	// ModeAuto selects from the request: htmx requests render
	// standalone, everything else renders the full page.
	ModeAuto = nethttp.ModeAuto
	// ModeFull always renders the full page.
	ModeFull = nethttp.ModeFull
	// ModeStandalone always renders only the fragment.
	ModeStandalone = nethttp.ModeStandalone
)

// The option constructors mirror the nethttp adapter's, wrapper by
// wrapper so a signature change there breaks compilation here instead of
// drifting silently. The chi module's parity test enforces that the set
// stays complete.

// Mode overrides the automatic render-mode selection.
func Mode(m RenderMode) Option { return nethttp.Mode(m) }

// Status sets the HTTP status code instead of the implicit 200.
func Status(code int) Option { return nethttp.Status(code) }

// Retarget sets HX-Retarget. Applied only on htmx requests.
func Retarget(selector string) Option { return nethttp.Retarget(selector) }

// Reswap sets HX-Reswap. Applied only on htmx requests.
func Reswap(style ghtmx.SwapStyle, modifiers ...ghtmx.SwapModifier) Option {
	return nethttp.Reswap(style, modifiers...)
}

// Reselect sets HX-Reselect. Applied only on htmx requests.
func Reselect(selector string) Option { return nethttp.Reselect(selector) }

// PushURL sets HX-Push-URL. Applied only on htmx requests.
func PushURL(url string) Option { return nethttp.PushURL(url) }

// PushURLDisabled sets HX-Push-URL: false. Applied only on htmx requests.
func PushURLDisabled() Option { return nethttp.PushURLDisabled() }

// ReplaceURL sets HX-Replace-URL. Applied only on htmx requests.
func ReplaceURL(url string) Option { return nethttp.ReplaceURL(url) }

// ReplaceURLDisabled sets HX-Replace-URL: false. Applied only on htmx
// requests.
func ReplaceURLDisabled() Option { return nethttp.ReplaceURLDisabled() }

// Render renders f in the mode selected for the request: standalone for
// htmx requests, full page otherwise (FR-035).
func Render(w http.ResponseWriter, r *http.Request, f ghtmx.Fragment, opts ...Option) error {
	return nethttp.Render(w, r, f, opts...)
}

// WithPage pairs a full page with the fragment an htmx request should
// receive. Both arguments must be non-nil.
func WithPage(page ghtmx.Component, fragment ghtmx.Fragment) ghtmx.Fragment {
	return nethttp.WithPage(page, fragment)
}
