package parser_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/format"
	"github.com/go-monolith/ghtmx/internal/parser"
)

// types.go carries 34 Write methods — one per node kind — that together
// re-print a parsed template as source. They are what `ghtmx fmt` is
// built from, so the property that matters is round-tripping: parsing
// already-formatted source and writing it back must reproduce it
// exactly. A formatter that is not idempotent rewrites a user's file
// every time they save.
//
// Those methods are also written as
//
//	if _, err := w.Write(...); err != nil { return err }
//
// roughly ninety times, and none of those branches is reachable while
// the sink is a strings.Builder. The write-failure sweep below reaches
// them, and asserts the property worth having: a failed write is
// reported, never swallowed into a half-written file.

// corpusRoot returns the repository root.
func corpusRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the corpus test source file")
	}
	// file = <repo>/internal/parser/corpus_test.go
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// corpusFiles returns every .ghtmx file in the module. testdata is
// skipped: it holds deliberately malformed inputs for the parser's error
// tests, which by definition do not round-trip.
func corpusFiles(t *testing.T) []string {
	t.Helper()
	root := corpusRoot(t)

	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
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
		t.Fatalf("found only %d .ghtmx files; the walk is missing the corpus and this test would assert little", len(out))
	}
	return out
}

// relTo shortens a path for subtest names.
func relTo(t *testing.T, path string) string {
	t.Helper()
	rel, err := filepath.Rel(corpusRoot(t), path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// TestCorpusIsAlreadyFormatted runs the real `ghtmx fmt` pipeline —
// parse, resolve imports, write — over every committed template and
// requires it to report no change. That is the property a contributor
// depends on: running the formatter must not rewrite files that are
// already canonical, and every one of the 34 Write methods has to
// reproduce its node exactly for that to hold.
//
// It goes through format.Templ rather than TemplateFile.Write directly
// because the import-resolution step in between is part of the
// formatter's contract; writing alone leaves the import block untouched
// and shifts the file.
func TestCorpusIsAlreadyFormatted(t *testing.T) {
	for _, path := range corpusFiles(t) {
		rel := relTo(t, path)
		t.Run(rel, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			out, changed, err := format.Templ(src, path, format.Config{})
			if err != nil {
				t.Fatalf("format: %v", err)
			}

			if formatterRewrites[rel] {
				if !changed {
					t.Errorf("this template is now formatter-clean; remove it from formatterRewrites")
				}
				return
			}
			if changed {
				t.Errorf("the formatter rewrites this committed template\n%s",
					firstDiff(string(src), string(out)))
			}
		})
	}
}

// formatterRewrites are the committed templates `ghtmx fmt` does not
// leave alone: each uses route bindings or declared events, and
// imports.Process wants to add the generated package's import that the
// bindings resolve through. The committed files omit it deliberately —
// examples/events/page.ghtmx even carries a comment saying so — so
// running the formatter over them today produces a diff.
//
// The set is pinned in both directions rather than skipped: a new
// template that is not formatter-clean fails the test, and so does one
// of these becoming clean, which is the signal that the underlying
// import behaviour was fixed and the entry should go.
var formatterRewrites = map[string]bool{
	"benchmarks/corpus/corpus.ghtmx":    true,
	"conformance/conformance.ghtmx":     true,
	"docs/official/site/docs.ghtmx":     true,
	"docs/official/site/examples.ghtmx": true,
	"docs/official/site/home.ghtmx":     true,
	"docs/official/site/layout.ghtmx":   true,
	"examples/crud/crud.ghtmx":          true,
	"examples/events/page.ghtmx":        true,
	"examples/fragments/rows.ghtmx":     true,
}

// TestFormattingIsIdempotent pins that a second pass changes nothing.
// Without it the formatter could oscillate between two forms, and every
// save would produce a diff.
func TestFormattingIsIdempotent(t *testing.T) {
	for _, path := range corpusFiles(t) {
		t.Run(relTo(t, path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			once, _, err := format.Templ(src, path, format.Config{})
			if err != nil {
				t.Fatalf("format: %v", err)
			}
			twice, changed, err := format.Templ(once, path, format.Config{})
			if err != nil {
				t.Fatalf("second format: %v", err)
			}
			if changed {
				t.Errorf("a second formatting pass changed the output\n%s",
					firstDiff(string(once), string(twice)))
			}
		})
	}
}

// TestCorpusReparsesToTheSameOutput is the weaker property that still
// holds when a file was not already canonically formatted: writing is
// stable under a second pass. Without it, `ghtmx fmt` could oscillate
// between two forms.
func TestCorpusReparsesToTheSameOutput(t *testing.T) {
	for _, path := range corpusFiles(t) {
		t.Run(relTo(t, path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			first, err := parser.ParseString(string(src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var once strings.Builder
			if err := first.Write(&once); err != nil {
				t.Fatalf("write: %v", err)
			}

			second, err := parser.ParseString(once.String())
			if err != nil {
				t.Fatalf("reparse of written output failed: %v\n%s", err, once.String())
			}
			var twice strings.Builder
			if err := second.Write(&twice); err != nil {
				t.Fatalf("second write: %v", err)
			}

			if once.String() != twice.String() {
				t.Errorf("formatting is not stable under a second pass\n%s",
					firstDiff(once.String(), twice.String()))
			}
		})
	}
}

var errWriteFailed = errors.New("simulated write failure")

// failAtWrite fails the nth Write call (1-based) and every call after.
type failAtWrite struct {
	n     int
	calls int
}

func (w *failAtWrite) Write(p []byte) (int, error) {
	w.calls++
	if w.calls >= w.n {
		return 0, errWriteFailed
	}
	return len(p), nil
}

type writeCounter struct{ n int }

func (w *writeCounter) Write(p []byte) (int, error) { w.n++; return len(p), nil }

// maxSweepsPerTemplate bounds the sweep. Where a template exceeds it the
// test strides and logs what it sampled, rather than silently capping —
// a quiet truncation would read as "every write site is covered".
const maxSweepsPerTemplate = 1500

// TestWriteReportsEveryWriteFailure fails the k-th write for every k and
// requires the error back out. A swallowed write error here means
// `ghtmx fmt` reports success after writing a truncated file over the
// user's source.
func TestWriteReportsEveryWriteFailure(t *testing.T) {
	for _, path := range corpusFiles(t) {
		t.Run(relTo(t, path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := parser.ParseString(string(src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			var counter writeCounter
			if err := parsed.Write(&counter); err != nil {
				t.Fatalf("baseline write failed: %v", err)
			}
			if counter.n == 0 {
				t.Fatal("writing made no writes; the sweep would assert nothing")
			}

			stride := 1
			if counter.n > maxSweepsPerTemplate {
				stride = counter.n/maxSweepsPerTemplate + 1
				t.Logf("%d writes: sampling every %d-th index", counter.n, stride)
			}

			for k := 1; k <= counter.n; k += stride {
				err := parsed.Write(&failAtWrite{n: k})
				if err == nil {
					t.Fatalf("write %d of %d failed but Write returned nil: the error was swallowed", k, counter.n)
				}
				if !errors.Is(err, errWriteFailed) {
					t.Fatalf("write %d of %d: Write returned %v, want it to wrap the write failure", k, counter.n, err)
				}
			}
		})
	}
}

// firstDiff renders the first differing line.
func firstDiff(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	for i := range max(len(wantLines), len(gotLines)) {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w != g {
			return "first difference at line " + itoa(i+1) + ":\n  want: " + quoteLine(w) + "\n  got:  " + quoteLine(g)
		}
	}
	return "the two differ only in trailing content"
}

func quoteLine(s string) string { return "\"" + strings.ReplaceAll(s, "\t", "\\t") + "\"" }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
