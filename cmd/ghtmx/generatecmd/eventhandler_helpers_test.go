package generatecmd

import (
	"bytes"
	"errors"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-monolith/ghtmx/internal/parser"
)

func TestWriterFileWriter(t *testing.T) {
	var buf bytes.Buffer
	write := WriterFileWriter(&buf)

	if err := write("ignored/name.go", []byte("generated")); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "generated" {
		t.Errorf("wrote %q, want %q", got, "generated")
	}
}

// failingWriter is how the -stdout path reports a closed pipe.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestWriterFileWriterReportsWriteFailures(t *testing.T) {
	sentinel := errors.New("pipe closed")

	err := WriterFileWriter(failingWriter{err: sentinel})("name.go", []byte("x"))
	if !errors.Is(err, sentinel) {
		t.Errorf("got %v, want it to wrap %v", err, sentinel)
	}
}

// goFileIsUpToDate decides whether a template is regenerated at all, so
// getting it wrong either wastes the whole build or — worse — silently
// serves stale output from a template the user just edited.
func TestGoFileIsUpToDate(t *testing.T) {
	dir := t.TempDir()
	templPath := filepath.Join(dir, "page.ghtmx")
	goPath := filepath.Join(dir, "page_ghtmx.go")

	edited := time.Now()

	tests := []struct {
		name        string
		writeGoFile bool
		goModTime   time.Time
		want        bool
	}{
		{
			name:        "no generated file yet",
			writeGoFile: false,
			want:        false,
		},
		{
			// The generated file predates the edit: stale, regenerate.
			name:        "generated before the template was edited",
			writeGoFile: true,
			goModTime:   edited.Add(-time.Hour),
			want:        false,
		},
		{
			name:        "generated after the template was edited",
			writeGoFile: true,
			goModTime:   edited.Add(time.Hour),
			want:        true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Remove(goPath)
			if tt.writeGoFile {
				if err := os.WriteFile(goPath, []byte("package main\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(goPath, tt.goModTime, tt.goModTime); err != nil {
					t.Fatal(err)
				}
			}

			if got := goFileIsUpToDate(templPath, "_ghtmx.go", edited); got != tt.want {
				t.Errorf("goFileIsUpToDate = %v, want %v", got, tt.want)
			}
		})
	}
}

// remapErrorList translates a compile error in generated Go back to the
// .ghtmx line the user actually wrote. Without it, an error points at a
// file the user never opened, at a line number that means nothing to
// them.
func TestRemapErrorList(t *testing.T) {
	sm := parser.NewSourceMap()
	sm.Add(
		parser.Expression{
			Value: "name",
			Range: parser.Range{
				From: parser.Position{Index: 42, Line: 7, Col: 3},
				To:   parser.Position{Index: 46, Line: 7, Col: 7},
			},
		},
		parser.Range{
			From: parser.Position{Index: 100, Line: 20, Col: 3},
			To:   parser.Position{Index: 104, Line: 20, Col: 7},
		},
	)

	// The generated position is one line further on than the source map
	// records, because of the package clause.
	var list scanner.ErrorList
	list.Add(token.Position{Filename: "page_ghtmx.go", Line: 21, Column: 3}, "undefined: name")

	got := remapErrorList(list, sm, "page.ghtmx")

	remapped, ok := got.(scanner.ErrorList)
	if !ok {
		t.Fatalf("remapErrorList returned %T, want scanner.ErrorList", got)
	}
	if len(remapped) != 1 {
		t.Fatalf("got %d errors, want 1", len(remapped))
	}
	if remapped[0].Pos.Filename != "page.ghtmx" {
		t.Errorf("filename = %q, want the template the user edited", remapped[0].Pos.Filename)
	}
	if remapped[0].Pos.Line != 8 {
		t.Errorf("line = %d, want 8 (source line 7, reported one-based)", remapped[0].Pos.Line)
	}
}

// TestRemapErrorListPassesThroughOtherErrors pins that an error which is
// not a Go error list is returned untouched, rather than being swallowed
// or mangled into an empty list.
func TestRemapErrorListPassesThroughOtherErrors(t *testing.T) {
	sentinel := errors.New("not a scanner.ErrorList")

	if got := remapErrorList(sentinel, parser.NewSourceMap(), "page.ghtmx"); !errors.Is(got, sentinel) {
		t.Errorf("got %v, want the original error back", got)
	}
	if got := remapErrorList(scanner.ErrorList{}, parser.NewSourceMap(), "page.ghtmx"); got == nil {
		t.Error("an empty error list came back nil")
	}
}

// TestRemapErrorListLeavesUnmappedPositionsAlone covers the branch where
// the generated position has no counterpart in the template — an error
// in code the compiler emitted rather than code the user wrote. Pointing
// that at an arbitrary template line would be worse than leaving it.
func TestRemapErrorListLeavesUnmappedPositionsAlone(t *testing.T) {
	var list scanner.ErrorList
	list.Add(token.Position{Filename: "page_ghtmx.go", Line: 999, Column: 1}, "undefined: helper")

	got := remapErrorList(list, parser.NewSourceMap(), "page.ghtmx").(scanner.ErrorList)

	if got[0].Pos.Filename != "page_ghtmx.go" {
		t.Errorf("filename = %q, want the generated file left in place", got[0].Pos.Filename)
	}
	if got[0].Pos.Line != 999 {
		t.Errorf("line = %d, want 999 unchanged", got[0].Pos.Line)
	}
}
