// Package nethttp adapts ghtmx rendering to net/http handlers with
// opt-in automatic render-mode selection (FR-035).
//
// Automatic selection is active only inside Render: htmx requests
// (HX-Request: true) receive the standalone fragment, every other
// request receives the full page. The one htmx request that gets the
// page is a history restore (HX-History-Restore-Request: true): htmx
// refetches the URL on back/forward navigation and selects the
// [hx-history-elt] element out of the response, so it needs the
// document that element lives in. The core runtime never inspects
// request headers, and an application that imports no adapter keeps the
// fully supported explicit path — calling Render or RenderFragment
// itself (FR-034).
//
// Defaults, and how to override them:
//
//   - Render mode: selected from HX-Request and
//     HX-History-Restore-Request. Override with Mode.
//   - Status code: 200 via the response's first write. Override with
//     Status, which calls WriteHeader before the body.
//   - Content-Type: set to "text/html; charset=utf-8" when the handler
//     has not set one. Pre-set the header to override.
//   - htmx response headers: none. Opt in per response with Retarget,
//     Reswap, Reselect, PushURL, and ReplaceURL; they apply only on htmx
//     requests, where the headers have meaning. Headers outside the
//     current swap (HX-Redirect, HX-Refresh, HX-Location, HX-Trigger
//     emitters) are set before calling Render with the typed helpers in
//     the root package and the generated event emitters.
//
// Render streams: an error returned mid-render may leave the response
// partially written. The runtime's error is returned unchanged.
package nethttp

import (
	"context"
	"io"
	"net/http"

	"github.com/go-monolith/ghtmx"
)

// RenderMode is the render mode Render uses for a request.
type RenderMode int

const (
	// ModeAuto selects from the request: htmx requests render
	// standalone, everything else — including an htmx history restore,
	// which selects its [hx-history-elt] out of the page — renders the
	// full page.
	ModeAuto RenderMode = iota
	// ModeFull always renders the full page.
	ModeFull
	// ModeStandalone always renders only the fragment.
	ModeStandalone
)

type config struct {
	mode    RenderMode
	status  int
	headers []func(http.ResponseWriter)
}

// Option configures one Render call.
//
// When adding an option constructor, mirror it in every framework
// adapter under adapters/ — the chi module's parity test enforces that
// their sets stay complete.
type Option func(*config)

// Mode overrides the automatic render-mode selection. It changes only
// what renders: htmx response headers still follow the request — an
// htmx request keeps them even under ModeFull, and a plain request
// never gets them, ModeStandalone or not.
func Mode(m RenderMode) Option {
	return func(c *config) { c.mode = m }
}

// Status sets the HTTP status code instead of the implicit 200.
func Status(code int) Option {
	return func(c *config) { c.status = code }
}

// Retarget sets HX-Retarget so htmx swaps into the selector instead of
// the requesting element's target. Applied only on htmx requests.
func Retarget(selector string) Option {
	return headerOption(func(w http.ResponseWriter) { ghtmx.SetRetarget(w, selector) })
}

// Reswap sets HX-Reswap to override the swap style. Applied only on
// htmx requests.
func Reswap(style ghtmx.SwapStyle, modifiers ...ghtmx.SwapModifier) Option {
	return headerOption(func(w http.ResponseWriter) { ghtmx.SetReswap(w, style, modifiers...) })
}

// Reselect sets HX-Reselect to choose the part of the response to swap
// in. Applied only on htmx requests.
func Reselect(selector string) Option {
	return headerOption(func(w http.ResponseWriter) { ghtmx.SetReselect(w, selector) })
}

// PushURL sets HX-Push-URL to push a URL into the browser history.
// Applied only on htmx requests.
func PushURL(url string) Option {
	return headerOption(func(w http.ResponseWriter) { ghtmx.SetPushURL(w, url) })
}

// PushURLDisabled sets HX-Push-URL: false, preventing a history entry.
// Applied only on htmx requests.
func PushURLDisabled() Option {
	return headerOption(ghtmx.SetPushURLDisabled)
}

// ReplaceURL sets HX-Replace-URL to replace the current URL in the
// browser location bar. Applied only on htmx requests.
func ReplaceURL(url string) Option {
	return headerOption(func(w http.ResponseWriter) { ghtmx.SetReplaceURL(w, url) })
}

// ReplaceURLDisabled sets HX-Replace-URL: false, preventing the
// location-bar update. Applied only on htmx requests.
func ReplaceURLDisabled() Option {
	return headerOption(ghtmx.SetReplaceURLDisabled)
}

func headerOption(set func(http.ResponseWriter)) Option {
	return func(c *config) { c.headers = append(c.headers, set) }
}

// Render renders f in the mode selected for the request: standalone for
// htmx requests, full page otherwise (FR-035). It sets the status code
// and htmx response headers per the package defaults and the given
// options, then delegates to the runtime and returns its error
// unchanged.
//
// Pass a generated standalone fragment when both modes render the same
// markup, or pair a page with its fragment via WithPage:
//
//	func show(w http.ResponseWriter, r *http.Request) {
//		page := ItemsPage(items)
//		row := ItemRowFragment(item.ID, item.Name)
//		if err := nethttp.Render(w, r, nethttp.WithPage(page, row)); err != nil {
//			// The response may be partially written by now, so a
//			// replacement error page cannot be sent reliably: log and
//			// let the client see the truncated response.
//			log.Printf("render /items: %v", err)
//		}
//	}
func Render(w http.ResponseWriter, r *http.Request, f ghtmx.Fragment, opts ...Option) error {
	var cfg config
	for _, opt := range opts {
		opt(&cfg)
	}

	htmxRequest := ghtmx.IsHTMXRequest(r)
	mode := cfg.mode
	if mode == ModeAuto {
		mode = ModeFull
		if htmxRequest && !ghtmx.IsHistoryRestoreRequest(r) {
			mode = ModeStandalone
		}
	}

	// htmx response headers only mean something to htmx; a full-page
	// browser load ignores them, so they are not sent there.
	if htmxRequest {
		for _, set := range cfg.headers {
			set(w)
		}
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	if cfg.status != 0 {
		w.WriteHeader(cfg.status)
	}

	if mode == ModeStandalone {
		return f.RenderFragment(r.Context(), w)
	}
	return f.Render(r.Context(), w)
}

// WithPage pairs a full page with the fragment an htmx request should
// receive: the full-page mode renders page, the standalone mode renders
// only fragment. Generated standalone fragments render identically in
// both modes, so the pairing is what makes automatic selection choose
// between genuinely different responses.
//
// Both arguments must be non-nil; WithPage panics immediately rather
// than mid-request.
func WithPage(page ghtmx.Component, fragment ghtmx.Fragment) ghtmx.Fragment {
	if page == nil || fragment == nil {
		panic("nethttp.WithPage: page and fragment must be non-nil")
	}
	return pagedFragment{page: page, fragment: fragment}
}

type pagedFragment struct {
	page     ghtmx.Component
	fragment ghtmx.Fragment
}

func (p pagedFragment) Render(ctx context.Context, w io.Writer) error {
	return p.page.Render(ctx, w)
}

func (p pagedFragment) RenderFragment(ctx context.Context, w io.Writer) error {
	return p.fragment.RenderFragment(ctx, w)
}
