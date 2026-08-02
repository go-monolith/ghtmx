package parser

import (
	"strings"
	"testing"
)

// Whitespace between elements is significant in HTML, and this is where
// that judgement is made. Collapsing a newline that separated two inline
// elements runs their text together in the browser; emitting one where
// the source had only spaces adds a gap that was never written.

func TestWhitespaceWrite(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		// A newline is what makes the gap significant to the browser, so
		// it collapses to exactly one space.
		{"newline", "\n", " "},
		{"newline with indentation", "\n\t\t", " "},
		{"several newlines", "\n\n\n", " "},
		// Without a newline the whitespace is already inline and adding
		// anything would change the rendering.
		{"spaces only", "   ", ""},
		{"tab only", "\t", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sb strings.Builder
			ws := &Whitespace{Value: tt.value}
			if err := ws.Write(&sb, 0); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if sb.String() != tt.want {
				t.Errorf("Write(%q) produced %q, want %q", tt.value, sb.String(), tt.want)
			}
		})
	}
}

// TestContainsNonTextNodes decides whether an element's children can be
// emitted as one string literal or have to be written piecewise. A wrong
// answer either loses a nested element or gives up an optimisation that
// applies to most of a page.
func TestContainsNonTextNodes(t *testing.T) {
	tests := []struct {
		name  string
		nodes []Node
		want  bool
	}{
		{"empty", nil, false},
		{"text only", []Node{&Text{Value: "hi"}}, false},
		{"text and whitespace", []Node{&Text{Value: "a"}, &Whitespace{Value: " "}}, false},
		{"whitespace only", []Node{&Whitespace{Value: "\n"}}, false},
		// Anything else has to be written piecewise, or it would be
		// swallowed into a literal.
		{"an element", []Node{&Element{Name: "div"}}, true},
		{"an expression", []Node{&StringExpression{}}, true},
		{"text then an element", []Node{&Text{Value: "a"}, &Element{Name: "b"}}, true},
		{"an element then text", []Node{&Element{Name: "b"}, &Text{Value: "a"}}, true},
		{"a comment", []Node{&HTMLComment{Contents: "c"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsNonTextNodes(tt.nodes); got != tt.want {
				t.Errorf("containsNonTextNodes = %v, want %v", got, tt.want)
			}
		})
	}
}
