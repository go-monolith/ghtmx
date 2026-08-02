package generator

import (
	"strings"
	"testing"
)

// The RangeWriter tracks the position of everything it emits, and those
// positions become the source map. A column that drifts by one sends
// every LSP feature — hover, go-to-definition, diagnostics — one
// character off for the rest of the file.

func TestRangeWriterTracksPositions(t *testing.T) {
	var sb strings.Builder
	rw := NewRangeWriter(&sb)

	r, err := rw.Write("hello")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if r.From.Line != 0 || r.From.Col != 0 {
		t.Errorf("first write starts at %d:%d, want 0:0", r.From.Line, r.From.Col)
	}
	if r.To.Col != 5 {
		t.Errorf("after writing 5 characters the column is %d, want 5", r.To.Col)
	}

	// A newline resets the column and advances the line, or every
	// position after the first line is wrong.
	r, err = rw.Write("\nworld")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if r.To.Line != 1 {
		t.Errorf("line after a newline is %d, want 1", r.To.Line)
	}
	if r.To.Col != 5 {
		t.Errorf("column after a newline is %d, want 5", r.To.Col)
	}

	if got := sb.String(); got != "hello\nworld" {
		t.Errorf("wrote %q, want %q", got, "hello\nworld")
	}
}

// TestRangeWriterCountsBytes records the unit columns are measured in.
// It is bytes, not runes: "é日本" is three characters but advances the
// column by eight. That is consistent with the parser, which records
// source positions the same way, so the two halves of a source map
// agree — but it is worth pinning, because LSP positions are defined in
// UTF-16 code units and any conversion added later has to account for
// this rather than assume characters.
func TestRangeWriterCountsBytes(t *testing.T) {
	const multibyte = "é日本"

	var sb strings.Builder
	rw := NewRangeWriter(&sb)

	r, err := rw.Write(multibyte)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, want := r.To.Col, uint32(len(multibyte)); got != want {
		t.Errorf("column advanced by %d, want %d (the byte length)", got, want)
	}
	// Whatever the unit, the bytes themselves must arrive intact.
	if sb.String() != multibyte {
		t.Errorf("wrote %q, want %q", sb.String(), multibyte)
	}
}

// TestRangeWriterReportsWriteFailures pins that a failed write surfaces
// rather than leaving the position tracker out of step with what
// actually reached the file.
func TestRangeWriterReportsWriteFailures(t *testing.T) {
	rw := NewRangeWriter(&failAtWrite{n: 1})

	if _, err := rw.Write("anything"); err == nil {
		t.Error("Write returned nil despite the underlying writer failing")
	}
}
