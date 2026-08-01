package analyzer

import (
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/diag"
	parser "github.com/go-monolith/ghtmx/internal/parser"
)

func validateFragments(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	tf, err := parser.ParseString(src)
	if err != nil {
		t.Fatalf("fixture does not parse: %v", err)
	}
	tf.Filepath = "app/page.ghtmx"
	sink := diag.NewSink(nil)
	ValidateFragments(tf, sink)
	return sink.Diagnostics()
}

func TestUniqueFragmentNamesPass(t *testing.T) {
	diags := validateFragments(t, `package main

fragment RowA(x string) {
	<tr><td>{ x }</td></tr>
}

fragment RowB(x string) {
	<tr><td>{ x }</td></tr>
}
`)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", diags)
	}
}

func TestDuplicateFragmentNameIsError(t *testing.T) {
	diags := validateFragments(t, `package main

fragment Row(x string) {
	<tr><td>{ x }</td></tr>
}

templ page(x string) {
	<table>
		fragment Row(x string) {
			<tr><td>{ x }</td></tr>
		}
	</table>
}
`)
	if len(diags) != 1 {
		t.Fatalf("expected one diagnostic, got %+v", diags)
	}
	d := diags[0]
	if d.ID != diag.DuplicateFragment || d.Severity != diag.Error {
		t.Errorf("expected an E0301 error, got %+v", d)
	}
	if !strings.Contains(d.Message, `"Row"`) || !strings.Contains(d.Message, "app/page.ghtmx:3") {
		t.Errorf("message must name the fragment and the first site, got %q", d.Message)
	}
}

// Reference resolution tests (FR-032, spec task 38).

func collectSet(t *testing.T, files map[string]struct{ pkg, src string }) *SetAnalysis {
	t.Helper()
	sa := NewSetAnalysis()
	for name, f := range files {
		tf, err := parser.ParseString(f.src)
		if err != nil {
			t.Fatalf("fixture %s does not parse: %v", name, err)
		}
		tf.Filepath = name
		sa.CollectFragments(tf, f.pkg)
	}
	return sa
}

// checkRefs returns only error-severity diagnostics: resolution tests are
// about errors, and fixtures may legitimately trip reachability warnings
// (GHTMX-W0101), which have their own tests in graph_test.go.
func checkRefs(t *testing.T, files map[string]struct{ pkg, src string }) []diag.Diagnostic {
	t.Helper()
	var errors []diag.Diagnostic
	for _, d := range checkAllDiags(t, files) {
		if d.Severity == diag.Error {
			errors = append(errors, d)
		}
	}
	return errors
}

func checkAllDiags(t *testing.T, files map[string]struct{ pkg, src string }) []diag.Diagnostic {
	t.Helper()
	sa := collectSet(t, files)
	sink := diag.NewSink(nil)
	sa.Check(nil, sink)
	return sink.Diagnostics()
}

const sharedFragment = `package shared

fragment UserRow(u string, n int) {
	<tr><td>{ u }</td></tr>
}

fragment secretRow(u string) {
	<tr><td>{ u }</td></tr>
}
`

func TestFragmentReferencedByTwoPagesResolvesToOneDeclaration(t *testing.T) {
	files := map[string]struct{ pkg, src string }{
		"shared/rows.ghtmx": {"example.com/app/shared", sharedFragment},
		"a/page1.ghtmx": {"example.com/app/a", `package a

import "example.com/app/shared"

templ page1(u string) {
	@shared.UserRow(u, 1)
}
`},
		"b/page2.ghtmx": {"example.com/app/b", `package b

import "example.com/app/shared"

templ page2(u string) {
	@shared.UserRow(u, 2)
}
`},
	}
	diags := checkRefs(t, files)
	if len(diags) != 0 {
		t.Fatalf("both references must resolve, got %+v", diags)
	}
	// Both resolve to the single registry entry.
	sa := collectSet(t, files)
	reg := sa.Fragments()
	info, ok := reg["example.com/app/shared"]["UserRow"]
	if !ok || info.Pos.File != "shared/rows.ghtmx" || info.ParamCount != 2 {
		t.Fatalf("registry entry = %+v ok=%v", info, ok)
	}
}

func TestCrossPackageUnexportedFragmentIsError(t *testing.T) {
	diags := checkRefs(t, map[string]struct{ pkg, src string }{
		"shared/rows.ghtmx": {"example.com/app/shared", sharedFragment},
		"a/page.ghtmx": {"example.com/app/a", `package a

import "example.com/app/shared"

templ page(u string) {
	@shared.secretRow(u)
}
`},
	})
	if len(diags) != 1 || diags[0].ID != diag.UnresolvableFragment {
		t.Fatalf("expected E0303, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "shared.secretRow") || !strings.Contains(diags[0].Message, "unexported") {
		t.Errorf("message must name the fragment, got %q", diags[0].Message)
	}
}

func TestSamePackageUnexportedFragmentResolves(t *testing.T) {
	diags := checkRefs(t, map[string]struct{ pkg, src string }{
		"shared/rows.ghtmx": {"example.com/app/shared", sharedFragment},
		"shared/page.ghtmx": {"example.com/app/shared", `package shared

templ page(u string) {
	@secretRow(u)
}
`},
	})
	if len(diags) != 0 {
		t.Fatalf("same-package unexported references are fine, got %+v", diags)
	}
}

func TestFragmentArityMismatchIsError(t *testing.T) {
	diags := checkRefs(t, map[string]struct{ pkg, src string }{
		"shared/rows.ghtmx": {"example.com/app/shared", sharedFragment},
		"a/page.ghtmx": {"example.com/app/a", `package a

import "example.com/app/shared"

templ page(u string) {
	@shared.UserRow(u)
}
`},
	})
	if len(diags) != 1 || diags[0].ID != diag.UnresolvableFragment {
		t.Fatalf("expected E0303 arity error, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "takes 2 argument(s), got 1") {
		t.Errorf("message must state both arities, got %q", diags[0].Message)
	}
}

func TestUncalledFragmentReferenceIsError(t *testing.T) {
	diags := checkRefs(t, map[string]struct{ pkg, src string }{
		"shared/rows.ghtmx": {"example.com/app/shared", sharedFragment},
		"a/page.ghtmx": {"example.com/app/a", `package a

import "example.com/app/shared"

templ page() {
	@shared.UserRow
}
`},
	})
	if len(diags) != 1 || !strings.Contains(diags[0].Message, "must be called") {
		t.Fatalf("expected a must-be-called error, got %+v", diags)
	}
}

func TestOrdinaryComponentReferencesIgnored(t *testing.T) {
	diags := checkRefs(t, map[string]struct{ pkg, src string }{
		"a/page.ghtmx": {"example.com/app/a", `package a

import "example.com/app/components"

templ page(u string) {
	@components.Button(u)
	@localComponent(u)
	@unknownpkg.Thing(u)
}
`},
	})
	if len(diags) != 0 {
		t.Fatalf("non-fragment references are the Go compiler's business, got %+v", diags)
	}
}

func TestChildrenExpressionInFragmentIsError(t *testing.T) {
	diags := validateFragments(t, `package main

fragment Card(title string) {
	<div class="card">
		<h2>{ title }</h2>
		{ children... }
	</div>
}
`)
	if len(diags) != 1 {
		t.Fatalf("expected one diagnostic, got %+v", diags)
	}
	d := diags[0]
	if d.ID != diag.FragmentChildren || d.Severity != diag.Error {
		t.Errorf("expected an E0302 error, got %+v", d)
	}
	if !strings.Contains(d.Message, `"Card"`) {
		t.Errorf("message must name the fragment, got %q", d.Message)
	}
}

func TestChildrenExpressionOutsideFragmentIsFine(t *testing.T) {
	diags := validateFragments(t, `package main

templ layout(title string) {
	<div>
		{ children... }
		fragment Row(x string) {
			<tr><td>{ x }</td></tr>
		}
	</div>
}
`)
	if len(diags) != 0 {
		t.Fatalf("children in the enclosing template is templ-conformant, got %+v", diags)
	}
}

func TestCrossFileDuplicateFragmentIsError(t *testing.T) {
	diags := checkRefs(t, map[string]struct{ pkg, src string }{
		"app/a.ghtmx": {"example.com/app", `package app

fragment Row(x string) {
	<tr><td>{ x }</td></tr>
}
`},
		"app/b.ghtmx": {"example.com/app", `package app

fragment Row(x string) {
	<tr><td>{ x }</td></tr>
}
`},
	})
	if len(diags) != 1 {
		t.Fatalf("expected one diagnostic, got %+v", diags)
	}
	d := diags[0]
	if d.ID != diag.DuplicateFragment || d.Severity != diag.Error {
		t.Errorf("expected an E0301 error, got %+v", d)
	}
	if d.Pos.File != "app/b.ghtmx" || !strings.Contains(d.Message, "app/a.ghtmx") {
		t.Errorf("diagnostic must point at the later file and name the first, got %+v", d)
	}
}

func TestSameNameDifferentPackagesIsFine(t *testing.T) {
	diags := checkRefs(t, map[string]struct{ pkg, src string }{
		"a/rows.ghtmx": {"example.com/app/a", `package a

fragment Row(x string) {
	<tr><td>{ x }</td></tr>
}
`},
		"b/rows.ghtmx": {"example.com/app/b", `package b

fragment Row(x string) {
	<tr><td>{ x }</td></tr>
}
`},
	})
	if len(diags) != 0 {
		t.Fatalf("entry points are package-scoped, got %+v", diags)
	}
}

func TestVariadicFragmentArity(t *testing.T) {
	files := map[string]struct{ pkg, src string }{
		"app/list.ghtmx": {"example.com/app", `package app

fragment List(title string, xs ...string) {
	<h1>{ title }</h1>
}

templ ok1(title string) {
	@List(title)
}

templ ok2(title string, xs []string) {
	@List(title, xs...)
}

templ ok3(title string) {
	@List(title, "a", "b")
}

templ bad() {
	@List()
}
`},
	}
	diags := checkRefs(t, files)
	if len(diags) != 1 {
		t.Fatalf("only the zero-argument call is too short, got %+v", diags)
	}
	if diags[0].ID != diag.UnresolvableFragment || !strings.Contains(diags[0].Message, "at least 1") {
		t.Errorf("expected an E0303 minimum-arity error, got %+v", diags[0])
	}
}

func TestChildrenBlockOnFragmentReferenceIsError(t *testing.T) {
	diags := checkRefs(t, map[string]struct{ pkg, src string }{
		"app/page.ghtmx": {"example.com/app", `package app

fragment Row(x string) {
	<tr><td>{ x }</td></tr>
}

templ page(x string) {
	@Row(x) {
		<p>never renders</p>
	}
}
`},
	})
	if len(diags) != 1 {
		t.Fatalf("expected one diagnostic, got %+v", diags)
	}
	if diags[0].ID != diag.FragmentChildren {
		t.Errorf("expected an E0302 error, got %+v", diags[0])
	}
}

func TestChildrenInNestedFragmentReportedOnceForInner(t *testing.T) {
	diags := validateFragments(t, `package main

fragment Outer(x string) {
	<div>
		fragment Inner(x string) {
			{ children... }
		}
	</div>
}
`)
	if len(diags) != 1 {
		t.Fatalf("expected exactly one diagnostic, got %+v", diags)
	}
	if diags[0].ID != diag.FragmentChildren || !strings.Contains(diags[0].Message, `"Inner"`) {
		t.Errorf("the innermost enclosing fragment is named, got %+v", diags[0])
	}
}

func TestCrossFileDuplicateReportsOncePerFile(t *testing.T) {
	diags := checkRefs(t, map[string]struct{ pkg, src string }{
		"app/a.ghtmx": {"example.com/app", `package app

fragment Row(x string) {
	<tr><td>{ x }</td></tr>
}
`},
		"app/b.ghtmx": {"example.com/app", `package app

fragment Row(x string) {
	<tr><td>{ x }</td></tr>
}

fragment Row(x string) {
	<tr><td>{ x }</td></tr>
}
`},
	})
	// The same-file duplicate in b.ghtmx belongs to ValidateFragments; the
	// cross-file check reports b's first Row against a's, exactly once.
	if len(diags) != 1 {
		t.Fatalf("expected exactly one cross-file diagnostic, got %+v", diags)
	}
	if diags[0].ID != diag.DuplicateFragment || diags[0].Pos.File != "app/b.ghtmx" {
		t.Errorf("expected one E0301 pointing at b.ghtmx, got %+v", diags[0])
	}
}

func TestUnicodeExportedFragmentResolvesCrossPackage(t *testing.T) {
	diags := checkRefs(t, map[string]struct{ pkg, src string }{
		"shared/rows.ghtmx": {"example.com/app/shared", `package shared

fragment Übersicht(x string) {
	<tr><td>{ x }</td></tr>
}
`},
		"a/page.ghtmx": {"example.com/app/a", `package a

import "example.com/app/shared"

templ page(x string) {
	@shared.Übersicht(x)
}
`},
	})
	if len(diags) != 0 {
		t.Fatalf("Ü is an exported identifier, got %+v", diags)
	}
}
