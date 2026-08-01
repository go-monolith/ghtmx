// Package fiber is the ghtmx adapter for the fiber framework, providing
// opt-in automatic render-mode selection (FR-035). fiber runs on
// fasthttp, so the adapter bridges its context to the net/http shapes
// the nethttp adapter works against and delegates to it — mode
// selection, status, and header behavior are identical by construction;
// see that package for the documented defaults and overrides. No core
// package depends on it (constitution A5).
package fiber

import (
	"net/http"
	"net/url"
	"slices"

	fiberfw "github.com/gofiber/fiber/v2"

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
// htmx requests, full page otherwise (FR-035). In fiber's idiom the
// handler returns Render's error directly.
//
// The render goes into fiber's buffered response body, sent when the
// handler returns. Two divergences from the streaming net/http path
// follow: on a mid-render error, fiber's error handler replaces the
// partial body with its own error response (no truncated HTML reaches
// the client), and calling c.Send* after Render replaces the rendered
// body.
func Render(c *fiberfw.Ctx, f ghtmx.Fragment, opts ...Option) error {
	return nethttp.Render(newResponseWriter(c), bridgeRequest(c), f, opts...)
}

// WithPage pairs a full page with the fragment an htmx request should
// receive. Both arguments must be non-nil.
func WithPage(page ghtmx.Component, fragment ghtmx.Fragment) ghtmx.Fragment {
	return nethttp.WithPage(page, fragment)
}

// bridgeRequest exposes the fiber request as an *http.Request carrying
// the headers (HX-Request), method, URL, and context. The nethttp
// adapter reads only headers and context today; the rest is hardening
// against it reading more tomorrow.
func bridgeRequest(c *fiberfw.Ctx) *http.Request {
	header := http.Header{}
	c.Request().Header.VisitAll(func(key, value []byte) {
		header.Add(string(key), string(value))
	})
	uri := c.Request().URI()
	r := &http.Request{
		Method: c.Method(),
		URL: &url.URL{
			Scheme:   string(uri.Scheme()),
			Host:     string(uri.Host()),
			Path:     string(uri.Path()),
			RawQuery: string(uri.QueryString()),
		},
		Header: header,
	}
	// The user context is context.Background() unless middleware set
	// one — unlike r.Context() on net/http, it does not cancel when the
	// client disconnects.
	return r.WithContext(c.UserContext())
}

// responseWriter bridges fiber's fasthttp response to http.ResponseWriter.
// Headers stage in an http.Header seeded from the response's current
// headers — so pre-set values (a handler-chosen Content-Type) are seen —
// and flush to fasthttp before the status or first body byte, mirroring
// net/http's write-then-frozen header semantics.
type responseWriter struct {
	ctx     *fiberfw.Ctx
	header  http.Header
	seed    http.Header
	flushed bool
}

func newResponseWriter(c *fiberfw.Ctx) *responseWriter {
	header := http.Header{}
	// fasthttp reports its text/plain default Content-Type as if it were
	// set, which would block the adapter's text/html fallback. With the
	// default suppressed during the walk, only explicitly-set values are
	// seeded — including a deliberately chosen text/plain.
	h := &c.Response().Header
	h.SetNoDefaultContentType(true)
	h.VisitAll(func(key, value []byte) {
		header.Add(string(key), string(value))
	})
	h.SetNoDefaultContentType(c.App().Config().DisableDefaultContentType)
	return &responseWriter{ctx: c, header: header, seed: header.Clone()}
}

func (w *responseWriter) Header() http.Header { return w.header }

func (w *responseWriter) Write(p []byte) (int, error) {
	w.flush()
	return w.ctx.Response().BodyWriter().Write(p)
}

func (w *responseWriter) WriteHeader(code int) {
	if w.flushed {
		return
	}
	w.ctx.Status(code)
	w.flush()
}

func (w *responseWriter) flush() {
	if w.flushed {
		return
	}
	w.flushed = true
	// Only write what Render changed: re-setting seeded values would
	// duplicate cookies, because fasthttp appends Set-Cookie
	// unconditionally rather than replacing by name.
	for key, values := range w.header {
		if slices.Equal(values, w.seed[key]) {
			continue
		}
		for i, value := range values {
			if i == 0 {
				w.ctx.Response().Header.Set(key, value)
				continue
			}
			w.ctx.Response().Header.Add(key, value)
		}
	}
}
