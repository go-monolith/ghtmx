package visualize

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/parser"
)

// The visualiser renders a .ghtmx file and its generated Go side by
// side, with each mapped character carrying the id of its counterpart so
// hovering one highlights the other. That pairing is the whole feature,
// so the tests pin it in both directions, plus the escaping that keeps
// source text from being interpreted as markup in the page that displays
// it.

// sourceMapWith maps one expression from a source range to a target
// range, which is enough for both directions of the lookup.
func sourceMapWith(t *testing.T, value string, srcLine, srcCol, tgtLine, tgtCol uint32) *parser.SourceMap {
	t.Helper()
	sm := parser.NewSourceMap()
	sm.Add(
		parser.Expression{
			Value: value,
			Range: parser.Range{
				From: parser.Position{Line: srcLine, Col: srcCol},
				To:   parser.Position{Line: srcLine, Col: srcCol + uint32(len(value))},
			},
		},
		parser.Range{
			From: parser.Position{Line: tgtLine, Col: tgtCol},
			To:   parser.Position{Line: tgtLine, Col: tgtCol + uint32(len(value))},
		},
	)
	return sm
}

// renderer is the ghtmx.Component shape, named so the tests can hold
// the two unexported line renderers in one table.
type renderer interface {
	Render(context.Context, io.Writer) error
}

func render(t *testing.T, c renderer) string {
	t.Helper()
	var sb strings.Builder
	if err := c.Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestTemplLinesLinkMappedCharactersToTheirTarget(t *testing.T) {
	sm := sourceMapWith(t, "name", 0, 0, 5, 2)
	got := render(t, templLines{contents: "name", sourceMap: sm})

	// Every mapped character carries both ids, which is what lets the
	// page highlight the corresponding character on the other side.
	if !strings.Contains(got, "src_0_0") {
		t.Errorf("output is missing the source id:\n%s", got)
	}
	if !strings.Contains(got, "tgt_5_2") {
		t.Errorf("output is missing the target id:\n%s", got)
	}
}

func TestGoLinesLinkMappedCharactersBackToTheSource(t *testing.T) {
	sm := sourceMapWith(t, "name", 3, 1, 0, 0)
	got := render(t, goLines{contents: "name", sourceMap: sm})

	if !strings.Contains(got, "src_3_1") {
		t.Errorf("output is missing the source id:\n%s", got)
	}
	if !strings.Contains(got, "tgt_0_0") {
		t.Errorf("output is missing the target id:\n%s", got)
	}
}

// TestUnmappedCharactersAreEscaped pins the other branch: text with no
// mapping is still shown, and shown safely. A < that reached the page
// unescaped would silently swallow the rest of the line.
func TestUnmappedCharactersAreEscaped(t *testing.T) {
	empty := parser.NewSourceMap()
	tests := []struct {
		name     string
		contents string
		want     string
		notWant  string
	}{
		{"angle bracket", "<", "&lt;", "<span>&lt"},
		{"ampersand", "&", "&amp;", ""},
		{"space becomes a non-breaking space", " ", "&nbsp;", ""},
		{"tab becomes a non-breaking space", "\t", "&nbsp;", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, side := range []struct {
				name string
				out  string
			}{
				{"templ", render(t, templLines{contents: tt.contents, sourceMap: empty})},
				{"go", render(t, goLines{contents: tt.contents, sourceMap: empty})},
			} {
				if !strings.Contains(side.out, tt.want) {
					t.Errorf("%s side does not contain %q:\n%s", side.name, tt.want, side.out)
				}
			}
		})
	}
}

// TestLineNumbersAreEmittedPerLine pins the gutter: without it the two
// panes cannot be read against each other at all.
func TestLineNumbersAreEmittedPerLine(t *testing.T) {
	empty := parser.NewSourceMap()
	contents := "a\nb\nc"

	for _, side := range []struct {
		name string
		out  string
	}{
		{"templ", render(t, templLines{contents: contents, sourceMap: empty})},
		{"go", render(t, goLines{contents: contents, sourceMap: empty})},
	} {
		for i, want := range []string{"<span>0&nbsp;</span>", "<span>1&nbsp;</span>", "<span>2&nbsp;</span>"} {
			if !strings.Contains(side.out, want) {
				t.Errorf("%s side is missing the gutter for line %d (%q):\n%s", side.name, i, want, side.out)
			}
		}
		if got, want := strings.Count(side.out, "<br/>"), 3; got != want {
			t.Errorf("%s side emitted %d line breaks, want %d", side.name, got, want)
		}
	}
}

func TestHTMLCombinesBothPanes(t *testing.T) {
	sm := sourceMapWith(t, "x", 0, 0, 0, 0)
	got := render(t, HTML("greeting.ghtmx", "x", "x", sm))

	for _, want := range []string{"greeting.ghtmx", "src_0_0", "tgt_0_0"} {
		if !strings.Contains(got, want) {
			t.Errorf("combined output is missing %q", want)
		}
	}
}

// failAfter fails once its budget is spent, which is the only way to
// reach the renderers' error returns: a strings.Builder never fails.
type failAfter struct {
	remaining int
	err       error
}

func (w *failAfter) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, w.err
	}
	w.remaining--
	return len(p), nil
}

type countingWriter struct{ n int }

func (w *countingWriter) Write(p []byte) (int, error) { w.n++; return len(p), nil }

// TestRenderersReportWriteFailures sweeps every write site in turn. A
// swallowed error here produces a truncated visualisation that looks
// like a source-map bug rather than a write failure, which is a bad
// afternoon for whoever debugs it.
func TestRenderersReportWriteFailures(t *testing.T) {
	sentinel := errors.New("write failed")
	// Contents chosen to exercise both branches of the inner loop: "x"
	// is mapped, "<" is not.
	sm := sourceMapWith(t, "x", 0, 0, 0, 0)

	tests := []struct {
		name      string
		component func() renderer
	}{
		{"templLines", func() renderer { return templLines{contents: "x<\ny", sourceMap: sm} }},
		{"goLines", func() renderer { return goLines{contents: "x<\ny", sourceMap: sm} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var counter countingWriter
			if err := tt.component().Render(context.Background(), &counter); err != nil {
				t.Fatalf("baseline render failed: %v", err)
			}
			if counter.n == 0 {
				t.Fatal("the renderer made no writes; the sweep would assert nothing")
			}
			for budget := range counter.n {
				w := &failAfter{remaining: budget, err: sentinel}
				if err := tt.component().Render(context.Background(), w); err == nil {
					t.Errorf("write %d of %d failed but the error was swallowed", budget+1, counter.n)
				}
			}
		})
	}
}
