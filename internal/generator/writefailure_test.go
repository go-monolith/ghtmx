package generator

import (
	"errors"
	"os"
	"testing"

	"github.com/go-monolith/ghtmx/internal/parser"
)

// generator.go is written as
//
//	if _, err = g.w.WriteIndent(...); err != nil { return err }
//
// several hundred times over. Nothing in the ordinary corpus run reaches
// a single one of those `return err` bodies, because the sink is always
// a bytes.Buffer and a bytes.Buffer cannot fail. That leaves the error
// plumbing — roughly a quarter of the package — completely unexercised,
// and the failure it hides is the quiet kind: an `err` shadowed by a
// later `:=`, or a write whose error is dropped, produces a truncated
// .go file rather than a reported failure. The user sees a compile error
// in generated code and no clue why.
//
// So: fail the k-th write and require the error to come back out. Doing
// that for every k reaches every write site the template can reach, and
// asserts a property worth having — no swallowed writes, no shadowed
// errors — rather than merely visiting lines.

var errWriteFailed = errors.New("simulated write failure")

// failAtWrite fails the nth Write call (1-based) and every call after it.
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

// maxSweepsPerTemplate bounds the work: the larger templates make tens
// of thousands of writes, and one Generate call per write would turn
// this into a multi-minute test. When a template exceeds the budget the
// sweep strides through its write indices instead, and the test logs
// exactly what it sampled — a silent cap would read as "every write site
// is covered" when it is not.
const maxSweepsPerTemplate = 2500

// sweepTemplates are chosen to reach distinct generator paths: control
// flow, elements and attributes, spreads, raw elements, scripts, CSS,
// fragments, and the templ-element composition path.
var sweepTemplates = []string{
	"test-if",
	"test-ifelse",
	"test-for",
	"test-switch",
	"test-element-attributes",
	"test-spread-attributes",
	"test-raw-elements",
	"test-templ-element",
	"test-css-usage",
	"test-script-usage",
	"test-text-inline-expression",
	"test-doctype",
}

func parseTemplate(t *testing.T, dir string) *parser.TemplateFile {
	t.Helper()
	src, err := os.ReadFile(dir + "/template.ghtmx")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseString(string(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return parsed
}

func TestGenerateReportsEveryWriteFailure(t *testing.T) {
	for _, dir := range sweepTemplates {
		t.Run(dir, func(t *testing.T) {
			parsed := parseTemplate(t, dir)

			var counter writeCounter
			if _, err := Generate(parsed, &counter); err != nil {
				t.Fatalf("baseline generation failed: %v", err)
			}
			if counter.n == 0 {
				t.Fatal("generation made no writes; the sweep would assert nothing")
			}

			stride := 1
			if counter.n > maxSweepsPerTemplate {
				stride = counter.n/maxSweepsPerTemplate + 1
				t.Logf("%d writes: sampling every %d-th write index (%d of %d)",
					counter.n, stride, counter.n/stride, counter.n)
			}

			for k := 1; k <= counter.n; k += stride {
				_, err := Generate(parsed, &failAtWrite{n: k})
				if err == nil {
					t.Fatalf("write %d of %d failed but Generate returned nil: "+
						"the error was swallowed, so a truncated file would be written as if it had succeeded",
						k, counter.n)
				}
				if !errors.Is(err, errWriteFailed) {
					t.Fatalf("write %d of %d: Generate returned %v, want it to wrap the write failure",
						k, counter.n, err)
				}
			}
		})
	}
}

// TestGenerateSurvivesAFailureAtEveryWriteWithoutPanicking is the
// weaker, broader companion: it runs the same sweep over the whole
// corpus at a coarse stride, so a template outside sweepTemplates that
// panics on a failed write — a nil-deref in a deferred cleanup, say —
// still gets caught.
func TestGenerateSurvivesAFailureAtEveryWriteWithoutPanicking(t *testing.T) {
	const samplesPerTemplate = 40

	for _, tpl := range findCorpus(t) {
		t.Run(tpl.rel, func(t *testing.T) {
			src, err := os.ReadFile(tpl.abs)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := parser.ParseString(string(src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			var counter writeCounter
			if _, err := Generate(parsed, &counter); err != nil {
				t.Fatalf("baseline generation failed: %v", err)
			}
			stride := max(counter.n/samplesPerTemplate, 1)

			for k := 1; k <= counter.n; k += stride {
				if _, err := Generate(parsed, &failAtWrite{n: k}); err == nil {
					t.Fatalf("write %d of %d failed but Generate returned nil", k, counter.n)
				}
			}
		})
	}
}
