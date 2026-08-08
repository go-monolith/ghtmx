// Package iris is the ghtmx adapter for the iris framework, providing
// opt-in automatic render-mode selection (FR-035). iris's ResponseWriter
// embeds http.ResponseWriter and its Request accessor returns the plain
// *http.Request, so the adapter delegates to the nethttp adapter through
// them — mode selection, status, and header behavior are identical by
// construction; see that package for the documented defaults and
// overrides. No core package depends on it (constitution A5).
//
// One buffering note: iris's writer records the status code on
// WriteHeader and sends it with the first body byte (or at end of
// request) instead of immediately. Render always writes a body, so the
// wire order — headers, then status, then body — is exactly net/http's;
// the adapter tests pin byte-identity against the nethttp adapter to
// keep it that way. The recording does mean the last WriteHeader before
// the body wins: a status the handler set before Render is replaced by
// the Status option, where net/http would keep the first and drop the
// second as superfluous.
package iris

import (
	irisfw "github.com/kataras/iris/v12"

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
// htmx requests, full page otherwise (FR-035). iris handlers return
// nothing, so route the error as the application prefers:
//
//	app.Get("/items", func(ctx iris.Context) {
//		if err := adapter.Render(ctx, adapter.WithPage(page, row)); err != nil {
//			// The response may be partially written by now — log and
//			// let the client see the truncated response.
//			log.Printf("render /items: %v", err)
//		}
//	})
func Render(ctx irisfw.Context, f ghtmx.Fragment, opts ...Option) error {
	return nethttp.Render(ctx.ResponseWriter(), ctx.Request(), f, opts...)
}

// WithPage pairs a full page with the fragment an htmx request should
// receive. Both arguments must be non-nil.
func WithPage(page ghtmx.Component, fragment ghtmx.Fragment) ghtmx.Fragment {
	return nethttp.WithPage(page, fragment)
}
