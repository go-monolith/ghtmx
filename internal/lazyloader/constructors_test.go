package lazyloader

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// New wires the loader's four collaborators together, and the two thin
// adapters below are the only places it touches the filesystem and the
// Go parser. They are trivial, which is exactly why nothing exercised
// them — and a nil map or a mis-wired dependency here surfaces as the
// LSP failing to open any document at all.

func TestNewWiresTheLoader(t *testing.T) {
	loader := New(NewParams{
		TemplDocHandler: nil,
		OpenDocSources:  map[string]string{},
	})
	if loader == nil {
		t.Fatal("New returned nil")
	}

	// The concrete type carries four maps that are written to on the
	// first document open; a nil one panics there rather than here.
	l, ok := loader.(*templDocLazyLoader)
	if !ok {
		t.Fatalf("New returned %T, want *templDocLazyLoader", loader)
	}
	if l.loadedPkgs == nil {
		t.Error("loadedPkgs is nil; the first package load would panic")
	}
	if l.openDocHeaders == nil {
		t.Error("openDocHeaders is nil; the first document open would panic")
	}
	if l.docsPendingLoad == nil {
		t.Error("docsPendingLoad is nil")
	}
	if l.pkgLoader == nil || l.pkgTraverser == nil || l.docHeaderParser == nil {
		t.Error("a collaborator was left unwired")
	}
}

// TestNewWithNilSources pins the case where no document is open yet,
// which is how the LSP starts.
func TestNewWithNilSources(t *testing.T) {
	loader := New(NewParams{})
	if loader == nil {
		t.Fatal("New returned nil for empty params")
	}
}

func TestTemplFileReaderReadsFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "page.ghtmx")
	if err := os.WriteFile(path, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reader := templFileReader{}
	got, err := reader.read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "package p\n" {
		t.Errorf("read returned %q, want %q", got, "package p\n")
	}
}

// TestTemplFileReaderReportsAMissingFile pins that a deleted template
// surfaces rather than being read as empty, which would make the loader
// believe the file has no declarations.
func TestTemplFileReaderReportsAMissingFile(t *testing.T) {
	reader := templFileReader{}
	if _, err := reader.read(filepath.Join(t.TempDir(), "absent.ghtmx")); err == nil {
		t.Error("read succeeded on a file that does not exist")
	}
}

func TestGoFileParserParsesSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nimport \"strings\"\n\nvar _ = strings.ToUpper\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fp := goFileParser{}
	f, err := fp.parseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if f.Name.Name != "main" {
		t.Errorf("package name = %q, want main", f.Name.Name)
	}
	if len(f.Imports) != 1 {
		t.Errorf("parsed %d imports, want 1", len(f.Imports))
	}
}

// TestGoFileParserUsesTheOverlay pins the unsaved-buffer path: the LSP
// hands the parser the editor's in-memory text, and reading from disk
// instead would resolve imports the user has already deleted.
func TestGoFileParserUsesTheOverlay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package ondisk\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fp := goFileParser{}
	f, err := fp.parseFile(token.NewFileSet(), path, "package inmemory\n", parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if f.Name.Name != "inmemory" {
		t.Errorf("package name = %q, want the overlay's inmemory", f.Name.Name)
	}
}

func TestGoFileParserReportsAMissingFile(t *testing.T) {
	fp := goFileParser{}
	_, err := fp.parseFile(token.NewFileSet(),
		filepath.Join(t.TempDir(), "absent.go"), nil, parser.ImportsOnly)
	if err == nil {
		t.Error("parseFile succeeded on a file that does not exist")
	}
}
