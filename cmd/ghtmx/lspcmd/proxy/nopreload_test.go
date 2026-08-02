package proxy

import (
	"context"
	"testing"

	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
)

// -no-preload exists for monorepos where scanning every template at
// startup is too slow. It changes what DidClose has to do: with preload
// on, the server keeps the document because something else may still
// reference it; with preload off, closing is the only signal to release
// it, and holding on would leak a buffer per file the user ever opened.
//
// The lazy loader these paths depend on is built during Initialize, so
// every case here initialises first — which is also the only order the
// LSP protocol permits.

// newNoPreloadServer returns an initialised server in -no-preload mode.
func newNoPreloadServer(t *testing.T) (*Server, context.Context) {
	t.Helper()
	s := newTestServer(&mockServer{})
	s.NoPreload = true
	ctx := lsp.WithClient(context.Background(), &recordingClient{})
	if _, err := s.Initialize(ctx, &lsp.InitializeParams{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return s, ctx
}

func TestDidOpenAndCloseUnderNoPreload(t *testing.T) {
	s, ctx := newNoPreloadServer(t)

	doc := lsp.TextDocumentItem{
		URI: lifecycleURI, LanguageID: "ghtmx", Version: 1, Text: lifecycleSource,
	}
	if err := s.DidOpen(ctx, &lsp.DidOpenTextDocumentParams{TextDocument: doc}); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	// The loader resolves through go/packages, so a template that is
	// not part of a module on disk loads to nothing — which is itself
	// the contract worth pinning: an unresolvable file must be a quiet
	// no-op rather than an error that blocks the editor.

	if err := s.DidClose(ctx, &lsp.DidCloseTextDocumentParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: lifecycleURI},
	}); err != nil {
		t.Fatalf("DidClose: %v", err)
	}

	// Closing something the loader never held must also be quiet: an
	// editor closing tabs on shutdown would otherwise produce an error
	// for every file it could not resolve.
	if s.templDocLazyLoader.HasLoaded(lsp.TextDocumentIdentifier{URI: lifecycleURI}) {
		t.Error("the document is still held after DidClose")
	}
}

// TestDidCloseWithNoPreloadRejectsABadURI pins the parse guard on that
// path: an unparseable URI has to be reported rather than silently
// releasing nothing.
func TestDidCloseWithNoPreloadRejectsABadURI(t *testing.T) {
	s, ctx := newNoPreloadServer(t)

	err := s.DidClose(ctx, &lsp.DidCloseTextDocumentParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "://not a uri"},
	})
	if err == nil {
		t.Error("DidClose accepted an unparseable URI")
	}
}

// TestDocumentLinkResolve covers the link target rewrite: a document
// link pointing at a .ghtmx has to be resolved through its generated Go,
// or ctrl-clicking an import in a template goes nowhere.
func TestDocumentLinkResolve(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		wantErr bool
	}{
		{name: "templ target", target: "file:///project/component.ghtmx"},
		{name: "go target", target: "file:///project/main.go"},
		{name: "unparseable target", target: "://not a uri", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(&mockServer{})
			ctx := lsp.WithClient(context.Background(), &recordingClient{})

			_, err := s.DocumentLinkResolve(ctx, &lsp.DocumentLink{Target: lsp.URI(tt.target)})
			if tt.wantErr && err == nil {
				t.Error("DocumentLinkResolve accepted an unparseable target")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("DocumentLinkResolve returned %v, want nil", err)
			}
		})
	}
}
