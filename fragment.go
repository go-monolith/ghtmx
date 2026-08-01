package ghtmx

import (
	"context"
	"io"
)

// Fragment is a component with a standalone render mode (FR-031). The
// compiler emits one Fragment-returning entry point per declared fragment:
// Render and RenderFragment both execute only the fragment's body — a
// standalone wrapper carries no page around it — so the two methods are
// byte-identical on generated fragments. RenderFragment exists as the
// intention-revealing name for htmx swap handlers (FR-034) and as the
// adapter integration point for automatic mode selection (FR-035).
//
// Both methods work directly against any io.Writer, including an
// http.ResponseWriter inside a plain net/http handler with no adapter
// imported (FR-034, FR-090). On this explicit path the engine only ever
// calls Write: it sets no HTTP status code, no response headers, and does
// not flush, so the handler keeps full control of the response. Render
// errors return to the caller unswallowed; the engine adds no recover or
// panic of its own.
type Fragment interface {
	// Component supplies Render, so a Fragment composes anywhere a
	// component can. On a standalone wrapper there is no page around the
	// fragment: Render writes exactly the bytes RenderFragment writes.
	Component
	// RenderFragment renders only the fragment's markup, with no
	// surrounding page chrome, and writes no HTTP status code or headers.
	RenderFragment(ctx context.Context, w io.Writer) error
}
