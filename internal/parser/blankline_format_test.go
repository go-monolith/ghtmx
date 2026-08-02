package parser_test

import (
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/format"
)

// The end-to-end half of the blank-line change: what a user actually
// notices is whether `ghtmx fmt` moves their comment. Both spacings have
// to survive a format unchanged — attached stays attached, separated
// stays separated.
func TestFormattingPreservesCommentSpacing(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "comment attached to an event declaration",
			src:  "package p\n\n// Documents the event below.\nevent ItemSaved(id string)\n",
		},
		{
			name: "comment separated from an event declaration",
			src:  "package p\n\n// A section heading.\n\nevent ItemSaved(id string)\n",
		},
		{
			name: "comment attached to a templ",
			src:  "package p\n\n// Documents the template below.\ntempl X() {\n\t<div></div>\n}\n",
		},
		{
			name: "comment separated from a templ",
			src:  "package p\n\n// A section heading.\n\ntempl X() {\n\t<div></div>\n}\n",
		},
		{
			name: "comment attached to a css block",
			src:  "package p\n\n// Documents the styles below.\ncss theme() {\n\tcolor: red;\n}\n",
		},
		{
			name: "comment separated from a css block",
			src:  "package p\n\n// A section heading.\n\ncss theme() {\n\tcolor: red;\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, changed, err := format.Templ([]byte(tt.src), "page.ghtmx", format.Config{})
			if err != nil {
				t.Fatalf("format: %v", err)
			}
			if changed {
				t.Errorf("the formatter moved the comment:\nwant %q\ngot  %q", tt.src, string(out))
			}
			// And it stays put on a second pass, so a save loop does not
			// oscillate between the two spacings.
			twice, changedAgain, err := format.Templ(out, "page.ghtmx", format.Config{})
			if err != nil {
				t.Fatalf("second format: %v", err)
			}
			if changedAgain {
				t.Errorf("a second pass moved the comment:\n%q", string(twice))
			}
			if !strings.Contains(string(twice), "//") {
				t.Errorf("the comment was lost entirely:\n%q", string(twice))
			}
		})
	}
}
