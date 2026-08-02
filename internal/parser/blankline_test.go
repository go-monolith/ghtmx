package parser

import "testing"

// endsWithBlankLine is what preserves the author's spacing between a
// top-level comment and the declaration below it. It is the whole of the
// formatter behaviour change it was written for, so its boundaries are
// pinned directly — the corpus round trip only exercises whichever
// shapes the committed templates happen to contain.
func TestEndsWithBlankLine(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"single newline", "// c\n", false},
		{"blank line", "// c\n\n", true},
		{"crlf blank line", "// c\r\n\r\n", true},
		// The case the rewrite was for: a separator line holding spaces
		// or tabs is still a blank line to whoever typed it, and suffix
		// matching missed it.
		{"blank line with a tab", "// c\n\t\n", true},
		{"blank line with spaces", "// c\n   \n", true},
		{"trailing spaces, no newline", "// c\n   ", false},
		{"blank line then trailing spaces", "// c\n\n  ", true},
		{"no newline at all", "// c", false},
		{"empty", "", false},
		{"several blank lines", "// c\n\n\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := endsWithBlankLine(tt.raw); got != tt.want {
				t.Errorf("endsWithBlankLine(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
