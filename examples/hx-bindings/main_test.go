package hxbindings

import (
	"context"
	"strings"
	"testing"
)

// TestSymbolBindingEmitsStaticPath: FR-020 — the handler's registered path
// is part of the static markup.
func TestSymbolBindingEmitsStaticPath(t *testing.T) {
	var sb strings.Builder
	if err := itemList(nil).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), `hx-get="/items"`) {
		t.Errorf("expected the registered path, got:\n%s", sb.String())
	}
}

// TestPageEmbedsStyleSheet: the rules live in items.css and reach the
// page through //go:embed. A missing file is a compile error, so what
// this guards is the wiring the compiler cannot see — a template that
// stops calling @styleSheet(), or a stylesheet emptied by a bad edit.
func TestPageEmbedsStyleSheet(t *testing.T) {
	var sb strings.Builder
	if err := itemsPage(demoItems).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "<style>") || !strings.Contains(out, "</style>") {
		t.Errorf("the page must carry a style element, got:\n%s", out)
	}
	if !strings.Contains(out, ".pill {") {
		t.Errorf("items.css was not inlined into the page, got:\n%s", out)
	}
}

// TestConstructorEscapingComposition is the mandatory route-binding case
// of the escaping conformance suite (FR-023, NFR-007): parameters
// containing /, ?, #, &, and a space are percent-encoded for the path
// segment position first, and HTML attribute-value escaping composes on
// top (& in the encoded URL becomes &amp; in the attribute).
func TestConstructorEscapingComposition(t *testing.T) {
	var sb strings.Builder
	if err := itemList([]string{`a/b?c#d&e f`}).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	// URL escaping first: / ? # and space percent-encoded; & is legal in a
	// path segment and survives URL escaping...
	// ...then attribute escaping: the surviving & becomes &amp;.
	want := `hx-get="/items/a%2Fb%3Fc%23d&amp;e%20f"`
	if !strings.Contains(out, want) {
		t.Errorf("expected %s in output, got:\n%s", want, out)
	}
}

// TestEscapingNotOverridableAtBindingSite: the emission is fixed by the
// attribute name — the constructor's SafeURL is the only accepted type, so
// there is no author-facing knob to change the escaping.
func TestEscapingNotOverridableAtBindingSite(t *testing.T) {
	var sb strings.Builder
	if err := itemList([]string{"plain"}).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), `hx-get="/items/plain"`) {
		t.Errorf("plain values pass through unchanged, got:\n%s", sb.String())
	}
}
