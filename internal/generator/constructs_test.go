package generator

import (
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/parser"
)

// The committed corpus covers the constructs the language documents, but
// several emitters are reached only by shapes it happens not to contain:
// a Go code block inside a template, a comment, a raw element carrying an
// expression. Each is valid syntax someone will write, and a break in the
// emitter for one would surface as broken generated Go in their project
// rather than anywhere in this repository.

func TestGenerateConstructs(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// A Go statement block inside a template body.
			name: "go code block",
			src: "package p\n\ntempl x(items []string) {\n" +
				"\t{{ count := len(items) }}\n" +
				"\t<div>{ fmt.Sprint(count) }</div>\n}\n",
			want: []string{"count := len(items)"},
		},
		{
			name: "go comment",
			src:  "package p\n\ntempl x() {\n\t// a note about the markup\n\t<div></div>\n}\n",
			want: []string{"<div>"},
		},
		{
			name: "html comment",
			src:  "package p\n\ntempl x() {\n\t<!-- rendered to the browser -->\n\t<div></div>\n}\n",
			want: []string{"rendered to the browser"},
		},
		{
			name: "raw element with an expression",
			src:  "package p\n\ntempl x(js string) {\n\t<script>{ js }</script>\n}\n",
			want: []string{"script"},
		},
		{
			name: "style raw element",
			src:  "package p\n\ntempl x() {\n\t<style>body { color: red; }</style>\n}\n",
			want: []string{"style"},
		},
		{
			name: "script element with a handler",
			src:  "package p\n\nscript onClick(msg string) {\n\tconsole.log(msg)\n}\n\ntempl x() {\n\t<button onclick={ onClick(\"hi\") }>go</button>\n}\n",
			want: []string{"onClick", "console.log"},
		},
		{
			// The emitter renders children through the runtime rather
			// than a literal identifier, so the assertion is that the
			// surrounding markup still comes out.
			name: "children expression",
			src:  "package p\n\ntempl wrapper() {\n\t<div>{ children... }</div>\n}\n",
			want: []string{"<div>"},
		},
		{
			name: "templ element composition",
			src:  "package p\n\ntempl inner() {\n\t<b></b>\n}\n\ntempl outer() {\n\t@inner() {\n\t\t<i></i>\n\t}\n}\n",
			want: []string{"inner()"},
		},
		{
			name: "significant whitespace between elements",
			src:  "package p\n\ntempl x() {\n\t<span>a</span> <span>b</span>\n}\n",
			want: []string{"span"},
		},
		{
			name: "void and self-closing elements",
			src:  "package p\n\ntempl x() {\n\t<br>\n\t<img src=\"a.png\"/>\n\t<hr>\n}\n",
			want: []string{"br", "img"},
		},
		{
			name: "boolean and spread attributes",
			src:  "package p\n\ntempl x(attrs ghtmx.Attributes, on bool) {\n\t<input disabled?={ on } { attrs... }/>\n}\n",
			want: []string{"disabled"},
		},
		{
			name: "conditional attribute",
			src:  "package p\n\ntempl x(on bool) {\n\t<div\n\t\tif on {\n\t\t\tclass=\"active\"\n\t\t}\n\t></div>\n}\n",
			want: []string{"active"},
		},
		{
			name: "switch with a default",
			src:  "package p\n\ntempl x(n int) {\n\tswitch n {\n\t\tcase 1:\n\t\t\t<p>one</p>\n\t\tdefault:\n\t\t\t<p>many</p>\n\t}\n}\n",
			want: []string{"one", "many"},
		},
		{
			// writeRawElement takes a different path once the element
			// carries attributes: the no-attribute case emits one
			// literal, this one emits the tag piecewise.
			name: "raw element with attributes",
			src:  "package p\n\ntempl x() {\n\t<script type=\"module\" defer>let a = 1;</script>\n}\n",
			want: []string{"module", "defer"},
		},
		{
			name: "style element with attributes",
			src:  "package p\n\ntempl x(nonce string) {\n\t<style nonce={ nonce }>body { color: red; }</style>\n}\n",
			want: []string{"style", "nonce"},
		},
		{
			// A doctype carrying a quote has to be escaped, or the
			// emitted Go string literal will not compile.
			name: "doctype with a quoted identifier",
			src:  "package p\n\ntempl x() {\n\t<!DOCTYPE html PUBLIC \"-//W3C//DTD HTML 4.01//EN\">\n\t<html></html>\n}\n",
			want: []string{"doctype"},
		},
		{
			// Trailing whitespace after an element drives
			// writeWhitespaceTrailer's vertical and horizontal branches.
			name: "trailing whitespace variants",
			src:  "package p\n\ntempl x() {\n\t<span>a</span>\n\t<span>b</span> <span>c</span>\n}\n",
			want: []string{"span"},
		},
		{
			name: "css template with an expression property",
			src:  "package p\n\ncss theme(c string) {\n\tcolor: { c };\n\tbackground: #fff;\n}\n",
			want: []string{"color", "background"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateSource(t, tt.src)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("generated output does not contain %q", want)
				}
			}
		})
	}
}

// TestGenerateConstructsSurviveWriteFailures runs the same shapes through
// the failing-writer sweep, so the error branches in these less-travelled
// emitters are exercised too. A swallowed error here writes a truncated
// .go file and reports success.
func TestGenerateConstructsSurviveWriteFailures(t *testing.T) {
	sources := []string{
		"package p\n\ntempl x(items []string) {\n\t{{ count := len(items) }}\n\t<div></div>\n}\n",
		"package p\n\ntempl x() {\n\t<!-- comment -->\n\t<div></div>\n}\n",
		"package p\n\ntempl x(js string) {\n\t<script>{ js }</script>\n}\n",
		"package p\n\ntempl wrapper() {\n\t<div>{ children... }</div>\n}\n",
		"package p\n\ntempl x() {\n\t<br>\n\t<img src=\"a.png\"/>\n}\n",
		"package p\n\ncss theme(c string) {\n\tcolor: { c };\n}\n",
		"package p\n\ntempl x() {\n\t<script type=\"module\" defer>let a = 1;</script>\n}\n",
		"package p\n\ntempl x() {\n\t<!DOCTYPE html>\n\t<html></html>\n}\n",
		"package p\n\ntempl x() {\n\t<span>a</span> <span>b</span>\n}\n",
	}
	for i, src := range sources {
		t.Run(subtestName(i), func(t *testing.T) {
			parsed, err := parser.ParseString(src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			var counter writeCounter
			if _, err := Generate(parsed, &counter); err != nil {
				t.Fatalf("baseline generation failed: %v", err)
			}
			for k := 1; k <= counter.n; k++ {
				if _, err := Generate(parsed, &failAtWrite{n: k}); err == nil {
					t.Fatalf("write %d of %d failed but Generate returned nil", k, counter.n)
				}
			}
		})
	}
}

// subtestName labels the write-failure sweep cases by index, since the
// sources have no single distinguishing token.
func subtestName(i int) string {
	names := []string{"go-code", "html-comment", "raw-element", "children", "void-elements", "css",
		"raw-element-with-attributes", "doctype", "trailing-whitespace"}
	if i < len(names) {
		return names[i]
	}
	return "case"
}
