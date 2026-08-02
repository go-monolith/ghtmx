package proxy

import (
	"context"
	"reflect"
	"testing"

	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
)

// Nearly every method on this server branches three ways on the URI it
// is handed: a .ghtmx file it knows about (rewrite the position into
// generated Go and rewrite the answer back), a .ghtmx file it has never
// seen (no source map — answer without converting rather than pointing
// somewhere wrong), and anything else (pass straight through to gopls).
//
// The sweeps elsewhere cover the first. These cover the other two, which
// are the states a real session actually spends time in: a file opened
// before the server finished starting, and every ordinary .go file in
// the project.

// fillRequest sets the URI and any position fields a request carries, so
// the method under test takes its conversion path rather than bailing on
// an empty URI.
func fillRequest(v reflect.Value, uri string, line, char uint32) {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	setTextDocumentURI(v, uri)
	setPositions(v, line, char)
}

// setPositions walks one level into a request and fills Position and
// Range fields, which is what drives the position-conversion branches.
func setPositions(v reflect.Value, line, char uint32) {
	posType := reflect.TypeFor[lsp.Position]()
	rangeType := reflect.TypeFor[lsp.Range]()
	pos := lsp.Position{Line: line, Character: char}

	var fill func(reflect.Value, int)
	fill = func(v reflect.Value, depth int) {
		if depth > 3 || !v.IsValid() {
			return
		}
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			return
		}
		if v.Type() == posType && v.CanSet() {
			v.Set(reflect.ValueOf(pos))
			return
		}
		if v.Type() == rangeType && v.CanSet() {
			v.Set(reflect.ValueOf(lsp.Range{Start: pos, End: pos}))
			return
		}
		for i := range v.NumField() {
			if v.Field(i).CanSet() {
				fill(v.Field(i), depth+1)
			}
		}
	}
	fill(v, 0)
}

// mayFailOnUnknownDocument names the methods that legitimately return an
// error for a .ghtmx file the server has never seen. DidChange is the
// only one: it applies an edit to stored text, and there is none —
// inventing an empty document would silently discard the user's file.
// Formatting, by contrast, answers with no edits, which is the right
// response to "format something I am not holding".
var mayFailOnUnknownDocument = map[string]bool{
	"DidChange": true,
}

// sweepWithURI drives every lsp.Server method against the given URI.
// allowFailure names the methods for which an error is the correct
// answer rather than a defect.
func sweepWithURI(t *testing.T, uri string, allowFailure map[string]bool, prepare func(*Server)) {
	t.Helper()
	for _, name := range serverMethodNames() {
		if notImplementedByTheMock[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			s := newTestServer(&mockServer{})
			if prepare != nil {
				prepare(s)
			}
			ctx := lsp.WithClient(context.Background(), &recordingClient{})

			err := callMethodWith(t, s, name, ctx, func(arg reflect.Value) {
				fillRequest(arg, uri, 5, 10)
			})
			if err != nil && !allowFailure[name] {
				t.Errorf("%s returned %v for %q, want nil", name, err, uri)
			}
			if err == nil && allowFailure[name] {
				t.Errorf("%s succeeded for %q; it needs the document text and should have said so", name, uri)
			}
		})
	}
}

// TestEveryMethodHandlesAnUnknownTemplURI is the case a race produces:
// the editor sends a request for a .ghtmx file before didOpen has been
// processed, so there is no source map to convert through. Every method
// has to answer rather than panic on the missing entry.
func TestEveryMethodHandlesAnUnknownTemplURI(t *testing.T) {
	sweepWithURI(t, "file:///project/never-opened.ghtmx", mayFailOnUnknownDocument, nil)
}

// TestEveryMethodHandlesAPlainGoURI covers the passthrough branch, which
// is most of what a real session sends: the user is editing ordinary Go
// most of the time, and none of it should be treated as a template.
func TestEveryMethodHandlesAPlainGoURI(t *testing.T) {
	sweepWithURI(t, "file:///project/main.go", nil, nil)
}

// TestEveryMethodHandlesAKnownTemplURI drives the conversion path with a
// document open and a source map in place, at a position that maps.
func TestEveryMethodHandlesAKnownTemplURI(t *testing.T) {
	const uri = "file:///project/component.ghtmx"
	sweepWithURI(t, uri, nil, func(s *Server) {
		s.TemplSource.Set(uri, NewDocument(testLog(), "package project\n\ntempl X() {\n\t<div></div>\n}\n"))
		s.GoSource[uri] = "package project\n"
	})
}

// TestEveryMethodHandlesAnEmptyURI pins the degenerate case an editor
// sends on shutdown, when the document it refers to is already gone.
func TestEveryMethodHandlesAnEmptyURI(t *testing.T) {
	sweepWithURI(t, "", nil, nil)
}

// TestFormattingADocumentThatIsNotOpen is a regression test. The server
// discarded the ok from TemplSource.Get and then dereferenced the nil
// Document, so a format request for a .ghtmx file it was not holding —
// an editor formatting on save while didOpen is still in flight, or
// anything arriving after a gopls restart — panicked and took the whole
// language server down.
func TestFormattingADocumentThatIsNotOpen(t *testing.T) {
	s := newTestServer(&mockServer{})
	ctx := lsp.WithClient(context.Background(), &recordingClient{})

	result, err := s.Formatting(ctx, &lsp.DocumentFormattingParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///project/never-opened.ghtmx"},
	})
	if err != nil {
		t.Errorf("Formatting returned %v, want nil", err)
	}
	if len(result) != 0 {
		t.Errorf("Formatting returned %d edits for a document it is not holding, want none", len(result))
	}
}
