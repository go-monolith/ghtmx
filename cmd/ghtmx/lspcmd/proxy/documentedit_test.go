package proxy

import (
	"io"
	"log/slog"
	"testing"

	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
)

// Document is the editor's buffer as this server sees it: every
// keystroke arrives as a range plus replacement text, and the result has
// to match what the editor has on screen. If it drifts by one character,
// every position the server reports afterwards — diagnostics,
// completions, go-to-definition — lands in the wrong place, and the
// symptom looks like a broken editor rather than a bad edit apply.
//
// So these tests drive Apply the way an editor does, and check the whole
// resulting buffer rather than the branch that was taken.

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func rng(startLine, startCol, endLine, endCol uint32) *lsp.Range {
	return &lsp.Range{
		Start: lsp.Position{Line: startLine, Character: startCol},
		End:   lsp.Position{Line: endLine, Character: endCol},
	}
}

func TestDocumentApply(t *testing.T) {
	tests := []struct {
		name  string
		start string
		r     *lsp.Range
		with  string
		want  string
	}{
		{
			name:  "insert within a line",
			start: "hello world",
			r:     rng(0, 5, 0, 5),
			with:  " there",
			want:  "hello there world",
		},
		{
			name:  "insert at the start",
			start: "world",
			r:     rng(0, 0, 0, 0),
			with:  "hello ",
			want:  "hello world",
		},
		{
			name:  "insert at the end of a line",
			start: "hello",
			r:     rng(0, 5, 0, 5),
			with:  "!",
			want:  "hello!",
		},
		{
			name:  "insert a newline splits the line",
			start: "ab",
			r:     rng(0, 1, 0, 1),
			with:  "\n",
			want:  "a\nb",
		},
		{
			name:  "delete within a line",
			start: "hello there world",
			r:     rng(0, 5, 0, 11),
			with:  "",
			want:  "hello world",
		},
		{
			name:  "delete across lines joins them",
			start: "one\ntwo\nthree",
			r:     rng(0, 3, 2, 0),
			with:  "",
			want:  "onethree",
		},
		{
			name:  "overwrite within a line",
			start: "hello world",
			r:     rng(0, 6, 0, 11),
			with:  "there",
			want:  "hello there",
		},
		{
			name:  "overwrite across lines",
			start: "one\ntwo\nthree",
			r:     rng(0, 1, 2, 2),
			with:  "X",
			want:  "oXree",
		},
		{
			name:  "a nil range replaces the whole document",
			start: "old\ncontent",
			r:     nil,
			with:  "new",
			want:  "new",
		},
		{
			name:  "a range covering everything replaces the document",
			start: "old\ncontent",
			r:     rng(0, 0, 2, 0),
			with:  "brand\nnew",
			want:  "brand\nnew",
		},
		{
			// An editor that has already applied an edit can send a
			// range past the end of what the server holds. Clamping it
			// is what keeps the two in sync instead of panicking.
			name:  "an out-of-range end is clamped",
			start: "short",
			r:     rng(0, 2, 99, 99),
			with:  "X",
			want:  "shX",
		},
		{
			name:  "an out-of-range start is clamped",
			start: "short",
			r:     rng(99, 99, 99, 99),
			with:  "!",
			want:  "short!",
		},
		{
			name:  "a column past the end of its line is clamped",
			start: "ab\ncd",
			r:     rng(0, 99, 0, 99),
			with:  "X",
			want:  "abX\ncd",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDocument(testLog(), tt.start)
			d.Apply(tt.r, tt.with)
			if got := d.String(); got != tt.want {
				t.Errorf("Apply(%v, %q) on %q\n got %q\nwant %q", tt.r, tt.with, tt.start, got, tt.want)
			}
		})
	}
}

// TestDocumentApplySequenceMatchesTheEditor replays a sequence of edits
// the way a real session does. Each edit's range is expressed against
// the document as it stands after the previous one, which is where an
// off-by-one compounds into nonsense.
func TestDocumentApplySequenceMatchesTheEditor(t *testing.T) {
	d := NewDocument(testLog(), "package main")

	steps := []struct {
		r    *lsp.Range
		with string
		want string
	}{
		{rng(0, 12, 0, 12), "\n", "package main\n"},
		{rng(1, 0, 1, 0), "\ntempl x() {", "package main\n\ntempl x() {"},
		{rng(2, 11, 2, 11), "\n}", "package main\n\ntempl x() {\n}"},
		{rng(2, 6, 2, 7), "y", "package main\n\ntempl y() {\n}"},
	}
	for i, step := range steps {
		d.Apply(step.r, step.with)
		if got := d.String(); got != step.want {
			t.Fatalf("after edit %d\n got %q\nwant %q", i+1, got, step.want)
		}
	}
}

func TestDocumentLenAndLineLengths(t *testing.T) {
	d := NewDocument(testLog(), "ab\ncdef\n")

	if got, want := d.LineLengths(), []int{2, 4, 0}; !equalInts(got, want) {
		t.Errorf("LineLengths() = %v, want %v", got, want)
	}
	line, col := d.Len()
	if line != 3 || col != 0 {
		t.Errorf("Len() = (%d, %d), want (3, 0)", line, col)
	}
}

func TestDocumentReplace(t *testing.T) {
	d := NewDocument(testLog(), "old")
	d.Replace("a\nb")
	if got, want := d.String(), "a\nb"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestDocumentContentsSetGetDelete(t *testing.T) {
	log := testLog()
	dc := newDocumentContents(log)
	const uri = "file:///a.ghtmx"

	if _, ok := dc.Get(uri); ok {
		t.Error("Get on an empty store reported ok")
	}

	dc.Set(uri, NewDocument(log, "content"))
	got, ok := dc.Get(uri)
	if !ok {
		t.Fatal("Get after Set reported not-ok")
	}
	if got.String() != "content" {
		t.Errorf("Get returned %q, want %q", got.String(), "content")
	}
	if uris := dc.URIs(); len(uris) != 1 || uris[0] != uri {
		t.Errorf("URIs() = %v, want [%s]", uris, uri)
	}

	dc.Delete(uri)
	if _, ok := dc.Get(uri); ok {
		t.Error("Get after Delete reported ok")
	}
	if uris := dc.URIs(); len(uris) != 0 {
		t.Errorf("URIs() after Delete = %v, want empty", uris)
	}
}

// TestDocumentContentsApplyReportsAnUnknownURI pins that a change for a
// document the server never opened is an error rather than a silent
// no-op — silently ignoring it would leave the editor and server
// disagreeing about the file with nothing to indicate it.
func TestDocumentContentsApplyReportsAnUnknownURI(t *testing.T) {
	dc := newDocumentContents(testLog())

	_, err := dc.Apply("file:///never-opened.ghtmx", []lsp.TextDocumentContentChangeEvent{
		{Range: rng(0, 0, 0, 0), Text: "x"},
	})
	if err == nil {
		t.Error("Apply on an unopened document succeeded, want an error")
	}
}

func TestDocumentContentsApplyAppliesEveryChange(t *testing.T) {
	log := testLog()
	dc := newDocumentContents(log)
	const uri = "file:///a.ghtmx"
	dc.Set(uri, NewDocument(log, "hello"))

	// An editor may batch several changes into one notification; all of
	// them have to land, in order.
	d, err := dc.Apply(uri, []lsp.TextDocumentContentChangeEvent{
		{Range: rng(0, 5, 0, 5), Text: " world"},
		{Range: rng(0, 0, 0, 0), Text: ">> "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := d.String(), ">> hello world"; got != want {
		t.Errorf("after applying both changes = %q, want %q", got, want)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
