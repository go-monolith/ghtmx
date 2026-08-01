package analyzer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/diag"
)

// Reference-graph tests: GHTMX-W0101 unused fragments (FR-033) and
// GHTMX-E0306 reference cycles (FR-053).

func graphDiags(t *testing.T, files map[string]struct{ pkg, src string }, id string) []diag.Diagnostic {
	t.Helper()
	var out []diag.Diagnostic
	for _, d := range checkAllDiags(t, files) {
		if d.ID == id {
			out = append(out, d)
		}
	}
	return out
}

// TestHandlerRenderedFragmentDoesNotWarn: a fragment whose generated
// <name>Fragment entry point is called from hand-written Go source
// (reported by route discovery via MarkGoFragmentRefs) counts as
// rendered — FR-034's handler-explicit path is not an unused fragment.
func TestHandlerRenderedFragmentDoesNotWarn(t *testing.T) {
	files := map[string]struct{ pkg, src string }{
		"app/rows.ghtmx": {"example.com/app", `package app

fragment editRow(x string) {
	<tr><td>{ x }</td></tr>
}

fragment stillOrphan(x string) {
	<tr><td>{ x }</td></tr>
}
`},
	}
	sa := collectSet(t, files)
	sa.MarkGoFragmentRefs(map[string]bool{"editRow": true})
	sink := diag.NewSink(nil)
	sa.Check(nil, sink)
	var warned []string
	for _, d := range sink.Diagnostics() {
		if d.ID == diag.UnusedFragment {
			warned = append(warned, d.Message)
		}
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "stillOrphan") {
		t.Fatalf("only the genuinely unused fragment must warn, got %v", warned)
	}
	// A later rediscovery replacing the set must be honored.
	sa.MarkGoFragmentRefs(map[string]bool{"editRow": true, "stillOrphan": true})
	sink = diag.NewSink(nil)
	sa.Check(nil, sink)
	for _, d := range sink.Diagnostics() {
		if d.ID == diag.UnusedFragment {
			t.Fatalf("no fragment should warn after both are handler-rendered, got %s", d.Message)
		}
	}
}

func TestUnusedFragmentWarns(t *testing.T) {
	diags := graphDiags(t, map[string]struct{ pkg, src string }{
		"app/page.ghtmx": {"example.com/app", `package app

fragment Orphan(x string) {
	<tr><td>{ x }</td></tr>
}

templ page() {
	<p>no reference to Orphan</p>
}
`},
	}, diag.UnusedFragment)
	if len(diags) != 1 {
		t.Fatalf("expected one warning, got %+v", diags)
	}
	d := diags[0]
	if d.Severity != diag.Warning {
		t.Errorf("W0101 must be a warning, not fail the build, got %+v", d)
	}
	if d.Pos.File != "app/page.ghtmx" || d.Pos.Line != 3 {
		t.Errorf("the warning must name the declaration site, got %+v", d.Pos)
	}
	if !strings.Contains(d.Message, `"Orphan"`) {
		t.Errorf("the warning must name the fragment, got %q", d.Message)
	}
}

func TestReferencedAndNestedFragmentsDoNotWarn(t *testing.T) {
	diags := graphDiags(t, map[string]struct{ pkg, src string }{
		"shared/rows.ghtmx": {"example.com/app/shared", `package shared

fragment UserRow(u string) {
	<tr><td>{ u }</td></tr>
}
`},
		"app/page.ghtmx": {"example.com/app", `package app

import "example.com/app/shared"

templ page(u string) {
	@shared.UserRow(u)
	fragment Inline(x string) {
		<span>{ x }</span>
	}
}
`},
	}, diag.UnusedFragment)
	if len(diags) != 0 {
		t.Fatalf("cross-package references and nested declaration sites both count as use, got %+v", diags)
	}
}

func TestDirectCycleIsError(t *testing.T) {
	diags := graphDiags(t, map[string]struct{ pkg, src string }{
		"app/frag.ghtmx": {"example.com/app", `package app

fragment Loop(x string) {
	<div>
		@Loop(x)
	</div>
}
`},
	}, diag.CircularReference)
	if len(diags) != 1 {
		t.Fatalf("expected one cycle error, got %+v", diags)
	}
	d := diags[0]
	if d.Severity != diag.Error {
		t.Errorf("E0306 must be an error, got %+v", d)
	}
	if !strings.Contains(d.Message, "Loop -> Loop") {
		t.Errorf("the chain must list the cycle, got %q", d.Message)
	}
}

func TestIndirectCycleListsFullChain(t *testing.T) {
	diags := graphDiags(t, map[string]struct{ pkg, src string }{
		"app/chain.ghtmx": {"example.com/app", `package app

templ a() {
	@b()
}

templ b() {
	@c()
}

templ c() {
	@a()
}
`},
	}, diag.CircularReference)
	if len(diags) != 1 {
		t.Fatalf("one cycle must be reported once, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "a -> b -> c -> a") {
		t.Errorf("the full chain must be listed, got %q", diags[0].Message)
	}
}

func TestCrossPackageCycleQualifiesNames(t *testing.T) {
	diags := graphDiags(t, map[string]struct{ pkg, src string }{
		"a/a.ghtmx": {"example.com/app/a", `package a

import "example.com/app/b"

templ PageA() {
	@b.PageB()
}
`},
		"b/b.ghtmx": {"example.com/app/b", `package b

import "example.com/app/a"

templ PageB() {
	@a.PageA()
}
`},
	}, diag.CircularReference)
	if len(diags) != 1 {
		t.Fatalf("expected one cycle error, got %+v", diags)
	}
	want := "example.com/app/a.PageA -> example.com/app/b.PageB -> example.com/app/a.PageA"
	if !strings.Contains(diags[0].Message, want) {
		t.Errorf("cross-package chains use qualified names, got %q", diags[0].Message)
	}
}

func TestNestedFragmentDeclarationSiteClosesCycle(t *testing.T) {
	// page renders Row at its declaration site; Row references page.
	diags := graphDiags(t, map[string]struct{ pkg, src string }{
		"app/page.ghtmx": {"example.com/app", `package app

templ page(x string) {
	<table>
		fragment Row(x string) {
			@page(x)
		}
	</table>
}
`},
	}, diag.CircularReference)
	if len(diags) != 1 {
		t.Fatalf("the declaration site is a render edge, got %+v", diags)
	}
	// Roots are visited in sorted key order, so the chain starts at Row.
	if !strings.Contains(diags[0].Message, "Row -> page -> Row") {
		t.Errorf("chain must include the nested declaration edge, got %q", diags[0].Message)
	}
}

func TestAcyclicGraphNoCycleError(t *testing.T) {
	diags := graphDiags(t, map[string]struct{ pkg, src string }{
		"app/page.ghtmx": {"example.com/app", `package app

fragment Row(x string) {
	<tr><td>{ x }</td></tr>
}

templ page(x string) {
	@Row(x)
	@Row(x)
}

templ other(x string) {
	@page(x)
}
`},
	}, diag.CircularReference)
	if len(diags) != 0 {
		t.Fatalf("diamond-shaped reuse is not a cycle, got %+v", diags)
	}
}

func TestCycleDetectionIsDeterministicAndIterative(t *testing.T) {
	// A chain of 5000 templates ending in a cycle back to the first:
	// recursion-based traversal would exhaust the stack, and repeated runs
	// must produce identical output.
	const n = 5000
	var sb strings.Builder
	sb.WriteString("package app\n\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "templ t%04d() {\n\t@t%04d()\n}\n\n", i, (i+1)%n)
	}
	files := map[string]struct{ pkg, src string }{
		"app/chain.ghtmx": {"example.com/app", sb.String()},
	}

	first := graphDiags(t, files, diag.CircularReference)
	if len(first) != 1 {
		t.Fatalf("expected exactly one cycle error, got %d", len(first))
	}
	if got := strings.Count(first[0].Message, "->"); got != n {
		t.Errorf("the chain must list all %d edges, got %d", n, got)
	}
	second := graphDiags(t, files, diag.CircularReference)
	if len(second) != 1 || first[0].Message != second[0].Message || first[0].Pos != second[0].Pos {
		t.Errorf("detection must be deterministic across runs")
	}
}

func TestTwoIndependentCyclesBothReported(t *testing.T) {
	diags := graphDiags(t, map[string]struct{ pkg, src string }{
		"app/cycles.ghtmx": {"example.com/app", `package app

templ a() {
	@b()
}

templ b() {
	@a()
}

fragment X(v string) {
	@Y(v)
}

fragment Y(v string) {
	@X(v)
}
`},
	}, diag.CircularReference)
	if len(diags) != 2 {
		t.Fatalf("both independent cycles must be reported, got %+v", diags)
	}
}

func TestParameterShadowingProducesNoFalseCycle(t *testing.T) {
	// layout's parameter `row` shadows the like-named fragment; the call
	// @row("x") targets the parameter, so there is no cycle and no
	// resolution error even though fragment row references layout.
	files := map[string]struct{ pkg, src string }{
		"app/page.ghtmx": {"example.com/app", `package app

fragment row(x string) {
	@layout(nil)
}

templ layout(row func(string) ghtmx.Component) {
	<div>
		@row("x")
	</div>
}

templ page() {
	@row("y")
}
`},
	}
	if diags := graphDiags(t, files, diag.CircularReference); len(diags) != 0 {
		t.Errorf("a shadowed name is the parameter, not the fragment: %+v", diags)
	}
	if diags := checkRefs(t, files); len(diags) != 0 {
		t.Errorf("shadowed references must skip resolution checks, got %+v", diags)
	}
}

func TestBareShadowedComponentValueNotAnError(t *testing.T) {
	// card's `body` parameter is rendered as a bare component value; the
	// like-named fragment must not trigger a must-be-called error.
	files := map[string]struct{ pkg, src string }{
		"app/page.ghtmx": {"example.com/app", `package app

fragment body(x string) {
	<p>{ x }</p>
}

templ card(body ghtmx.Component) {
	<div>
		@body
	</div>
}

templ page() {
	@body("hello")
}
`},
	}
	if diags := checkRefs(t, files); len(diags) != 0 {
		t.Errorf("bare shadowed reference is a component value, got %+v", diags)
	}
}

func TestLegacyCallSyntaxCountsAsUseAndEdges(t *testing.T) {
	// The legacy {! Comp(x) } form references components too: it marks
	// fragments used and participates in cycle detection.
	files := map[string]struct{ pkg, src string }{
		"app/page.ghtmx": {"example.com/app", `package app

fragment Row(x string) {
	<tr><td>{ x }</td></tr>
}

templ page(x string) {
	{! Row(x) }
}
`},
	}
	if diags := graphDiags(t, files, diag.UnusedFragment); len(diags) != 0 {
		t.Errorf("a legacy-syntax reference counts as use, got %+v", diags)
	}

	cycle := map[string]struct{ pkg, src string }{
		"app/cycle.ghtmx": {"example.com/app", `package app

templ a() {
	{! b() }
}

templ b() {
	{! a() }
}
`},
	}
	if diags := graphDiags(t, cycle, diag.CircularReference); len(diags) != 1 {
		t.Errorf("legacy-syntax edges must close cycles, got %+v", diags)
	}
}

func TestSameCycleThroughTwoBackEdgesReportedOnce(t *testing.T) {
	diags := graphDiags(t, map[string]struct{ pkg, src string }{
		"app/cycle.ghtmx": {"example.com/app", `package app

templ a() {
	@b()
}

templ b() {
	@a()
	@a()
}
`},
	}, diag.CircularReference)
	if len(diags) != 1 {
		t.Fatalf("one cycle must be reported once regardless of back-edge count, got %+v", diags)
	}
}

func TestBareReferencesAddNoEdges(t *testing.T) {
	// An uncalled reference is a component value, never a render call, so
	// it closes no cycle (it is separately an E0303 for fragments).
	diags := graphDiags(t, map[string]struct{ pkg, src string }{
		"app/page.ghtmx": {"example.com/app", `package app

fragment Row(x string) {
	@page
}

templ page() {
	@Row("x")
}
`},
	}, diag.CircularReference)
	if len(diags) != 0 {
		t.Errorf("bare references must not close cycles, got %+v", diags)
	}
}

func TestGenericInstantiationRefParses(t *testing.T) {
	tests := []struct {
		expr     string
		name     string
		argCount int
	}{
		{"List[int](xs)", "List", 1},
		{"Pair[string, int](a, b)", "Pair", 2},
		{"pkg.List[int](xs)", "List", 1},
		{"List[int]", "List", -1},
	}
	for _, tt := range tests {
		ref, ok := parseFragmentRef(tt.expr)
		if !ok || ref.name != tt.name || ref.argCount != tt.argCount {
			t.Errorf("parseFragmentRef(%q) = %+v ok=%v, want name %q argCount %d", tt.expr, ref, ok, tt.name, tt.argCount)
		}
	}
}

func TestSignatureParams(t *testing.T) {
	tests := []struct {
		sig  string
		want []string
	}{
		{"Name(u User, n int)", []string{"u", "n"}},
		{"Name(a, b string)", []string{"a", "b"}},
		{"Name()", nil},
		{"(r Repo) Name(x string)", []string{"r", "x"}},
		{"Name[T any](v T)", []string{"v"}},
		{"Name(f func(int) string)", []string{"f"}},
	}
	for _, tt := range tests {
		got := signatureParams(tt.sig)
		if len(got) != len(tt.want) {
			t.Errorf("signatureParams(%q) = %v, want %v", tt.sig, got, tt.want)
			continue
		}
		for _, w := range tt.want {
			if !got[w] {
				t.Errorf("signatureParams(%q) missing %q: %v", tt.sig, w, got)
			}
		}
	}
}
