package analyzer

import (
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/diag"
	parser "github.com/go-monolith/ghtmx/internal/parser"
	"github.com/go-monolith/ghtmx/internal/routes"
)

func collectFiles(t *testing.T, sa *SetAnalysis, files map[string]string) {
	t.Helper()
	for name, src := range files {
		tf, err := parser.ParseString(src)
		if err != nil {
			t.Fatalf("fixture %s does not parse: %v", name, err)
		}
		tf.Filepath = name
		sa.CollectFile(tf)
	}
}

func checkTargets(t *testing.T, files map[string]string, overrides map[string]diag.Severity) []diag.Diagnostic {
	t.Helper()
	sa := NewSetAnalysis()
	collectFiles(t, sa, files)
	sink := diag.NewSink(overrides)
	sa.Check(nil, sink)
	return sink.Diagnostics()
}

func TestTargetEmittedInAnotherFileProducesNoDiagnostic(t *testing.T) {
	diags := checkTargets(t, map[string]string{
		"a.ghtmx": `package main

templ list() {
	<button hx-trigger="click" hx-target="#detail">Go</button>
}
`,
		"b.ghtmx": `package main

templ detail() {
	<div id="detail"></div>
}
`,
	}, nil)
	if len(diags) != 0 {
		t.Fatalf("cross-file IDs must suppress the warning, got %+v", diags)
	}
}

func TestDanglingLiteralTargetWarns(t *testing.T) {
	diags := checkTargets(t, map[string]string{
		"a.ghtmx": `package main

templ list() {
	<button hx-target="#missing">Go</button>
}
`,
	}, nil)
	if len(diags) != 1 {
		t.Fatalf("expected one diagnostic, got %+v", diags)
	}
	d := diags[0]
	if d.ID != diag.DanglingTarget || d.Severity != diag.Warning {
		t.Errorf("expected a W0201 warning, got %+v", d)
	}
	if !strings.Contains(d.Message, "#missing") || !strings.Contains(d.Message, "statically-analyzable literal IDs only") {
		t.Errorf("message must name the selector and the check's scope, got %q", d.Message)
	}
	if d.Pos.File != "a.ghtmx" || d.Pos.Line != 4 {
		t.Errorf("position = %+v", d.Pos)
	}
}

func TestInterpolatedSelectorsAndIDsExempt(t *testing.T) {
	// A dynamic selector is never checked; a dynamic id contributes
	// nothing — but the literal target matching a literal id elsewhere
	// stays satisfied.
	diags := checkTargets(t, map[string]string{
		"a.ghtmx": `package main

templ list(target string, rowID string) {
	<button hx-target={ target }>dyn</button>
	<div id={ rowID }></div>
	<button hx-target="#real">lit</button>
	<div id="real"></div>
}
`,
	}, nil)
	if len(diags) != 0 {
		t.Fatalf("interpolated selectors and IDs are exempt, got %+v", diags)
	}
}

func TestNonIDSelectorsExempt(t *testing.T) {
	diags := checkTargets(t, map[string]string{
		"a.ghtmx": `package main

templ list() {
	<button hx-target="this">a</button>
	<button hx-target="closest form">b</button>
	<button hx-target="next #row">c</button>
	<button hx-target=".card">d</button>
	<button hx-select="#detail .content">e</button>
}
`,
	}, nil)
	if len(diags) != 0 {
		t.Fatalf("only fully-literal ID selectors are checked, got %+v", diags)
	}
}

func TestConditionalBranchIDCountsAsEmitted(t *testing.T) {
	diags := checkTargets(t, map[string]string{
		"a.ghtmx": `package main

templ list(x bool) {
	<button hx-target="#maybe">Go</button>
	<div
		if x {
			id="maybe"
		}
	></div>
}
`,
	}, nil)
	if len(diags) != 0 {
		t.Fatalf("conditionally emitted IDs count conservatively, got %+v", diags)
	}
}

func TestStrictModePromotesToError(t *testing.T) {
	diags := checkTargets(t, map[string]string{
		"a.ghtmx": `package main

templ list() {
	<button hx-target="#missing">Go</button>
}
`,
	}, map[string]diag.Severity{diag.DanglingTarget: diag.Error})
	if len(diags) != 1 || diags[0].Severity != diag.Error {
		t.Fatalf("strict mode must promote to error, got %+v", diags)
	}
}

func TestRecollectingAFileReplacesItsFacts(t *testing.T) {
	sa := NewSetAnalysis()
	collectFiles(t, sa, map[string]string{"a.ghtmx": `package main

templ v1() {
	<button hx-target="#gone">Go</button>
}
`})
	// The file is edited: the dangling target is removed.
	collectFiles(t, sa, map[string]string{"a.ghtmx": `package main

templ v2() {
	<div id="fine"></div>
}
`})
	sink := diag.NewSink(nil)
	sa.Check(nil, sink)
	if got := sink.Diagnostics(); len(got) != 0 {
		t.Fatalf("re-collection must replace stale facts, got %+v", got)
	}
}

func TestUnboundRouteWarns(t *testing.T) {
	table := routes.NewTable()
	if _, ok := table.Add(routes.Route{
		Verb: routes.GET, Path: "/orphan",
		Handler: routes.SymbolRef{PkgPath: "example.com/app", Name: "Orphan"},
		Pos:     routes.Position{File: "main.go", Line: 10, Col: 2},
	}); !ok {
		t.Fatal("add failed")
	}
	if _, ok := table.Add(routes.Route{
		Verb: routes.POST, Path: "/used",
		Handler: routes.SymbolRef{PkgPath: "example.com/app", Name: "Used"},
		Pos:     routes.Position{File: "main.go", Line: 11, Col: 2},
	}); !ok {
		t.Fatal("add failed")
	}

	sa := NewSetAnalysis()
	sa.MarkBound(routes.Route{Verb: routes.POST, Path: "/used"})
	sink := diag.NewSink(nil)
	sa.Check(table, sink)
	diags := sink.Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("expected one unbound-route warning, got %+v", diags)
	}
	d := diags[0]
	if d.ID != diag.UnboundRoute || d.Severity != diag.Warning {
		t.Errorf("expected W0104 warning, got %+v", d)
	}
	if !strings.Contains(d.Message, "GET /orphan") || d.Pos.File != "main.go" || d.Pos.Line != 10 {
		t.Errorf("warning must name the registration site, got %+v", d)
	}
}

func TestNavOnlyRouteExemptFromUnboundWarning(t *testing.T) {
	table := routes.NewTable()
	if _, ok := table.Add(routes.Route{
		Verb: routes.GET, Path: "/admin/audit",
		Handler: routes.SymbolRef{PkgPath: "example.com/app", Name: "AuditLog"},
		Pos:     routes.Position{File: "main.go", Line: 5, Col: 2},
		NavOnly: true,
	}); !ok {
		t.Fatal("add failed")
	}
	if _, ok := table.Add(routes.Route{
		Verb: routes.GET, Path: "/orphan",
		Handler: routes.SymbolRef{PkgPath: "example.com/app", Name: "Orphan"},
		Pos:     routes.Position{File: "main.go", Line: 6, Col: 2},
	}); !ok {
		t.Fatal("add failed")
	}

	sa := NewSetAnalysis()
	sink := diag.NewSink(nil)
	sa.Check(table, sink)
	diags := sink.Diagnostics()
	// The nav-marked route is exempt; the unmarked one still warns, so the
	// check stays on for genuinely orphaned routes.
	if len(diags) != 1 {
		t.Fatalf("expected exactly the non-nav route to warn, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "GET /orphan") {
		t.Errorf("the warning must be about the unmarked route, got %+v", diags[0])
	}
	if !strings.Contains(diags[0].Suggest, "nav") {
		t.Errorf("the remedy must mention the nav marker, got %q", diags[0].Suggest)
	}
}
