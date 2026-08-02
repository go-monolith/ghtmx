package proxy

import (
	"context"
	"testing"

	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
	"github.com/go-monolith/ghtmx/internal/parser"
)

// When gopls dies the proxy has to rebuild its session from scratch:
// replay the initialize params, then re-open every generated Go document
// (FR-085). If either snapshot is wrong the replacement gopls has a
// different view of the workspace than the editor does, and the symptom
// is Go features that quietly stop working until the editor restarts.

func TestLastInitializeParamsIsRecorded(t *testing.T) {
	s := newTestServer(&mockServer{})

	// Before initialize there is nothing to replay.
	if got := s.LastInitializeParams(); got != nil {
		t.Errorf("LastInitializeParams before initialize = %+v, want nil", got)
	}

	want := &lsp.InitializeParams{
		WorkspaceFolders: []lsp.WorkspaceFolder{
			{URI: "file:///project", Name: "project"},
		},
	}
	if _, err := s.Initialize(context.Background(), want); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	got := s.LastInitializeParams()
	if got == nil {
		t.Fatal("the initialize params were not recorded; a gopls restart could not replay the session")
	}
	if len(got.WorkspaceFolders) != 1 || got.WorkspaceFolders[0].URI != want.WorkspaceFolders[0].URI {
		t.Errorf("WorkspaceFolders = %+v, want %+v", got.WorkspaceFolders, want.WorkspaceFolders)
	}
}

func TestOpenGoDocumentsSnapshotsEveryOpenTemplate(t *testing.T) {
	client := &recordingClient{}
	s := newTestServer(&mockServer{})

	// Nothing open yet.
	if got := s.OpenGoDocuments(); len(got) != 0 {
		t.Errorf("OpenGoDocuments with nothing open returned %d items, want 0", len(got))
	}

	openDocument(t, s, client, lifecycleURI, lifecycleSource)

	items := s.OpenGoDocuments()
	if len(items) != 1 {
		t.Fatalf("OpenGoDocuments returned %d items, want 1", len(items))
	}
	item := items[0]
	// gopls only understands the generated Go file, so the replayed URI
	// has to be the .go one, not the template.
	if got := string(item.URI); got != "file:///project/lifecycle_ghtmx.go" {
		t.Errorf("replayed URI = %q, want the generated Go file", got)
	}
	if item.LanguageID != "go" {
		t.Errorf("LanguageID = %q, want go", item.LanguageID)
	}
	if item.Text == "" {
		t.Error("the replayed document has no text; the replacement gopls would see an empty file")
	}
}

// TestOpenGoDocumentsDropsClosedTemplates pins that the snapshot follows
// the editor: replaying a document the user closed would leave gopls
// holding a file that is no longer open.
func TestOpenGoDocumentsDropsClosedTemplates(t *testing.T) {
	client := &recordingClient{}
	s := newTestServer(&mockServer{})
	ctx := openDocument(t, s, client, lifecycleURI, lifecycleSource)

	if err := s.DidClose(ctx, &lsp.DidCloseTextDocumentParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: lifecycleURI},
	}); err != nil {
		t.Fatal(err)
	}

	if got := s.OpenGoDocuments(); len(got) != 0 {
		t.Errorf("a closed template is still replayed: %+v", got)
	}
}

func TestIsRangeWithin(t *testing.T) {
	parent := lsp.Range{
		Start: lsp.Position{Line: 2, Character: 4},
		End:   lsp.Position{Line: 6, Character: 10},
	}
	tests := []struct {
		name  string
		child lsp.Range
		want  bool
	}{
		{
			name:  "strictly inside",
			child: lsp.Range{Start: lsp.Position{Line: 3}, End: lsp.Position{Line: 4}},
			want:  true,
		},
		{
			name:  "identical",
			child: parent,
			want:  true,
		},
		{
			name:  "starts on the parent's line but earlier in it",
			child: lsp.Range{Start: lsp.Position{Line: 2, Character: 1}, End: lsp.Position{Line: 3}},
			want:  false,
		},
		{
			name:  "ends on the parent's line but later in it",
			child: lsp.Range{Start: lsp.Position{Line: 3}, End: lsp.Position{Line: 6, Character: 20}},
			want:  false,
		},
		{
			name:  "starts before the parent",
			child: lsp.Range{Start: lsp.Position{Line: 1}, End: lsp.Position{Line: 3}},
			want:  false,
		},
		{
			name:  "ends after the parent",
			child: lsp.Range{Start: lsp.Position{Line: 3}, End: lsp.Position{Line: 9}},
			want:  false,
		},
		{
			name:  "starts exactly at the parent's start",
			child: lsp.Range{Start: lsp.Position{Line: 2, Character: 4}, End: lsp.Position{Line: 3}},
			want:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRangeWithin(parent, tt.child); got != tt.want {
				t.Errorf("isRangeWithin = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestConvertSymbolRange rewrites a document symbol's range from
// generated Go back into the template. Without it the outline view
// highlights the wrong lines, which reads as an editor that has lost
// track of the file.
func TestConvertSymbolRange(t *testing.T) {
	const templURI = "file:///project/symbols.ghtmx"

	t.Run("a mapped symbol is rewritten", func(t *testing.T) {
		s := newTestServer(&mockServer{})
		sm := parser.NewSourceMap()
		sm.AddSymbolRange(
			parser.Range{
				From: parser.Position{Line: 3, Col: 0},
				To:   parser.Position{Line: 5, Col: 1},
			},
			parser.Range{
				From: parser.Position{Line: 12, Col: 0},
				To:   parser.Position{Line: 20, Col: 1},
			},
		)
		s.SourceMapCache.Set(templURI, sm)

		symbol := &lsp.DocumentSymbol{
			Name:  "Page",
			Range: lsp.Range{Start: lsp.Position{Line: 12}, End: lsp.Position{Line: 20, Character: 1}},
		}
		s.convertSymbolRange(lsp.DocumentURI(templURI), symbol)

		if symbol.Range.Start.Line != 3 {
			t.Errorf("start line = %d, want 3 — the symbol was not mapped back into the template", symbol.Range.Start.Line)
		}
	})

	t.Run("no source map leaves the symbol alone", func(t *testing.T) {
		s := newTestServer(&mockServer{})
		original := lsp.Range{Start: lsp.Position{Line: 12}, End: lsp.Position{Line: 20}}
		symbol := &lsp.DocumentSymbol{Name: "Page", Range: original}

		s.convertSymbolRange("file:///project/never-opened.ghtmx", symbol)

		// Guessing a position would send the outline somewhere wrong;
		// leaving it is the safer failure.
		if symbol.Range != original {
			t.Errorf("range = %+v, want it untouched at %+v", symbol.Range, original)
		}
	})

	t.Run("an unmapped position leaves the symbol alone", func(t *testing.T) {
		s := newTestServer(&mockServer{})
		s.SourceMapCache.Set(templURI, parser.NewSourceMap())
		original := lsp.Range{Start: lsp.Position{Line: 900}, End: lsp.Position{Line: 901}}
		symbol := &lsp.DocumentSymbol{Name: "Generated", Range: original}

		s.convertSymbolRange(lsp.DocumentURI(templURI), symbol)

		if symbol.Range != original {
			t.Errorf("range = %+v, want it untouched at %+v", symbol.Range, original)
		}
	})
}

// TestDiagnosticCacheClearsTemplDiagnostics pins the eviction that makes
// a fixed template stop showing its old errors. Without it a diagnostic
// the user has already fixed stays on screen until the editor restarts.
//
// Note the parameter names run opposite to what they store:
// AddGoDiagnostics takes the *templ* diagnostics and merges them with
// the cached gopls ones, and AddTemplDiagnostics is the mirror. Each is
// named for the side it is being called from.
func TestDiagnosticCacheClearsTemplDiagnostics(t *testing.T) {
	const uri = "file:///project/page.ghtmx"
	cache := NewDiagnosticCache()

	// The templ side reports a parse error.
	merged := cache.AddGoDiagnostics(uri, []lsp.Diagnostic{{Message: "parse error"}})
	if len(merged) != 1 || merged[0].Message != "parse error" {
		t.Fatalf("merged = %+v, want the single templ diagnostic", merged)
	}

	// The user fixes it, so the templ side clears.
	cache.ClearTemplDiagnostics(uri)

	// gopls now reports something of its own: only that should surface.
	merged = cache.AddTemplDiagnostics(uri, []lsp.Diagnostic{{Message: "undefined: x"}})
	for _, d := range merged {
		if d.Message == "parse error" {
			t.Errorf("a cleared templ diagnostic came back; the fixed error stays on screen: %+v", merged)
		}
	}
	if len(merged) != 1 {
		t.Errorf("merged = %+v, want only the gopls diagnostic", merged)
	}
}

// TestDiagnosticCacheClearOnAnUnknownURI pins that clearing a document
// the cache never saw is harmless — it happens whenever a file is closed
// before it was ever diagnosed — and that it does not disturb another
// document's entry.
func TestDiagnosticCacheClearOnAnUnknownURI(t *testing.T) {
	const kept = "file:///project/kept.ghtmx"
	cache := NewDiagnosticCache()
	cache.AddGoDiagnostics(kept, []lsp.Diagnostic{{Message: "still relevant"}})

	cache.ClearTemplDiagnostics("file:///project/never-seen.ghtmx")

	merged := cache.AddTemplDiagnostics(kept, nil)
	if len(merged) != 1 || merged[0].Message != "still relevant" {
		t.Errorf("clearing an unknown URI disturbed another document: %+v", merged)
	}
}
