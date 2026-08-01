package generator

import (
	"strings"
	"testing"

	parser "github.com/go-monolith/ghtmx/internal/parser"
)

// TestNestedFragmentParticipatesInline: FR-030 — a fragment declared
// within a page participates at its declaration site via a direct call to
// the shared body, and the body plus both wrappers hoist to file level.
func TestNestedFragmentParticipatesInline(t *testing.T) {
	tf, err := parser.ParseString(`package main

templ page(name string) {
	<div id="wrap">
		fragment Greeting(name string) {
			<h1>{ name }</h1>
		}
	</div>
}
`)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if _, err := Generate(tf, &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	// The declaration site calls the shared body with the enclosing
	// scope's variables, bound by name.
	if !strings.Contains(out, "ghtmxFragmentBody_Greeting(ghtmxruntime.GeneratedComponentInput{Context: ctx, Writer: ghtmx_7f3b9d1a_Buffer}, name)") {
		t.Errorf("declaration site must call the shared body, got:\n%s", out)
	}
	// Exactly one body function, plus both wrappers.
	if got := strings.Count(out, "func ghtmxFragmentBody_Greeting("); got != 1 {
		t.Errorf("expected exactly one body function, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "func Greeting(name string) ghtmx.Component {") {
		t.Errorf("expected the inline wrapper, got:\n%s", out)
	}
	if !strings.Contains(out, "func GreetingFragment(name string) ghtmx.Fragment {") {
		t.Errorf("expected the standalone wrapper, got:\n%s", out)
	}
	// The body markup lives in the body function only.
	if got := strings.Count(out, "<h1>"); got != 1 {
		t.Errorf("body markup must be emitted exactly once, got %d occurrences:\n%s", got, out)
	}
}

// TestTopLevelFragmentEmitsBodyAndWrappers: FR-031 — dual entry points.
func TestTopLevelFragmentEmitsBodyAndWrappers(t *testing.T) {
	tf, err := parser.ParseString(`package main

fragment Row(x string) {
	<tr><td>{ x }</td></tr>
}
`)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if _, err := Generate(tf, &sb); err != nil {
		t.Fatalf("top-level fragments must generate: %v", err)
	}
	out := sb.String()
	for _, want := range []string{
		"func ghtmxFragmentBody_Row(ghtmx_7f3b9d1a_Input ghtmxruntime.GeneratedComponentInput, x string) (ghtmx_7f3b9d1a_Err error) {",
		"func Row(x string) ghtmx.Component {",
		"func RowFragment(x string) ghtmx.Fragment {",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

// TestManyReferencesOneBody: FR-032 — a fragment referenced by several
// pages emits exactly one body function; references call the inline
// wrapper, which calls the body.
func TestManyReferencesOneBody(t *testing.T) {
	tf, err := parser.ParseString(`package main

fragment Row(x string) {
	<tr><td>{ x }</td></tr>
}

templ page1(x string) {
	@Row(x)
}

templ page2(x string) {
	@Row(x)
}

templ page3(x string) {
	@Row(x)
}

templ page4(x string) {
	@Row(x)
}

templ page5(x string) {
	@Row(x)
}
`)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if _, err := Generate(tf, &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if got := strings.Count(out, "func ghtmxFragmentBody_Row("); got != 1 {
		t.Errorf("five referencing pages must share one body, got %d", got)
	}
	if got := strings.Count(out, "Row(x).Render(ctx, ghtmx_7f3b9d1a_Buffer)"); got != 5 {
		t.Errorf("each page calls the inline wrapper, got %d:\n%s", got, out)
	}
}

// TestFragmentNestedInFragmentIsHoisted: a fragment declared inside a
// fragment body renders at its site via a body call and still gets its own
// hoisted body plus both wrappers, even when it is the file's last node.
func TestFragmentNestedInFragmentIsHoisted(t *testing.T) {
	tf, err := parser.ParseString(`package main

fragment Outer(x string) {
	<div>
		fragment Inner(x string) {
			<span>{ x }</span>
		}
	</div>
}
`)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if _, err := Generate(tf, &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "ghtmxFragmentBody_Inner(ghtmxruntime.GeneratedComponentInput{Context: ctx, Writer: ghtmx_7f3b9d1a_Buffer}, x)") {
		t.Errorf("outer body must call the inner body at the declaration site, got:\n%s", out)
	}
	for _, want := range []string{
		"func ghtmxFragmentBody_Outer(",
		"func ghtmxFragmentBody_Inner(",
		"func Inner(x string) ghtmx.Component {",
		"func InnerFragment(x string) ghtmx.Fragment {",
	} {
		if got := strings.Count(out, want); got != 1 {
			t.Errorf("expected exactly one %q, got %d:\n%s", want, got, out)
		}
	}
}

// TestVariadicFragmentForwardsSpread: a variadic parameter forwards to the
// shared body as a spread argument in both wrappers.
func TestVariadicFragmentForwardsSpread(t *testing.T) {
	tf, err := parser.ParseString(`package main

fragment List(title string, xs ...string) {
	<ul>
		for _, x := range xs {
			<li>{ x }</li>
		}
	</ul>
}
`)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if _, err := Generate(tf, &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if got := strings.Count(out, "ghtmxFragmentBody_List(ghtmx_7f3b9d1a_Input, title, xs...)"); got != 2 {
		t.Errorf("both wrappers must forward the variadic parameter as a spread, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "func List(title string, xs ...string) ghtmx.Component {") {
		t.Errorf("wrapper signature must keep the variadic parameter, got:\n%s", out)
	}
}

// TestUnsupportedFragmentSignaturesError: generics and unnamed or blank
// parameters cannot be forwarded to the shared body — generation reports a
// clear error instead of emitting non-compiling code.
func TestUnsupportedFragmentSignaturesError(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"generic", "fragment Row[T any](x T) {\n\t<span>row</span>\n}", "type parameters"},
		{"unnamed", "fragment Row(string) {\n\t<span>row</span>\n}", "must be named"},
		{"blank", "fragment Row(_ string) {\n\t<span>row</span>\n}", "must be named"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tf, err := parser.ParseString("package main\n\n" + tt.src + "\n")
			if err != nil {
				t.Skipf("signature rejected at parse time: %v", err)
			}
			var sb strings.Builder
			if _, err := Generate(tf, &sb); err == nil {
				t.Fatalf("expected a generation error, got none")
			} else if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error must mention %q, got %v", tt.want, err)
			}
		})
	}
}

// TestFragmentBodyClearsChildren: the body clears caller-supplied children
// from ctx so the inline and standalone renders cannot diverge.
func TestFragmentBodyClearsChildren(t *testing.T) {
	tf, err := parser.ParseString(`package main

fragment Row(x string) {
	<tr><td>{ x }</td></tr>
}
`)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if _, err := Generate(tf, &sb); err != nil {
		t.Fatal(err)
	}
	body := sb.String()
	i := strings.Index(body, "func ghtmxFragmentBody_Row(")
	j := strings.Index(body[i:], "\n}\n")
	if i < 0 || j < 0 {
		t.Fatalf("body function not found:\n%s", body)
	}
	if !strings.Contains(body[i:i+j], "ctx = ghtmx.ClearChildren(ctx)") {
		t.Errorf("body must clear children from ctx, got:\n%s", body[i:i+j])
	}
}
