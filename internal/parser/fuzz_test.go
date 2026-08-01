package parser

import (
	"io"
	"testing"
)

// FuzzParseString is the whole-file robustness target for NFR-006: the
// lexer and parser must never panic or fail to terminate on any input.
// When an input parses successfully, the resulting AST must also survive
// diagnosis and printing (the formatter path) without panicking — errors
// are fine, panics are the defect.
func FuzzParseString(f *testing.F) {
	seeds := []string{
		"",
		"package main\n",
		"package main\n\ntempl hello() {\n\t<div>Hello</div>\n}\n",
		"package main\n\ntempl hello(name string) {\n\t<h1 id=\"x\" class={ cls }>{ name }</h1>\n}\n",
		"package main\n\ntempl c() {\n\tif x > 0 {\n\t\t<span>+</span>\n\t} else {\n\t\t<span>-</span>\n\t}\n}\n",
		"package main\n\ntempl c(items []string) {\n\tfor _, i := range items {\n\t\t<li>{ i }</li>\n\t}\n}\n",
		"package main\n\ntempl c(x int) {\n\tswitch x {\n\t\tcase 1:\n\t\t\t<b>1</b>\n\t\tdefault:\n\t\t\t<i>?</i>\n\t}\n}\n",
		"package main\n\ntempl page() {\n\t@header(\"t\")\n\t{ children... }\n}\n",
		"package main\n\ncss red() {\n\tcolor: #ff0000;\n}\n",
		"package main\n\nscript alertit(msg string) {\n\talert(msg)\n}\n",
		"package main\n\ntempl c() {\n\t<script>console.log(\"x\", { y })</script>\n}\n",
		"package main\n\ntempl c() {\n\t<button hx-post=\"/x\" hx-target=\"#out\">Go</button>\n}\n",
		"package main\n\ntempl c(attrs templ.Attributes) {\n\t<div { attrs... } data-x?={ true }></div>\n}\n",
		"package main\n\nfragment Row(u User) {\n\t<tr><td>{ u.Name }</td></tr>\n}\n",
		"package main\n\ntempl page(u User) {\n\t<table>\n\t\tfragment Row(u User) {\n\t\t\t<tr></tr>\n\t\t}\n\t</table>\n}\n",
		"package main\n\nfragment Broken( {\n",
		"package main\n\nevent UserCreated(id string, name string)\n",
		"package main\n\nevent CartCleared()\n",
		"package main\n\nevent broken(\n",
		"package main\n\ntempl broken() {\n\t<div>",
		"package main\n\ntempl broken() {\n\t{ unclosed\n}\n",
		"templ orphan() { <p>no package</p> }",
		"package main\n\n// comment\n\nvar x = 1\n\ntempl c() {\n\t<!-- html comment -->\n\t<!DOCTYPE html>\n}\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		tf, err := ParseString(src)
		if err != nil {
			// Malformed input must yield an error, never a panic.
			return
		}
		if tf == nil {
			return
		}
		// A successfully parsed tree must survive the shared-AST consumers.
		_, _ = Diagnose(tf)
		_ = tf.Write(io.Discard)
	})
}
