// Package revel is the ghtmx adapter for the revel framework, providing
// opt-in automatic render-mode selection (FR-035). revel is a full-stack
// MVC framework whose controller actions return revel.Result values
// instead of writing to the response, so the adapter's primary surface
// is Result — an action returns one and revel applies it after the
// filter chain:
//
//	func (c Items) Show() revel.Result {
//		return ghtmxrevel.Result(c.Controller, ghtmxrevel.WithPage(page, row))
//	}
//
// Result's Apply bridges revel's Request and Response to the net/http
// shapes the nethttp adapter works against and delegates to it — mode
// selection, status, and header behavior are identical by construction;
// see that package for the documented defaults and overrides. Render is
// the same bridge as a plain function for code that holds the request
// and response directly, and it returns the runtime's error unchanged;
// revel's Result interface has no error result, so Apply instead logs a
// render failure through the controller's logger (RevelLog when the
// controller has none) and leaves the response as the runtime wrote it.
// No core package depends on it (constitution A5).
//
// Three divergences follow from revel's conventions, not from the
// adapter:
//
//   - A status the controller assigned to c.Response.Status is honored
//     when no Status option is given, exactly as revel's built-in
//     results honor it; the Status option wins when both are present.
//     The committed status is written back to c.Response.Status so
//     revel's request log reports what was sent.
//   - A Content-Type the controller assigned to c.Response.ContentType
//     counts as a pre-set header: it blocks the adapter's text/html
//     default, mirroring the WriteHeader convention of revel's built-in
//     results. One already set on the server headers outranks it —
//     the precedence is pre-set server header, then resp.ContentType,
//     then the text/html default — where the built-in results would
//     let resp.ContentType overwrite the server header.
//   - The render streams through revel's response writer: on a
//     mid-render error the client sees the truncated body — revel
//     replaces the response only when an action panics, not when a
//     Result's Apply fails.
package revel

import (
	"errors"
	"net/http"
	"net/url"
	"slices"

	revelfw "github.com/revel/revel"

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

// Result returns the revel.Result that renders f in the mode selected
// for the request: standalone for htmx requests, full page otherwise
// (FR-035). It is revel's idiom — the controller action returns it and
// revel applies it after the filter chain:
//
//	func (c Items) Create() revel.Result {
//		return ghtmxrevel.Result(c.Controller, row,
//			ghtmxrevel.Status(http.StatusCreated), ghtmxrevel.Retarget("#items"))
//	}
//
// revel's Result interface cannot surface the render error, so Apply
// logs one through the controller's logger — RevelLog when the
// controller has none — and leaves the possibly partially written
// response as is. Code that needs the error itself calls Render.
func Result(c *revelfw.Controller, f ghtmx.Fragment, opts ...Option) revelfw.Result {
	return renderResult{controller: c, fragment: f, opts: opts}
}

// Render renders f in the mode selected for the request: standalone for
// htmx requests, full page otherwise (FR-035). It is the bridge behind
// Result for code that holds revel's request and response directly — a
// filter, an interceptor, or a custom Result — and it returns the
// runtime's error unchanged.
func Render(req *revelfw.Request, resp *revelfw.Response, f ghtmx.Fragment, opts ...Option) error {
	w := newResponseWriter(resp)
	err := nethttp.Render(w, bridgeRequest(req), f, opts...)
	// net/http commits the headers and the implicit 200 when the handler
	// returns even if it wrote no body byte; revel has no such moment, so
	// commit here — otherwise a zero-byte render (an empty fragment
	// carrying only HX-* headers) would drop its staged headers and never
	// write the status back. flush is a no-op when a body write or
	// WriteHeader already ran.
	w.flush()
	return err
}

// WithPage pairs a full page with the fragment an htmx request should
// receive. Both arguments must be non-nil.
func WithPage(page ghtmx.Component, fragment ghtmx.Fragment) ghtmx.Fragment {
	return nethttp.WithPage(page, fragment)
}

// renderResult is the revel.Result returned by Result.
type renderResult struct {
	controller *revelfw.Controller
	fragment   ghtmx.Fragment
	opts       []Option
}

// Apply renders through Render. Result's contract has no error return,
// so a render failure is logged and the response left as the runtime
// wrote it — possibly truncated, never replaced.
func (r renderResult) Apply(req *revelfw.Request, resp *revelfw.Response) {
	if err := Render(req, resp, r.fragment, r.opts...); err != nil {
		log := revelfw.RevelLog
		if r.controller != nil && r.controller.Log != nil {
			log = r.controller.Log
		}
		log.Error("ghtmx render failed; the response may be partially written", "error", err)
	}
}

// bridgeRequest exposes the revel request as an *http.Request carrying
// the headers (HX-Request), method, URL, host, and context. The nethttp
// adapter reads only headers and context today; the rest is hardening
// against it reading more tomorrow.
func bridgeRequest(req *revelfw.Request) *http.Request {
	header := http.Header{}
	if req.Header != nil && req.Header.Server != nil {
		for _, key := range req.Header.Server.GetKeys() {
			for _, value := range req.Header.Server.Get(key) {
				header.Add(key, value)
			}
		}
	}
	u := req.URL
	if u == nil {
		u = &url.URL{}
	}
	r := &http.Request{
		Method: req.Method,
		URL:    u,
		Host:   req.Host,
		Header: header,
	}
	// revel resolves the request-scoped context through the server
	// engine; an engine that provides none leaves r on
	// context.Background().
	if req.In != nil {
		if ctx := req.Context(); ctx != nil {
			r = r.WithContext(ctx)
		}
	}
	return r
}

// errNoWriter surfaces a response bound to no engine writer instead of a
// nil-interface panic mid-render.
var errNoWriter = errors.New("revel adapter: response has no engine writer")

// responseWriter bridges revel's Response to http.ResponseWriter through
// its public surface: headers stage in an http.Header seeded from the
// response's current server headers — so pre-set values (a
// controller-chosen Content-Type, cookies) are seen — and flush through
// OutResponse's header access before the status commits via SetStatus,
// mirroring net/http's write-then-frozen header semantics. On revel's Go
// engine SetStatus calls WriteHeader on the underlying connection
// immediately, so the ordering is load-bearing, not cosmetic.
type responseWriter struct {
	resp    *revelfw.Response
	header  http.Header
	seed    http.Header
	flushed bool
}

func newResponseWriter(resp *revelfw.Response) *responseWriter {
	header := http.Header{}
	if h := resp.Out.Header(); h != nil && h.Server != nil {
		for _, key := range h.Server.GetKeys() {
			for _, value := range h.Server.Get(key) {
				header.Add(key, value)
			}
		}
	}
	seed := header.Clone()
	// revel's results treat resp.ContentType as the chosen Content-Type.
	// Seeding it after the clone stages it as a change to flush, and its
	// presence blocks the nethttp adapter's text/html default.
	if resp.ContentType != "" && header.Get("Content-Type") == "" {
		header.Set("Content-Type", resp.ContentType)
	}
	return &responseWriter{resp: resp, header: header, seed: seed}
}

func (w *responseWriter) Header() http.Header { return w.header }

func (w *responseWriter) Write(p []byte) (int, error) {
	w.flush()
	writer := w.resp.GetWriter()
	if writer == nil {
		return 0, errNoWriter
	}
	return writer.Write(p)
}

func (w *responseWriter) WriteHeader(code int) {
	if w.flushed {
		return
	}
	w.resp.Status = code
	w.flush()
}

// flush commits the staged headers and the status, once. Only values
// Render changed are written back: re-setting seeded values would
// duplicate cookies, because revel's ServerHeader appends Set-Cookie
// rather than replacing by name. The status comes from WriteHeader when
// the Status option ran, from a controller-assigned resp.Status
// otherwise, and defaults to 200 — the convention revel's built-in
// results follow — and is left on resp.Status for revel's request log.
func (w *responseWriter) flush() {
	if w.flushed {
		return
	}
	w.flushed = true
	if out := w.resp.Out.Header(); out != nil {
		for key, values := range w.header {
			if slices.Equal(values, w.seed[key]) {
				continue
			}
			for i, value := range values {
				if i == 0 {
					out.Set(key, value)
					continue
				}
				out.Add(key, value)
			}
		}
	}
	if w.resp.Status == 0 {
		w.resp.Status = http.StatusOK
	}
	w.resp.SetStatus(w.resp.Status)
}
