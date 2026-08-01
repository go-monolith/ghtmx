package proxy

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/analyzer"
	"github.com/go-monolith/ghtmx/internal/generator/central"
	"github.com/go-monolith/ghtmx/internal/htmxsurface"
	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
	parser "github.com/go-monolith/ghtmx/internal/parser"
	"github.com/go-monolith/ghtmx/internal/routes"
)

func completionServer(t *testing.T) *Server {
	t.Helper()
	surface, err := htmxsurface.ForVersion("2.0.10")
	if err != nil {
		t.Fatal(err)
	}
	table := routes.NewTable()
	table.Add(routes.Route{Verb: routes.GET, Path: "/items", Handler: routes.SymbolRef{PkgPath: "example.com/app/handlers", Name: "ListItems"}})
	table.Add(routes.Route{Verb: routes.POST, Path: "/items", Handler: routes.SymbolRef{PkgPath: "example.com/app/handlers", Name: "CreateItem"}})
	table.Add(routes.Route{
		Verb: routes.POST, Path: "/items/{id}", Handler: routes.SymbolRef{PkgPath: "example.com/app/handlers", Name: "SaveItem"},
		Params: []routes.RouteParam{{Name: "id"}},
	})
	byName, _ := central.Naming(table)

	sa := analyzer.NewSetAnalysis()
	tf, err := parser.ParseString("package main\n\nevent ItemSaved(id string)\n\nevent CartCleared()\n")
	if err != nil {
		t.Fatal(err)
	}
	tf.Filepath = "events.ghtmx"
	sa.CollectFile(tf)

	return &Server{
		Log:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		surface:          surface,
		routeTable:       table,
		constructors:     byName,
		generatedPkgName: "ghtmxgen",
		setAnalysis:      sa,
	}
}

func labels(items []lsp.CompletionItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Label
	}
	return out
}

// TestVerbFilteredRouteCompletion: FR-081 — hx-post={ … } lists only
// POST-registered handlers, and parameterised routes complete as
// constructor snippets with parameter placeholders.
func TestVerbFilteredRouteCompletion(t *testing.T) {
	s := completionServer(t)
	items := s.routeBindingCompletions(detectCompletionContext(`	<button hx-post={ `))
	got := strings.Join(labels(items), ";")
	if !strings.Contains(got, "handlers.CreateItem") {
		t.Errorf("POST handler missing: %s", got)
	}
	if strings.Contains(got, "ListItems") {
		t.Errorf("GET handler must be filtered out: %s", got)
	}
	var snippet *lsp.CompletionItem
	for i, it := range items {
		if strings.HasPrefix(it.Label, "ghtmxgen.SaveItem") {
			snippet = &items[i]
		}
	}
	if snippet == nil {
		t.Fatalf("constructor completion missing: %s", got)
	}
	if snippet.InsertText != "ghtmxgen.SaveItem(${1:id})" || snippet.InsertTextFormat != lsp.InsertTextFormatSnippet {
		t.Errorf("constructor must insert parameter placeholders, got %q", snippet.InsertText)
	}
}

// TestAttributeNameAndValueCompletion: FR-082 — names and values offered
// only when valid for the configured version.
func TestAttributeNameAndValueCompletion(t *testing.T) {
	s := completionServer(t)

	names := labels(s.ghtmxCompletionsFromLine(`	<div hx-sw`, 11))
	if len(names) == 0 || names[0] != "hx-swap" {
		t.Errorf("expected hx-swap first, got %v", names)
	}
	for _, n := range names {
		if _, ok := s.surface.Attribute(n); !ok && n != "hx-swap-oob" && !strings.HasPrefix(n, "hx-on") {
			t.Errorf("offered name %q is not in the configured surface", n)
		}
	}

	values := labels(s.attributeValueCompletions(detectCompletionContext(`	<div hx-swap="inner`)))
	if len(values) != 1 || values[0] != "innerHTML" {
		t.Errorf("expected only innerHTML for prefix inner, got %v", values)
	}
	all := labels(s.attributeValueCompletions(detectCompletionContext(`	<div hx-swap="`)))
	if len(all) != len(s.surface.SwapStyles()) {
		t.Errorf("expected the full configured style set, got %v", all)
	}
}

// TestEventNameCompletion: FR-082 — only declared events are offered, in
// hx-on listeners and hx-trigger values.
func TestEventNameCompletion(t *testing.T) {
	s := completionServer(t)

	listeners := labels(s.attributeNameCompletions(detectCompletionContext(`	<div hx-on:it`)))
	if len(listeners) != 1 || listeners[0] != "item-saved" {
		t.Errorf("expected only the declared item-saved, got %v", listeners)
	}

	triggers := labels(s.attributeValueCompletions(detectCompletionContext(`	<div hx-trigger="`)))
	if strings.Join(triggers, ";") != "cart-cleared;item-saved" {
		t.Errorf("expected exactly the declared events, got %v", triggers)
	}
}

// ghtmxCompletionsFromLine exercises the REAL entry point through the
// document store, at the end of the given line.
func (p *Server) ghtmxCompletionsFromLine(line string, character int) []lsp.CompletionItem {
	const testURI = "file:///completions-test.ghtmx"
	if p.TemplSource == nil {
		p.TemplSource = newDocumentContents(p.Log)
	}
	p.TemplSource.Set(testURI, &Document{Log: p.Log, Lines: []string{line}})
	items, _ := p.ghtmxCompletions(testURI, lsp.Position{Line: 0, Character: uint32(character)})
	return items
}

// TestCompletionEdgeContexts covers the review-found context gaps.
func TestCompletionEdgeContexts(t *testing.T) {
	s := completionServer(t)

	// Multi-line tag style: the attribute sits on its own indented line.
	names := labels(s.ghtmxCompletionsFromLine("		hx-sw", 7))
	if len(names) == 0 || names[0] != "hx-swap" {
		t.Errorf("indented attribute lines must complete, got %v", names)
	}

	// Single-quoted values complete like double-quoted ones.
	vals := labels(s.ghtmxCompletionsFromLine(`	<div hx-swap='inner`, 20))
	if len(vals) != 1 || vals[0] != "innerHTML" {
		t.Errorf("single-quoted values must complete, got %v", vals)
	}

	// The htmx namespace is not the declared-event registry.
	if got := s.ghtmxCompletionsFromLine(`	<div hx-on::af`, 15); len(got) != 0 {
		t.Errorf("hx-on:: must not offer declared events, got %v", labels(got))
	}
	// The dash form reaches declared events.
	dash := labels(s.ghtmxCompletionsFromLine(`	<div hx-on-item`, 16))
	if len(dash) != 1 || dash[0] != "item-saved" {
		t.Errorf("hx-on- dash form must offer declared events, got %v", dash)
	}

	// Surface-enumerated values are data-driven.
	enc := labels(s.ghtmxCompletionsFromLine(`	<div hx-encoding="`, 19))
	if len(enc) == 0 {
		t.Errorf("surface-enumerated values must complete, got %v", enc)
	}
}

// TestAnyVerbRoutesOffered: method-less registrations are valid bindings
// for every verb and must appear in completion.
func TestAnyVerbRoutesOffered(t *testing.T) {
	s := completionServer(t)
	table, _, _ := s.routeState()
	table.Add(routes.Route{Verb: routes.AnyVerb, Path: "/anything", Handler: routes.SymbolRef{PkgPath: "example.com/app/handlers", Name: "Anything"}})

	items := labels(s.routeBindingCompletions(detectCompletionContext(`	<a hx-get={ `)))
	found := false
	for _, l := range items {
		if l == "handlers.Anything" {
			found = true
		}
	}
	if !found {
		t.Errorf("AnyVerb routes must be offered for every verb, got %v", items)
	}
}
