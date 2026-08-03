package generator

import (
	"regexp"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/parser"
)

// When `ghtmx generate` resolves a symbol binding, the analyzer folds it
// into the registered path and the generator never sees it. The language
// server has no route table and generates straight from the parsed
// template, so the binding does reach the generator — and the default
// emission demands a ghtmx.SafeURL of it, which a handler func is not.
// That mismatch is what surfaced in editors as a false IncompatibleAssign
// on templates that build perfectly.
//
// Bindings the analyzer would not have lowered — constructor calls,
// generated-package references, literals — keep their type either way.

const hxBindingSrc = "package p\n\ntempl x() {\n" +
	"\t<button hx-post={ CreateItem }>Create</button>\n}\n"

// generateString generates src and returns the Go output.
func generateString(t *testing.T, src string, opts ...GenerateOpt) (string, GeneratorOutput) {
	t.Helper()
	tf, err := parser.ParseString(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	w := new(strings.Builder)
	op, err := Generate(tf, w, opts...)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return w.String(), op
}

// TestEditorBindingsDropTheTypeExpectation: with the option the handler
// is no longer assigned to a ghtmx.SafeURL, so gopls has nothing to
// reject. The positive half matters as much as the absence: the
// declaration must survive, or the variable the attribute is written
// from is undeclared and the generated Go is broken in a new way.
func TestEditorBindingsDropTheTypeExpectation(t *testing.T) {
	got, _ := generateString(t, hxBindingSrc, WithEditorBindings("ghtmxgen"))
	if strings.Contains(got, "ghtmx.SafeURL = CreateItem") {
		t.Errorf("the handler is still assigned to a ghtmx.SafeURL:\n%s", got)
	}
	if !strings.Contains(got, "_ = CreateItem") {
		t.Errorf("expected the handler kept as a bare reference:\n%s", got)
	}
	if !regexp.MustCompile(`var \w+ ghtmx\.SafeURL\n`).MatchString(got) {
		t.Errorf("the SafeURL variable must still be declared; the attribute is written from it:\n%s", got)
	}
}

// TestConstructorCallsKeepTheirType: the relaxation covers only what the
// analyzer would have lowered. A route constructor call reaches the
// generator in a real build too, its result really must be a
// ghtmx.SafeURL, and the editor should keep checking that.
func TestConstructorCallsKeepTheirType(t *testing.T) {
	tests := []struct {
		name  string
		attr  string
		relax bool
	}{
		{"bare handler", "{ CreateItem }", true},
		{"qualified handler", "{ handlers.CreateItem }", true},
		{"constructor call", "{ ghtmxgen.SaveItem(id) }", false},
		{"generated package reference", "{ ghtmxgen.ListItemsPath }", false},
		{"literal", `{ "/items" }`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "package p\n\ntempl x(id string) {\n\t<button hx-post=" + tt.attr + ">go</button>\n}\n"
			got, _ := generateString(t, src, WithEditorBindings("ghtmxgen"))
			relaxed := !strings.Contains(got, "ghtmx.SafeURL = ")
			if relaxed != tt.relax {
				t.Errorf("relaxed = %v, want %v for %s:\n%s", relaxed, tt.relax, tt.attr, got)
			}
		})
	}
}

// TestEditorBindingsKeepTheExpression is the other half, and the half a
// lowering-based fix fails: deleting the expression also deletes the
// position gopls completes, defines and hovers at inside `{ … }`
// (FR-081, FR-083). The symbol must survive, and stay source-mapped.
func TestEditorBindingsKeepTheExpression(t *testing.T) {
	got, op := generateString(t, hxBindingSrc, WithEditorBindings("ghtmxgen"))
	if !strings.Contains(got, "CreateItem") {
		t.Fatalf("the bound handler vanished from the generated Go:\n%s", got)
	}

	// Walk the same path the editor does: the cursor's template position
	// maps to a Go position, and gopls answers at whatever is there.
	srcLine, srcCol := findInSource(t, hxBindingSrc, "CreateItem")
	tgt, ok := op.SourceMap.TargetPositionFromSource(srcLine, srcCol)
	if !ok {
		t.Fatal("the bound handler has no source-map entry; completion and hover inside { … } resolve through it")
	}
	lines := strings.Split(got, "\n")
	if int(tgt.Line) >= len(lines) {
		t.Fatalf("the mapped target line %d is past the end of %d generated lines", tgt.Line, len(lines))
	}
	targetLine := lines[tgt.Line]
	if !strings.Contains(targetLine, "CreateItem") {
		t.Errorf("the mapped position lands on %q, which does not hold the handler", targetLine)
	}
}

// findInSource returns the 0-based line and column of substr in src.
func findInSource(t *testing.T, src, substr string) (line, col uint32) {
	t.Helper()
	for i, l := range strings.Split(src, "\n") {
		if c := strings.Index(l, substr); c >= 0 {
			return uint32(i), uint32(c)
		}
	}
	t.Fatalf("%q not found in source", substr)
	return 0, 0
}

// TestDefaultBindingEmissionIsUnchanged: the option is for the editor
// only. Everything that compiles for real keeps the SafeURL assignment,
// which is what makes the escaping contract enforceable even if analysis
// were bypassed.
func TestDefaultBindingEmissionIsUnchanged(t *testing.T) {
	got, _ := generateString(t, hxBindingSrc)
	if !strings.Contains(got, "ghtmx.SafeURL = CreateItem") {
		t.Errorf("the default emission must still demand a ghtmx.SafeURL:\n%s", got)
	}
}

// TestEditorBindingsLeaveOtherURLAttributesAlone: the option targets the
// five route-aware verb attributes. An href takes a real Go expression
// that is expected to produce a URL, and its emission must not change.
func TestEditorBindingsLeaveOtherURLAttributesAlone(t *testing.T) {
	src := "package p\n\ntempl x(u string) {\n\t<a href={ ghtmx.URL(u) }>go</a>\n}\n"
	withOpt, _ := generateString(t, src, WithEditorBindings("ghtmxgen"))
	without, _ := generateString(t, src)
	if withOpt != without {
		t.Errorf("href emission changed under WithEditorBindings:\n--- with ---\n%s\n--- without ---\n%s", withOpt, without)
	}
}
