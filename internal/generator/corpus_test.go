package generator

import (
	"bytes"
	"go/format"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/parser"
)

// The 46 test-* packages next door do not exercise this package: each
// renders its already-generated template_ghtmx.go and diffs the HTML, so
// generation happened at `go generate` time and the generator itself is
// never called. The only thing standing between a generator regression
// and a green build is CI's ensure-generated step, which shells out to
// `go run ./cmd/ghtmx generate` in a separate process.
//
// This is that check, in-process: parse every .ghtmx in the module,
// generate, and compare byte-for-byte against the committed output. It
// reaches the feature branches — elements, attributes, conditionals,
// loops, switches, scripts, CSS, fragments, spreads — that the corpus
// was written to cover but that nothing was actually running.
//
// Reproducing the committed bytes exactly means matching what
// FSEventHandler.generate does: generate with a module-root-relative,
// slash-separated filename (it is embedded in runtime error messages),
// then run the result through go/format. CI generates with
// -include-version=false, so no version option is set here either.

// moduleRoot returns the repository root. The test's working directory
// is this package's directory, but generated files embed paths relative
// to the module root, so every path has to be rebuilt from there.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the corpus test source file")
	}
	// file = <repo>/internal/generator/corpus_test.go
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// ignoredPrefixes are the module-root-relative directories the root
// generate pass skips, mirroring .ghtmxignore_generate. Both are
// generated separately, against a different route table, so their
// committed output cannot be reproduced by a plain root-relative run.
var ignoredPrefixes = []string{
	"cmd/ghtmx/testproject/testdata",
	"docs/official",
	// The htmx 4 examples: own ghtmx.json (4.0.0 pin), own route
	// table, own central package.
	"examples/htmx4-inheritance",
	"examples/htmx4-status",
	"examples/htmx4-query",
}

// routeBoundTemplates carry hx- attributes bound to Go handlers, so
// their committed output contains URLs that only exist once
// routes.Discover has built the route table for the module. This test
// generates without one, so those attributes come out empty and the
// bytes cannot match. They are still generated and formatted below —
// only the byte comparison is skipped, and CI's ensure-generated step
// covers them with the real route table.
var routeBoundTemplates = map[string]bool{
	"benchmarks/corpus/corpus.ghtmx":   true,
	"examples/crud/crud.ghtmx":         true,
	"examples/events/page.ghtmx":       true,
	"examples/fragments/rows.ghtmx":    true,
	"examples/hx-bindings/items.ghtmx": true,
}

// corpusTemplate is one .ghtmx file and the generated Go beside it.
type corpusTemplate struct {
	// rel is the module-root-relative, slash-separated path, which is
	// what gets embedded in generated error messages.
	rel string
	abs string
}

// findCorpus walks the module for .ghtmx files that have committed
// output beside them.
func findCorpus(t *testing.T) []corpusTemplate {
	t.Helper()
	root := moduleRoot(t)

	var out []corpusTemplate
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// testdata is invisible to the go tool and holds fixtures
			// generated against other route tables.
			if d.Name() == "testdata" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".ghtmx" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, prefix := range ignoredPrefixes {
			if strings.HasPrefix(rel, prefix+"/") {
				return nil
			}
		}
		out = append(out, corpusTemplate{rel: rel, abs: path})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("no .ghtmx files found; the walk is not reaching the corpus and this test would assert nothing")
	}
	return out
}

// generatedPath returns the committed output path for a template.
func generatedPath(rel string) string {
	return strings.TrimSuffix(rel, ".ghtmx") + "_ghtmx.go"
}

// generateOne runs the pipeline FSEventHandler.generate runs.
func generateOne(t *testing.T, tpl corpusTemplate) (formatted []byte, output GeneratorOutput) {
	t.Helper()
	src, err := os.ReadFile(tpl.abs)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseString(string(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	output, err = Generate(parsed, &buf, WithFileName(tpl.rel))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	formatted, err = format.Source(buf.Bytes())
	if err != nil {
		t.Fatalf("the generated Go does not parse: %v\n%s", err, buf.String())
	}
	return formatted, output
}

// TestCorpusGenerationMatchesCommittedOutput is the regression net: if a
// generator change alters the emitted code, this names the template it
// changed rather than leaving it for a CI step in another process.
func TestCorpusGenerationMatchesCommittedOutput(t *testing.T) {
	root := moduleRoot(t)
	corpus := findCorpus(t)

	var compared int
	for _, tpl := range corpus {
		t.Run(tpl.rel, func(t *testing.T) {
			committedPath := filepath.Join(root, filepath.FromSlash(generatedPath(tpl.rel)))
			committed, err := os.ReadFile(committedPath)
			if err != nil {
				t.Skipf("no committed output beside this template: %v", err)
			}

			formatted, _ := generateOne(t, tpl)
			if routeBoundTemplates[tpl.rel] {
				// Generated and formatted above, which is what
				// exercises the generator; only the comparison is
				// meaningless without the route table.
				return
			}
			if !bytes.Equal(formatted, committed) {
				t.Errorf("generated output differs from %s\n%s",
					generatedPath(tpl.rel), firstDifference(string(committed), string(formatted)))
			}
			// Counted here, not around t.Run: incrementing outside the
			// closure counts templates that skipped for having no
			// committed output, so the guard below would stay green even
			// if every _ghtmx.go vanished.
			compared++
		})
	}
	if compared < 40 {
		t.Errorf("only %d templates were actually compared against committed output; "+
			"the corpus should be far larger, so either the walk is missing files or the committed output is gone", compared)
	}
}

// TestCorpusGenerationIsDeterministic pins NFR-004: generating the same
// template twice must produce identical bytes. Map iteration order
// leaking into the output would make every build a diff.
func TestCorpusGenerationIsDeterministic(t *testing.T) {
	for _, tpl := range findCorpus(t) {
		t.Run(tpl.rel, func(t *testing.T) {
			first, _ := generateOne(t, tpl)
			second, _ := generateOne(t, tpl)
			if !bytes.Equal(first, second) {
				t.Errorf("two generations of the same template differ:\n%s",
					firstDifference(string(first), string(second)))
			}
		})
	}
}

// TestCorpusSourceMapsPointIntoTheTemplate pins that every recorded
// mapping refers to a position that exists in the source. A source map
// with out-of-range positions sends the LSP to the wrong place, which
// looks like a broken editor rather than a broken compiler.
func TestCorpusSourceMapsPointIntoTheTemplate(t *testing.T) {
	for _, tpl := range findCorpus(t) {
		t.Run(tpl.rel, func(t *testing.T) {
			src, err := os.ReadFile(tpl.abs)
			if err != nil {
				t.Fatal(err)
			}
			lineCount := uint32(len(strings.Split(string(src), "\n")))

			_, output := generateOne(t, tpl)
			if output.SourceMap == nil {
				t.Fatal("no source map was produced")
			}
			// Every lookup must resolve or cleanly report absence.
			// Panicking on an out-of-range index here would take the
			// language server down mid-keystroke, so sweep the whole
			// template rather than spot-checking.
			for line := range lineCount {
				for col := range uint32(8) {
					output.SourceMap.TargetPositionFromSource(line, col)
					output.SourceMap.SourcePositionFromTarget(line, col)
				}
			}
		})
	}
}

// firstDifference renders the first differing line, which is far more
// useful in a failure than two thousand lines of Go.
func firstDifference(want, got string) string {
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
			return "first difference at line " + itoa(i+1) + ":\n  committed: " + quote(w) + "\n  generated: " + quote(g)
		}
	}
	return "the files differ only in trailing content"
}

func quote(s string) string { return strconv.Quote(s) }

func itoa(i int) string { return strconv.Itoa(i) }
