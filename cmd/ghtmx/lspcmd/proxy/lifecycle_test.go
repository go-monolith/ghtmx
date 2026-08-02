package proxy

import (
	"context"
	"strings"
	"testing"

	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
)

// The document-sync methods are the proxy's spine: everything else
// depends on the server holding the same text the editor does. Opening a
// real template and driving it through open → change → save → close
// exercises the parse, the source-map rebuild, the diagnostic publish
// and the cache eviction together, which is how they actually run.

const lifecycleURI = "file:///project/lifecycle.ghtmx"

const lifecycleSource = `package project

templ Greeting(name string) {
	<div>{ name }</div>
}
`

// openDocument drives a DidOpen through the proxy with a live client.
func openDocument(t *testing.T, s *Server, client *recordingClient, uri, text string) context.Context {
	t.Helper()
	ctx := lsp.WithClient(context.Background(), client)
	err := s.DidOpen(ctx, &lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        lsp.DocumentURI(uri),
			LanguageID: "ghtmx",
			Version:    1,
			Text:       text,
		},
	})
	if err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	return ctx
}

func TestDocumentLifecycle(t *testing.T) {
	client := &recordingClient{}
	s := newTestServer(&mockServer{})
	ctx := openDocument(t, s, client, lifecycleURI, lifecycleSource)

	// After opening, the server holds the text and a source map: without
	// both, every position it reports afterwards is guesswork.
	doc, ok := s.TemplSource.Get(lifecycleURI)
	if !ok {
		t.Fatal("the opened document is not in TemplSource")
	}
	if !strings.Contains(doc.String(), "Greeting") {
		t.Errorf("stored text does not match what was opened:\n%s", doc.String())
	}
	if _, ok := s.SourceMapCache.Get(lifecycleURI); !ok {
		t.Error("no source map was built for the opened document")
	}
	if _, ok := s.GoSource[lifecycleURI]; !ok {
		t.Error("no generated Go was recorded for the opened document")
	}

	// Editing replaces the text the server holds.
	err := s.DidChange(ctx, &lsp.DidChangeTextDocumentParams{
		TextDocument: lsp.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: lsp.TextDocumentIdentifier{URI: lifecycleURI},
			Version:                2,
		},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{
			{Text: strings.Replace(lifecycleSource, "Greeting", "Welcome", 1)},
		},
	})
	if err != nil {
		t.Fatalf("DidChange: %v", err)
	}
	doc, _ = s.TemplSource.Get(lifecycleURI)
	if !strings.Contains(doc.String(), "Welcome") {
		t.Errorf("the edit did not reach the stored document:\n%s", doc.String())
	}

	// Saving must not disturb what is held.
	if err := s.DidSave(ctx, &lsp.DidSaveTextDocumentParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: lifecycleURI},
	}); err != nil {
		t.Fatalf("DidSave: %v", err)
	}
	if _, ok := s.TemplSource.Get(lifecycleURI); !ok {
		t.Error("the document disappeared on save")
	}

	// Closing evicts it, or a stale buffer outlives the editor tab.
	if err := s.DidClose(ctx, &lsp.DidCloseTextDocumentParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: lifecycleURI},
	}); err != nil {
		t.Fatalf("DidClose: %v", err)
	}
	if _, ok := s.TemplSource.Get(lifecycleURI); ok {
		t.Error("the document survived DidClose; a stale buffer would outlive the editor tab")
	}
}

// TestDidOpenPublishesDiagnostics pins that opening a template reports
// its problems. A parse failure that produced no diagnostic would leave
// the editor showing a clean file that will not compile.
func TestDidOpenPublishesDiagnostics(t *testing.T) {
	client := &recordingClient{}
	s := newTestServer(&mockServer{})

	// Unterminated element: the parser rejects it.
	openDocument(t, s, client, lifecycleURI, "package project\n\ntempl X() {\n\t<div>\n}\n")

	if len(client.published) == 0 {
		t.Fatal("opening an unparseable template published no diagnostics")
	}
	var found bool
	for _, p := range client.published {
		if string(p.URI) == lifecycleURI && len(p.Diagnostics) > 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("no diagnostics were published against %s: %+v", lifecycleURI, client.published)
	}
}

// TestDidOpenOnAValidTemplateClearsDiagnostics is the other half: a
// clean file has to publish an empty set, or a fixed error stays on
// screen forever.
func TestDidOpenOnAValidTemplateClearsDiagnostics(t *testing.T) {
	client := &recordingClient{}
	s := newTestServer(&mockServer{})

	openDocument(t, s, client, lifecycleURI, lifecycleSource)

	if len(client.published) == 0 {
		t.Fatal("opening a valid template published nothing; a previous error would never clear")
	}
	last := client.published[len(client.published)-1]
	if len(last.Diagnostics) != 0 {
		t.Errorf("a valid template published %d diagnostics: %+v", len(last.Diagnostics), last.Diagnostics)
	}
}

// TestDidChangeToAnInvalidTemplateReportsIt covers the edit path's error
// branch — the common case of typing mid-edit.
func TestDidChangeToAnInvalidTemplateReportsIt(t *testing.T) {
	client := &recordingClient{}
	s := newTestServer(&mockServer{})
	ctx := openDocument(t, s, client, lifecycleURI, lifecycleSource)

	before := len(client.published)
	err := s.DidChange(ctx, &lsp.DidChangeTextDocumentParams{
		TextDocument: lsp.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: lsp.TextDocumentIdentifier{URI: lifecycleURI},
			Version:                2,
		},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{
			{Text: "package project\n\ntempl X() {\n\t<div>\n}\n"},
		},
	})
	if err != nil {
		t.Fatalf("DidChange: %v", err)
	}
	if len(client.published) <= before {
		t.Error("an edit that broke the template published no new diagnostics")
	}
}

// TestDidCloseOnAnUnopenedDocument pins that closing something never
// opened is harmless — editors do send this after a crash.
func TestDidCloseOnAnUnopenedDocument(t *testing.T) {
	s := newTestServer(&mockServer{})
	ctx := lsp.WithClient(context.Background(), &recordingClient{})

	if err := s.DidClose(ctx, &lsp.DidCloseTextDocumentParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///project/never-opened.ghtmx"},
	}); err != nil {
		t.Errorf("DidClose on an unopened document returned %v, want nil", err)
	}
}

// TestNonTemplDocumentsPassStraightThrough pins that a plain .go file
// opened in the editor is forwarded to gopls untouched rather than being
// parsed as a template.
func TestNonTemplDocumentsPassStraightThrough(t *testing.T) {
	const goURI = "file:///project/main.go"
	client := &recordingClient{}
	s := newTestServer(&mockServer{})

	ctx := lsp.WithClient(context.Background(), client)
	err := s.DidOpen(ctx, &lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        goURI,
			LanguageID: "go",
			Version:    1,
			Text:       "package main\n\nfunc main() {}\n",
		},
	})
	if err != nil {
		t.Fatalf("DidOpen on a .go file: %v", err)
	}
	if _, ok := s.TemplSource.Get(goURI); ok {
		t.Error("a .go file was stored as template source")
	}
}
