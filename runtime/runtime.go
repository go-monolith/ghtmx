package runtime

import (
	"context"
	"io"

	"github.com/go-monolith/ghtmx"
)

// GeneratedComponentInput is used to avoid generated code needing to import the `context` and `io` packages.
type GeneratedComponentInput struct {
	Context context.Context
	Writer  io.Writer
}

// GeneratedTemplate is used to avoid generated code needing to import the `context` and `io` packages.
func GeneratedTemplate(f func(GeneratedComponentInput) error) ghtmx.Component {
	return ghtmx.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return f(GeneratedComponentInput{ctx, w})
	})
}

// GeneratedFragment creates a ghtmx.Fragment from a generated standalone
// fragment wrapper. Render and RenderFragment are the same function: a
// standalone wrapper executes only the fragment body, so there is no page
// output to strip (FR-031).
func GeneratedFragment(f func(GeneratedComponentInput) error) ghtmx.Fragment {
	return generatedFragment{f: f}
}

type generatedFragment struct {
	f func(GeneratedComponentInput) error
}

// Render writes the fragment's markup so the wrapper composes as an
// ordinary component. It runs the same body as RenderFragment and writes
// the same bytes: only Write is called on w — no status code, no headers.
func (g generatedFragment) Render(ctx context.Context, w io.Writer) error {
	return g.f(GeneratedComponentInput{Context: ctx, Writer: w})
}

// RenderFragment writes only the fragment's markup, for handler-explicit
// rendering (FR-034): pass an http.ResponseWriter directly — only Write is
// called on it, so status code and headers stay with the handler. Errors
// return to the caller.
func (g generatedFragment) RenderFragment(ctx context.Context, w io.Writer) error {
	return g.f(GeneratedComponentInput{Context: ctx, Writer: w})
}
