package parser_test

import (
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/parser"
)

// The parser's error branches are spread thin: roughly a hundred
// separate "this token is not what I expected" sites, one or two
// statements each, across twenty-five files. Writing a hand-crafted case
// per site would be a hundred tests that each pin one message.
//
// Mutating real templates reaches them in bulk instead, and asserts the
// property that actually matters: whatever you do to a .ghtmx file, the
// parser either understands it or reports an error — it never panics,
// never hangs, and never returns a tree it cannot reproduce. That is the
// contract the LSP depends on, because it parses on every keystroke and
// every intermediate state is a mutation of something valid.

// mutationSeeds are templates chosen to span the grammar: elements and
// attributes, control flow, expressions, and the three declaration kinds
// that are not `templ`.
var mutationSeeds = []string{
	"package p\n\ntempl x(name string, items []string) {\n" +
		"\t<div class=\"a\" data-x={ name }>\n" +
		"\t\tif name != \"\" {\n\t\t\t<span>{ name }</span>\n\t\t} else {\n\t\t\t<b>none</b>\n\t\t}\n" +
		"\t\tfor _, i := range items {\n\t\t\t<li>{ i }</li>\n\t\t}\n" +
		"\t</div>\n}\n",
	"package p\n\ntempl x(n int) {\n\tswitch n {\n\t\tcase 1:\n\t\t\t<p>one</p>\n\t\tdefault:\n\t\t\t<p>many</p>\n\t}\n}\n",
	"package p\n\ncss theme(c string) {\n\tcolor: { c };\n\tbackground: #fff;\n}\n",
	"package p\n\nscript onClick(msg string) {\n\tconsole.log(msg)\n}\n",
	"package p\n\nfragment Row(id string) {\n\t<tr><td>{ id }</td></tr>\n}\n",
	"package p\n\nevent Saved(id string)\n\ntempl x() {\n\t<div hx-on:saved=\"go()\"></div>\n}\n",
	"package p\n\ntempl x(attrs ghtmx.Attributes) {\n\t<input { attrs... } disabled?={ true }/>\n}\n",
	"package p\n\ntempl inner() {\n\t<b></b>\n}\n\ntempl outer() {\n\t@inner() {\n\t\t<i></i>\n\t}\n}\n",
}

// parseMustNotPanic parses src and reports a panic as a failure. The
// result is deliberately ignored: for a mutated template either outcome
// is legitimate, and asserting one would be asserting the mutation's
// meaning rather than the parser's robustness.
func parseMustNotPanic(t *testing.T, label, src string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("parsing panicked on %s: %v\nsource:\n%s", label, r, src)
		}
	}()
	_, _ = parser.ParseString(src)
}

// TestDeletingAnyByteIsHandled removes one byte at a time. A deletion is
// what a backspace produces, so every one of these is a state the LSP
// sees in a real editing session.
func TestDeletingAnyByteIsHandled(t *testing.T) {
	for si, seed := range mutationSeeds {
		t.Run(seedName(si), func(t *testing.T) {
			for i := range len(seed) {
				mutated := seed[:i] + seed[i+1:]
				parseMustNotPanic(t, "byte deletion at "+itoa(i), mutated)
			}
		})
	}
}

// TestCorruptingAnyByteIsHandled replaces one byte at a time with each
// of the characters the grammar treats specially. This is what reaches
// the "expected X, got Y" branches: a brace where a bracket belongs, a
// quote in the middle of an expression.
func TestCorruptingAnyByteIsHandled(t *testing.T) {
	replacements := []byte{'{', '}', '<', '>', '"', '\'', '(', ')', '/', '=', '\n', '\t', '@', '!', '.'}

	for si, seed := range mutationSeeds {
		t.Run(seedName(si), func(t *testing.T) {
			for i := range len(seed) {
				// One replacement per position, cycling through the set:
				// the full cross product is 15x the work for no extra
				// branches once the corpus is this varied.
				r := replacements[i%len(replacements)]
				if seed[i] == r {
					continue
				}
				mutated := seed[:i] + string(r) + seed[i+1:]
				parseMustNotPanic(t, "byte "+itoa(i)+" replaced with "+string(r), mutated)
			}
		})
	}
}

// TestDroppingAnyLineIsHandled removes whole lines, which is what a
// cut, an unfinished paste, or an interrupted refactor leaves behind.
// Unlike single bytes this reliably produces unbalanced blocks.
func TestDroppingAnyLineIsHandled(t *testing.T) {
	for si, seed := range mutationSeeds {
		t.Run(seedName(si), func(t *testing.T) {
			lines := strings.Split(seed, "\n")
			for i := range lines {
				mutated := strings.Join(append(append([]string{}, lines[:i]...), lines[i+1:]...), "\n")
				parseMustNotPanic(t, "line "+itoa(i)+" dropped", mutated)
			}
		})
	}
}

// TestTruncatingAtAnyLineIsHandled is the half-typed-file case at line
// granularity, complementing the byte-level truncation sweep in
// malformed_test.go.
func TestTruncatingAtAnyLineIsHandled(t *testing.T) {
	for si, seed := range mutationSeeds {
		t.Run(seedName(si), func(t *testing.T) {
			lines := strings.Split(seed, "\n")
			for i := range lines {
				parseMustNotPanic(t, "truncated at line "+itoa(i), strings.Join(lines[:i], "\n"))
			}
		})
	}
}

// TestDuplicatingAnyLineIsHandled covers the other half of a bad paste:
// a repeated closing brace, a duplicated attribute, a second package
// clause.
func TestDuplicatingAnyLineIsHandled(t *testing.T) {
	for si, seed := range mutationSeeds {
		t.Run(seedName(si), func(t *testing.T) {
			lines := strings.Split(seed, "\n")
			for i := range lines {
				doubled := append([]string{}, lines[:i+1]...)
				doubled = append(doubled, lines[i])
				doubled = append(doubled, lines[i+1:]...)
				parseMustNotPanic(t, "line "+itoa(i)+" duplicated", strings.Join(doubled, "\n"))
			}
		})
	}
}

// TestWhatParsesAlsoWrites is the strong assertion the sweeps above
// deliberately avoid: anything the parser *accepts* must be reproducible
// by the writer. A tree that cannot be written back is worse than a
// parse error, because `ghtmx fmt` would silently truncate the file.
func TestWhatParsesAlsoWrites(t *testing.T) {
	for si, seed := range mutationSeeds {
		t.Run(seedName(si), func(t *testing.T) {
			lines := strings.Split(seed, "\n")
			for i := range lines {
				mutated := strings.Join(append(append([]string{}, lines[:i]...), lines[i+1:]...), "\n")
				parsed, err := parser.ParseString(mutated)
				if err != nil {
					continue
				}
				var sb strings.Builder
				if err := parsed.Write(&sb); err != nil {
					t.Errorf("line %d dropped: parsed but could not be written back: %v\nsource:\n%s", i, err, mutated)
				}
			}
		})
	}
}

func seedName(i int) string {
	names := []string{
		"element-and-control-flow", "switch", "css", "script",
		"fragment", "event", "spread-attributes", "templ-element",
	}
	if i < len(names) {
		return names[i]
	}
	return "seed" + itoa(i)
}
