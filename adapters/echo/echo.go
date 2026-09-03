// Package echo is the ghtmx adapter for the echo framework, providing
// opt-in automatic render-mode selection (FR-035). It delegates to the
// nethttp adapter through echo's request and response accessors — mode
// selection, status, and header behavior are identical by construction;
// see that package for the documented defaults and overrides. No core
// package depends on it (constitution A5).
package echo

import (
	echofw "github.com/labstack/echo/v4"

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
	// standalone, everything else — including an htmx history restore,
	// which selects its [hx-history-elt] out of the page — renders the
	// full page.
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
// htmx requests, full page otherwise (FR-035). In echo's idiom the
// handler returns Render's error directly. If the render fails before
// the first body byte, the response is still uncommitted and echo's
// error handler sends its own error response — inheriting the
// Content-Type and any htmx headers Render already set.
func Render(c echofw.Context, f ghtmx.Fragment, opts ...Option) error {
	return nethttp.Render(c.Response(), c.Request(), f, opts...)
}

// WithPage pairs a full page with the fragment an htmx request should
// receive. Both arguments must be non-nil.
func WithPage(page ghtmx.Component, fragment ghtmx.Fragment) ghtmx.Fragment {
	return nethttp.WithPage(page, fragment)
}
