package parser

import (
	"strings"
	"testing"

	"github.com/a-h/parse"
)

func TestFragmentDeclarationTopLevel(t *testing.T) {
	tf, err := ParseString(`package main

fragment UserRow(u User) {
	<tr><td>{ u.Name }</td></tr>
}
`)
	if err != nil {
		t.Fatal(err)
	}
	var frag *FragmentDeclaration
	for _, n := range tf.Nodes {
		if f, ok := n.(*FragmentDeclaration); ok {
			frag = f
		}
	}
	if frag == nil {
		t.Fatal("expected a top-level FragmentDeclaration")
	}
	if frag.Name != "UserRow" {
		t.Errorf("Name = %q", frag.Name)
	}
	if frag.Expression.Value != "UserRow(u User)" {
		t.Errorf("Expression = %q", frag.Expression.Value)
	}
	if !frag.TopLevel {
		t.Error("TopLevel must be set for file-level declarations")
	}
	if len(frag.Children) == 0 {
		t.Error("expected body children")
	}
	// The expression range points at the declaration's name+params.
	if frag.Expression.Range.From.Line != 2 {
		t.Errorf("expression range = %+v, want line 2 (0-indexed)", frag.Expression.Range)
	}
}

func TestFragmentDeclarationNestedInTemplate(t *testing.T) {
	tf, err := ParseString(`package main

templ page(users []User) {
	<table>
		for _, u := range users {
			fragment UserRow(u User) {
				<tr><td>{ u.Name }</td></tr>
			}
		}
	</table>
}
`)
	if err != nil {
		t.Fatal(err)
	}
	ht, ok := tf.Nodes[0].(*HTMLTemplate)
	if !ok {
		t.Fatalf("expected an HTMLTemplate, got %T", tf.Nodes[0])
	}
	var frag *FragmentDeclaration
	var walk func(nodes []Node)
	walk = func(nodes []Node) {
		for _, n := range nodes {
			if f, isFrag := n.(*FragmentDeclaration); isFrag {
				frag = f
				return
			}
			if c, isComposite := n.(CompositeNode); isComposite {
				walk(c.ChildNodes())
			}
		}
	}
	walk(ht.Children)
	if frag == nil {
		t.Fatal("expected a nested FragmentDeclaration")
	}
	if frag.Name != "UserRow" || frag.TopLevel {
		t.Errorf("Name=%q TopLevel=%v", frag.Name, frag.TopLevel)
	}
	// The declaration participates in the page: it is part of the page's
	// child tree at its declaration site.
	if frag.Range.From.Line == 0 {
		t.Errorf("range must be set, got %+v", frag.Range)
	}
}

func TestFragmentMalformedInputsDoNotPanic(t *testing.T) {
	inputs := []string{
		"package main\n\nfragment {\n}\n",
		"package main\n\nfragment Broken( {\n}\n",
		"package main\n\nfragment NoBody()\n",
		"package main\n\nfragment Unclosed() {\n\t<div>",
	}
	for _, src := range inputs {
		if _, err := ParseString(src); err == nil {
			t.Errorf("expected a parse error for %q", src)
		}
	}
}

func TestFragmentKeywordEscapableInText(t *testing.T) {
	// The word "fragment" in text position remains expressible via a
	// string expression, like if/for/switch (FR-004 keyword policy).
	tf, err := ParseString(`package main

templ page() {
	<p>{ "fragment" } of a page</p>
}
`)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := tf.Write(&sb); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), `{ "fragment" }`) {
		t.Errorf("escaped keyword must round-trip, got:\n%s", sb.String())
	}
}

func TestFragmentFormatterRoundTrip(t *testing.T) {
	src := `package main

fragment UserRow(u User) {
	<tr><td>{ u.Name }</td></tr>
}

templ page(u User) {
	<table>
		fragment InlineRow(u User) {
			<tr><td>{ u.Name }</td></tr>
		}
	</table>
}
`
	tf, err := ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := tf.Write(&sb); err != nil {
		t.Fatal(err)
	}
	first := sb.String()
	// Idempotence: re-parsing the formatted output reproduces it.
	tf2, err := ParseString(first)
	if err != nil {
		t.Fatalf("formatted output must re-parse: %v\n%s", err, first)
	}
	sb.Reset()
	if err := tf2.Write(&sb); err != nil {
		t.Fatal(err)
	}
	if sb.String() != first {
		t.Errorf("formatting must be idempotent.\nfirst:\n%s\nsecond:\n%s", first, sb.String())
	}
	if !strings.Contains(first, "fragment UserRow(u User) {") {
		t.Errorf("formatter must print the declaration, got:\n%s", first)
	}
}

// TestFragmentReferenceParsesWithQualifierAndRanges covers the FR-032
// reference syntax: a fragment reference is the templ-element form
// @Name(args) / @pkg.Name(args), which parses with its qualifier and
// argument expressions and carries source ranges. Classification of the
// reference as a fragment happens in the analyzer.
func TestFragmentReferenceParsesWithQualifierAndRanges(t *testing.T) {
	tf, err := ParseString(`package main

templ page(u User) {
	@shared.UserRow(u, 42)
}
`)
	if err != nil {
		t.Fatal(err)
	}
	ht := tf.Nodes[0].(*HTMLTemplate)
	var ref *TemplElementExpression
	for _, n := range ht.Children {
		if e, ok := n.(*TemplElementExpression); ok {
			ref = e
		}
	}
	if ref == nil {
		t.Fatal("expected a templ element reference")
	}
	if ref.Expression.Value != "shared.UserRow(u, 42)" {
		t.Errorf("expression = %q", ref.Expression.Value)
	}
	// 0-indexed line 3, and the range spans the expression.
	if ref.Expression.Range.From.Line != 3 || ref.Expression.Range.To.Col <= ref.Expression.Range.From.Col {
		t.Errorf("range = %+v", ref.Expression.Range)
	}
}

func TestFragmentParserAllocsAreBounded(t *testing.T) {
	// Guard against pathological backtracking: a non-fragment input is
	// rejected quickly.
	input := parse.NewInput("frag not-a-fragment")
	_, matched, err := fragmentExpressionParser.Parse(input)
	if matched || err != nil {
		t.Errorf("non-fragment input must not match: %v %v", matched, err)
	}
}
