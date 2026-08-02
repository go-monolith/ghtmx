package parser_test

import (
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/parser"
)

// The 25 *parser.go files are dense with error branches that the happy
// path never reaches: unterminated elements, unclosed braces, malformed
// expressions. Those branches are what turns a typo into a diagnostic
// with a line and column on it, so a swallowed one costs the user the
// only signal they get — the compiler either accepts nonsense and emits
// broken Go, or fails somewhere unrelated with a position pointing at
// the wrong place.
//
// Each case below is a mistake someone actually makes mid-edit. The
// assertion is deliberately about behaviour rather than message text: it
// must fail, and it must not panic or hang.

func TestMalformedTemplatesAreRejected(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		// Element structure.
		{"unclosed element", "package p\n\ntempl x() {\n\t<div>\n}\n"},
		{"mismatched closing tag", "package p\n\ntempl x() {\n\t<div></span>\n}\n"},
		{"unterminated open tag", "package p\n\ntempl x() {\n\t<div\n}\n"},
		{"stray closing tag", "package p\n\ntempl x() {\n\t</div>\n}\n"},
		{"unclosed template brace", "package p\n\ntempl x() {\n\t<div></div>\n"},

		// Attributes.
		{"unterminated attribute value", "package p\n\ntempl x() {\n\t<div class=\"a></div>\n}\n"},
		{"unclosed attribute expression", "package p\n\ntempl x() {\n\t<div class={ foo></div>\n}\n"},
		{"unterminated conditional attribute", "package p\n\ntempl x() {\n\t<div if cond {\n\t\tclass=\"a\"\n\t></div>\n}\n"},
		{"unclosed spread", "package p\n\ntempl x() {\n\t<div { attrs...></div>\n}\n"},

		// String expressions.
		{"unclosed string expression", "package p\n\ntempl x() {\n\t{ name\n}\n"},

		// Control flow.
		{"if without a closing brace", "package p\n\ntempl x() {\n\tif true {\n\t\t<div></div>\n}\n"},
		{"for without a closing brace", "package p\n\ntempl x() {\n\tfor range items {\n\t\t<div></div>\n}\n"},
		{"switch without a closing brace", "package p\n\ntempl x() {\n\tswitch v {\n\tcase 1:\n\t\t<div></div>\n}\n"},

		// Declarations.
		{"templ without parentheses", "package p\n\ntempl x {\n\t<div></div>\n}\n"},
		{"templ without a body", "package p\n\ntempl x()\n"},
		{"css without a closing brace", "package p\n\ncss c() {\n\tcolor: red;\n"},
		{"script without a closing brace", "package p\n\nscript s() {\n\tconsole.log(1)\n"},
		{"fragment without a closing brace", "package p\n\nfragment f() {\n\t<div></div>\n"},

		// File level.
		{"unterminated raw element", "package p\n\ntempl x() {\n\t<script>var a = 1;\n}\n"},
		{"unterminated html comment", "package p\n\ntempl x() {\n\t<!-- unterminated\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A panic here is the worst outcome: the LSP parses on every
			// keystroke, so half-typed input reaching a nil deref takes
			// the editor's language server down.
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("parsing panicked on malformed input: %v\nsource:\n%s", r, tt.src)
				}
			}()

			tf, err := parser.ParseString(tt.src)
			if err == nil {
				t.Errorf("parsed successfully, want an error\nsource:\n%s\nparsed: %+v", tt.src, tf)
			}
		})
	}
}

// TestTruncationAtEveryOffsetIsHandled feeds every prefix of a valid
// template to the parser. This is what an editor sends while someone is
// typing, so each prefix must either parse or fail cleanly — never
// panic, never hang.
func TestTruncationAtEveryOffsetIsHandled(t *testing.T) {
	const valid = `package p

import "strings"

// A component.
templ Greeting(name string, items []string) {
	<div class="greeting" data-x={ name }>
		if name != "" {
			<span>{ strings.ToUpper(name) }</span>
		} else {
			<span>anonymous</span>
		}
		for _, item := range items {
			<li>{ item }</li>
		}
		switch len(items) {
			case 0:
				<p>none</p>
			default:
				<p>{ name }</p>
		}
	</div>
}

css style() {
	color: #ffffff;
}

script onClick() {
	console.log("hi")
}
`
	for i := range len(valid) {
		prefix := valid[:i]
		t.Run(strings.ReplaceAll(prefix[max(0, len(prefix)-12):], "\n", "\\n"), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("parsing panicked on a %d-byte prefix: %v\nsource:\n%s", i, r, prefix)
				}
			}()
			// The result does not matter — only that parsing terminates
			// without panicking.
			_, _ = parser.ParseString(prefix)
		})
	}
}

// TestAcceptedByTheParserButNotNecessarilyValid records inputs the
// parser deliberately lets through. A missing package clause gets the
// default the parser was constructed with; an empty or declaration-only
// file is simply a file with no nodes; and malformed Go inside a control
// flow expression is the Go compiler's to reject, with a position the
// source map translates back. Pinning them here stops someone
// "tightening" the parser into rejecting input the rest of the pipeline
// is built to handle.
func TestAcceptedByTheParserButNotNecessarilyValid(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"no package clause", "templ x() {\n\t<div></div>\n}\n"},
		{"empty file", ""},
		{"package only", "package p\n"},
		{"empty string expression", "package p\n\ntempl x() {\n\t{ }\n}\n"},
		{"if without a condition", "package p\n\ntempl x() {\n\tif {\n\t\t<div></div>\n\t}\n}\n"},
		{"else without if", "package p\n\ntempl x() {\n\t} else {\n\t\t<div></div>\n\t}\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("parsing panicked: %v\nsource:\n%s", r, tt.src)
				}
			}()
			if _, err := parser.ParseString(tt.src); err != nil {
				t.Errorf("parser rejected input it is expected to accept: %v\nsource:\n%s", err, tt.src)
			}
		})
	}
}

// TestValidTemplatesStillParse guards the cases above from over-reach:
// a parser change that rejected everything would satisfy every
// assertion in TestMalformedTemplatesAreRejected.
func TestValidTemplatesStillParse(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"minimal", "package p\n\ntempl x() {\n\t<div></div>\n}\n"},
		{"void element", "package p\n\ntempl x() {\n\t<br>\n}\n"},
		{"self-closing", "package p\n\ntempl x() {\n\t<img src=\"a.png\"/>\n}\n"},
		{"expression attribute", "package p\n\ntempl x(c string) {\n\t<div class={ c }></div>\n}\n"},
		{"string expression", "package p\n\ntempl x(n string) {\n\t{ n }\n}\n"},
		{"if else", "package p\n\ntempl x(b bool) {\n\tif b {\n\t\t<a></a>\n\t} else {\n\t\t<b></b>\n\t}\n}\n"},
		{"for", "package p\n\ntempl x(i []int) {\n\tfor range i {\n\t\t<li></li>\n\t}\n}\n"},
		{"css", "package p\n\ncss c() {\n\tcolor: red;\n}\n"},
		{"script", "package p\n\nscript s() {\n\tconsole.log(1)\n}\n"},
		{"comment above templ", "package p\n\n// Doc.\ntempl x() {\n\t<div></div>\n}\n"},
		{"html comment", "package p\n\ntempl x() {\n\t<!-- note -->\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parser.ParseString(tt.src); err != nil {
				t.Errorf("valid template failed to parse: %v\nsource:\n%s", err, tt.src)
			}
		})
	}
}
