// Package beego is the ghtmx adapter for the beego framework's v2
// major version, providing opt-in automatic render-mode selection
// (FR-035). beego's context carries the standard library's shapes
// directly — ctx.ResponseWriter is an http.ResponseWriter over the real
// connection and ctx.Request the *http.Request — so the adapter
// delegates to the nethttp adapter through them: mode selection,
// status, and header behavior are identical by construction; see that
// package for the documented defaults and overrides. No core package
// depends on it (constitution A5).
//
// Two divergences follow from beego's context, not from the adapter:
//
//   - ctx.ResponseWriter (*context.Response) records the first status
//     code and silently drops every later WriteHeader, where net/http
//     logs a superfluous-call warning. A status the handler committed
//     before Render therefore wins over the Status option, quietly.
//   - beego's own output buffer is bypassed: Render writes through
//     ctx.ResponseWriter, never ctx.Output. A pending
//     ctx.Output.SetStatus is flushed by the router only after the
//     handler returns — too late, the response is committed — so set
//     the status with the Status option instead.
package beego

import (
	beegocontext "github.com/beego/beego/v2/server/web/context"

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
// htmx requests, full page otherwise (FR-035). beego's functional
// HandleFunc returns nothing, so the handler handles Render's error
// itself. The render streams straight to the connection — by the time
// an error surfaces the response may be partially written, so log it
// and let the client see the truncated response:
//
//	router.Get("/items", func(ctx *context.Context) {
//		if err := beego.Render(ctx, beego.WithPage(page, row)); err != nil {
//			log.Printf("render /items: %v", err)
//		}
//	})
func Render(ctx *beegocontext.Context, f ghtmx.Fragment, opts ...Option) error {
	return nethttp.Render(ctx.ResponseWriter, ctx.Request, f, opts...)
}

// WithPage pairs a full page with the fragment an htmx request should
// receive. Both arguments must be non-nil.
func WithPage(page ghtmx.Component, fragment ghtmx.Fragment) ghtmx.Fragment {
	return nethttp.WithPage(page, fragment)
}
