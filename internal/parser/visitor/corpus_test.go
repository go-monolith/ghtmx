package visitor_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-monolith/ghtmx/internal/parser"
	"github.com/go-monolith/ghtmx/internal/parser/visitor"
)

// New() builds 34 default closures, one per node kind, each recursing
// into that node's children. Nothing exercised most of them: a visitor
// whose default for some node kind forgot to recurse would silently
// stop the walk there, and every consumer — the analyzer, the LSP's
// symbol scan — would quietly see a subtree that is not there.
//
// So walk the whole committed corpus with a counting visitor (does
// every node kind get reached?) and again with one that fails (does the
// error come back out, or is the walk swallowing it?).

func corpusRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the corpus test source file")
	}
	// file = <repo>/internal/parser/visitor/corpus_test.go
	return filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
}

func corpusTemplates(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(corpusRoot(t), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".ghtmx" {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 40 {
		t.Fatalf("found only %d .ghtmx files; the walk is missing the corpus", len(out))
	}
	return out
}

func parseFile(t *testing.T, path string) *parser.TemplateFile {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseString(string(src))
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return parsed
}

// TestDefaultVisitorWalksTheWholeCorpus drives every default closure.
// The assertion is deliberately weak — no error, and some nodes seen —
// because the point is reaching all 34 defaults across 60 templates,
// which no single hand-written tree would do.
func TestDefaultVisitorWalksTheWholeCorpus(t *testing.T) {
	var total int
	for _, path := range corpusTemplates(t) {
		parsed := parseFile(t, path)

		var seen int
		v := visitor.New()
		// Count element visits without disturbing the recursion: the
		// default is captured and called through.
		defaultElement := v.Element
		v.Element = func(n *parser.Element) error {
			seen++
			return defaultElement(n)
		}

		if err := parsed.Visit(v); err != nil {
			t.Errorf("%s: walk failed: %v", path, err)
		}
		total += seen
	}
	if total == 0 {
		t.Error("the walk visited no elements across the whole corpus; the recursion is not happening")
	}
}

// TestVisitorErrorsPropagate is the property that matters: a visitor
// that returns an error must stop the walk and surface it. Swallowing
// it would make a failed analysis look like a clean one.
func TestVisitorErrorsPropagate(t *testing.T) {
	sentinel := errors.New("visitor refused")

	// Each case fails at a different node kind, so the error has to
	// travel back up through a different chain of default closures.
	tests := []struct {
		name    string
		install func(v *visitor.Visitor)
	}{
		{
			name: "element",
			install: func(v *visitor.Visitor) {
				v.Element = func(*parser.Element) error { return sentinel }
			},
		},
		{
			name: "text",
			install: func(v *visitor.Visitor) {
				v.Text = func(*parser.Text) error { return sentinel }
			},
		},
		{
			name: "package",
			install: func(v *visitor.Visitor) {
				v.Package = func(*parser.Package) error { return sentinel }
			},
		},
		{
			name: "html template",
			install: func(v *visitor.Visitor) {
				v.HTMLTemplate = func(*parser.HTMLTemplate) error { return sentinel }
			},
		},
	}

	paths := corpusTemplates(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var propagated int
			for _, path := range paths {
				parsed := parseFile(t, path)
				v := visitor.New()
				tt.install(v)

				if err := parsed.Visit(v); errors.Is(err, sentinel) {
					propagated++
				} else if err != nil {
					t.Errorf("%s: got %v, want the sentinel or nil", path, err)
				}
			}
			if propagated == 0 {
				t.Errorf("no template propagated a failure from the %s visitor; "+
					"either the node kind never occurs in the corpus or the error is being swallowed", tt.name)
			}
		})
	}
}

// TestVisitTemplateFileReachesHeaderAndPackage pins the top-level walk
// order, which the analyzer depends on: headers and the package clause
// are visited before any node.
func TestVisitTemplateFileReachesHeaderAndPackage(t *testing.T) {
	parsed, err := parser.ParseString("package main\n\ntempl hello() {\n\t<div>hi</div>\n}\n")
	if err != nil {
		t.Fatal(err)
	}

	var order []string
	v := visitor.New()
	defaultPackage := v.Package
	v.Package = func(n *parser.Package) error {
		order = append(order, "package")
		return defaultPackage(n)
	}
	defaultTemplate := v.HTMLTemplate
	v.HTMLTemplate = func(n *parser.HTMLTemplate) error {
		order = append(order, "template")
		return defaultTemplate(n)
	}

	if err := parsed.Visit(v); err != nil {
		t.Fatal(err)
	}

	if len(order) < 2 || order[0] != "package" {
		t.Errorf("visit order = %v, want the package clause first", order)
	}
}
