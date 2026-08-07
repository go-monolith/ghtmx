package analyzer

import (
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/diag"
	parser "github.com/go-monolith/ghtmx/internal/parser"
)

func validateImports(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	tf, err := parser.ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	tf.Filepath = "app/page.ghtmx"
	sink := diag.NewSink(nil)
	ValidateImports(tf, sink)
	return sink.Diagnostics()
}

func TestRootImportIsE0308(t *testing.T) {
	diags := validateImports(t, `package main

import "github.com/go-monolith/ghtmx"

templ page() {
	<div>hi</div>
}
`)
	if len(diags) != 1 {
		t.Fatalf("expected one diagnostic, got %+v", diags)
	}
	d := diags[0]
	if d.ID != diag.ReservedImport || d.Severity != diag.Error {
		t.Errorf("expected an E0308 error, got %+v", d)
	}
	// Col 8: the spec (the quoted path) after `import `, composed through
	// the first-line branch of the position mapping.
	if d.Pos.File != "app/page.ghtmx" || d.Pos.Line != 3 || d.Pos.Col != 8 {
		t.Errorf("the diagnostic must anchor at the import spec in the template, got %+v", d.Pos)
	}
	if !strings.Contains(d.Suggest, "ghtmxlib") {
		t.Errorf("the remedy must offer the alias escape hatch, got %q", d.Suggest)
	}
}

func TestRootImportAliasedAsDefaultNameIsE0308(t *testing.T) {
	// An explicit alias equal to the package's default name is the same
	// redeclaration as the unaliased form.
	diags := validateImports(t, `package main

import ghtmx "github.com/go-monolith/ghtmx"

templ page() {
	<div>hi</div>
}
`)
	if len(diags) != 1 || diags[0].ID != diag.ReservedImport {
		t.Fatalf("expected one E0308, got %+v", diags)
	}
	if !strings.Contains(diags[0].Suggest, "remove this import") {
		t.Errorf("for the root path the remedy should offer removal first, got %q", diags[0].Suggest)
	}
}

func TestThirdPartyGhtmxNamedPathIsNotFlagged(t *testing.T) {
	// Deliberate false negative, pinned: the package name of a third-party
	// path is not knowable syntax-only, and E0308 cannot be silenced, so a
	// wrong guess would hard-block a legal build. The Go compiler still
	// reports the collision if the package really is named ghtmx.
	diags := validateImports(t, `package main

import "example.com/ghtmx"

templ page() {
	<div>hi</div>
}
`)
	if len(diags) != 0 {
		t.Fatalf("an unaliased third-party path must not be guessed at, got %+v", diags)
	}
}

func TestRootImportInBlockIsPositioned(t *testing.T) {
	diags := validateImports(t, `package main

import (
	"fmt"

	"github.com/go-monolith/ghtmx"
)

templ page() {
	<div>{ fmt.Sprint(1) }</div>
}
`)
	if len(diags) != 1 {
		t.Fatalf("expected one diagnostic, got %+v", diags)
	}
	// The spec sits on its own line, indented by one tab: column 2. This
	// pins the not-first-line branch of the position mapping.
	if got := diags[0].Pos; got.Line != 6 || got.Col != 2 {
		t.Errorf("the diagnostic must anchor at the spec inside the block, got %+v", got)
	}
}

func TestReservedAliasesAreE0308(t *testing.T) {
	diags := validateImports(t, `package main

import (
	ghtmx "example.com/other"
	ghtmxruntime "example.com/another"
)

templ page() {
	<div>hi</div>
}
`)
	if len(diags) != 2 {
		t.Fatalf("expected two diagnostics, got %+v", diags)
	}
	for _, d := range diags {
		if d.ID != diag.ReservedImport {
			t.Errorf("expected E0308, got %+v", d)
		}
		if !strings.Contains(d.Message, "collides") {
			t.Errorf("the message must explain the collision, got %q", d.Message)
		}
	}
}

func TestBenignImportsAreClean(t *testing.T) {
	diags := validateImports(t, `package main

import (
	"fmt"

	ghtmxlib "github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/runtime"
	_ "example.com/sideeffect"
)

templ page() {
	<div>{ fmt.Sprint(ghtmxlib.Version(), runtime.TemplateExtensions) }</div>
}
`)
	if len(diags) != 0 {
		t.Fatalf("aliased root, unaliased runtime, and blank imports are all legal, got %+v", diags)
	}
}

func TestNoImportsIsClean(t *testing.T) {
	diags := validateImports(t, `package main

templ page() {
	<div>hi</div>
}
`)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", diags)
	}
}
