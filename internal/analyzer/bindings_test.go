package analyzer

import (
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/diag"
	"github.com/go-monolith/ghtmx/internal/generator/central"
	"github.com/go-monolith/ghtmx/internal/htmxsurface"
	parser "github.com/go-monolith/ghtmx/internal/parser"
	"github.com/go-monolith/ghtmx/internal/routes"
)

func testTable(t *testing.T) *routes.Table {
	t.Helper()
	table := routes.NewTable()
	add := func(r routes.Route) {
		t.Helper()
		if _, ok := table.Add(r); !ok {
			t.Fatalf("failed to add %+v", r)
		}
	}
	handlers := "example.com/app/handlers"
	add(routes.Route{Verb: routes.POST, Path: "/users", Handler: routes.SymbolRef{PkgPath: handlers, Name: "CreateUser"}})
	add(routes.Route{Verb: routes.GET, Path: "/users", Handler: routes.SymbolRef{PkgPath: handlers, Name: "ListUsers"}})
	add(routes.Route{Verb: routes.GET, Path: "/users/{id}", Handler: routes.SymbolRef{PkgPath: handlers, Name: "GetUser"}, Params: []routes.RouteParam{{Name: "id"}}})
	add(routes.Route{Verb: routes.DELETE, Path: "/sessions", Handler: routes.SymbolRef{PkgPath: "example.com/app", Name: "EndSession"}})
	add(routes.Route{Verb: routes.AnyVerb, Path: "/health", Handler: routes.SymbolRef{PkgPath: handlers, Name: "Health"}})
	add(routes.Route{Verb: routes.PUT, Path: "/a&b", Handler: routes.SymbolRef{PkgPath: handlers, Name: "Amp"}})
	return table
}

const bindingHeader = `package main

import "example.com/app/handlers"

`

func resolve(t *testing.T, src string) (*parser.TemplateFile, []diag.Diagnostic) {
	t.Helper()
	tf, err := parser.ParseString(src)
	if err != nil {
		t.Fatalf("fixture does not parse: %v", err)
	}
	tf.Filepath = "app/page.ghtmx"
	surface, err := htmxsurface.ForVersion("2.0.10")
	if err != nil {
		t.Fatal(err)
	}
	sink := diag.NewSink(nil)
	ResolveBindings(tf, BindingEnv{
		Table:            testTable(t),
		Surface:          surface,
		PkgPath:          "example.com/app",
		GeneratedPkgName: "ghtmxgen",
	}, sink)
	return tf, sink.Diagnostics()
}

// renderedAttr finds the first hx-* attribute in the file and reports how
// it will be emitted.
func firstHxAttribute(t *testing.T, tf *parser.TemplateFile) parser.Attribute {
	t.Helper()
	var found parser.Attribute
	for _, node := range tf.Nodes {
		ht, ok := node.(*parser.HTMLTemplate)
		if !ok {
			continue
		}
		walkNodes(ht.Children, func(n parser.Node) {
			e, ok := n.(*parser.Element)
			if !ok || found != nil {
				return
			}
			for _, a := range e.Attributes {
				key := ""
				switch attr := a.(type) {
				case *parser.ConstantAttribute:
					key = keyName(attr.Key)
				case *parser.ExpressionAttribute:
					key = keyName(attr.Key)
				}
				if strings.HasPrefix(key, "hx-") {
					found = a
					return
				}
			}
		})
	}
	if found == nil {
		t.Fatal("no hx-* attribute found")
	}
	return found
}

func TestSymbolBindingLowersToRegisteredPath(t *testing.T) {
	tf, diags := resolve(t, bindingHeader+`templ page() {
	<button hx-post={ handlers.CreateUser }>Create</button>
}
`)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", diags)
	}
	attr := firstHxAttribute(t, tf)
	ca, ok := attr.(*parser.ConstantAttribute)
	if !ok {
		t.Fatalf("binding was not lowered to a constant attribute: %T", attr)
	}
	if ca.Value != "/users" {
		t.Errorf("lowered value = %q, want /users", ca.Value)
	}
}

func TestAllFiveVerbsResolve(t *testing.T) {
	table := testTable(t)
	if _, ok := table.Add(routes.Route{Verb: routes.PATCH, Path: "/p", Handler: routes.SymbolRef{PkgPath: "example.com/app/handlers", Name: "P"}}); !ok {
		t.Fatal("add failed")
	}
	surface, _ := htmxsurface.ForVersion("2.0.10")
	src := bindingHeader + `templ page() {
	<a hx-get={ handlers.ListUsers }>l</a>
	<a hx-post={ handlers.CreateUser }>c</a>
	<a hx-patch={ handlers.P }>p</a>
	<a hx-delete={ EndSession }>d</a>
}
`
	tf, err := parser.ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	tf.Filepath = "app/page.ghtmx"
	sink := diag.NewSink(nil)
	ResolveBindings(tf, BindingEnv{Table: table, Surface: surface, PkgPath: "example.com/app", GeneratedPkgName: "ghtmxgen"}, sink)
	if diags := sink.Diagnostics(); len(diags) != 0 {
		t.Fatalf("expected all verbs to resolve, got %+v", diags)
	}
}

func TestBareIdentResolvesInCurrentPackage(t *testing.T) {
	tf, diags := resolve(t, `package main

templ page() {
	<button hx-delete={ EndSession }>End</button>
}
`)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", diags)
	}
	ca, ok := firstHxAttribute(t, tf).(*parser.ConstantAttribute)
	if !ok || ca.Value != "/sessions" {
		t.Fatalf("bare ident did not lower correctly: %+v", firstHxAttribute(t, tf))
	}
}

func TestAnyVerbRouteBindableFromEveryVerb(t *testing.T) {
	_, diags := resolve(t, bindingHeader+`templ page() {
	<a hx-get={ handlers.Health }>h</a>
}
`)
	if len(diags) != 0 {
		t.Fatalf("AnyVerb routes must be bindable, got %+v", diags)
	}
}

func TestLoweredPathIsAttributeEscaped(t *testing.T) {
	tf, diags := resolve(t, bindingHeader+`templ page() {
	<a hx-put={ handlers.Amp }>x</a>
}
`)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", diags)
	}
	ca := firstHxAttribute(t, tf).(*parser.ConstantAttribute)
	if ca.Value != "/a&amp;b" {
		t.Errorf("path must be HTML-attribute-escaped once at compile time, got %q", ca.Value)
	}
}

func TestUnknownHandlerNamesTheSymbol(t *testing.T) {
	_, diags := resolve(t, bindingHeader+`templ page() {
	<button hx-post={ handlers.Missing }>x</button>
}
`)
	if len(diags) != 1 || diags[0].ID != diag.UnknownHandler {
		t.Fatalf("expected E0101, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "example.com/app/handlers.Missing") {
		t.Errorf("error must name the symbol, got %q", diags[0].Message)
	}
	if diags[0].Pos.File != "app/page.ghtmx" || diags[0].Pos.Line != 6 {
		t.Errorf("position = %+v", diags[0].Pos)
	}
}

func TestVerbMismatchStatesBothVerbs(t *testing.T) {
	_, diags := resolve(t, bindingHeader+`templ page() {
	<button hx-get={ handlers.CreateUser }>x</button>
}
`)
	if len(diags) != 1 || diags[0].ID != diag.VerbMismatch {
		t.Fatalf("expected E0102, got %+v", diags)
	}
	msg := diags[0].Message
	if !strings.Contains(msg, "GET") || !strings.Contains(msg, "POST") {
		t.Errorf("error must state both expected and actual verbs, got %q", msg)
	}
}

func TestParameterisedRouteDirectsToConstructor(t *testing.T) {
	_, diags := resolve(t, bindingHeader+`templ page() {
	<a hx-get={ handlers.GetUser }>x</a>
}
`)
	if len(diags) != 1 || diags[0].ID != diag.ParameterisedBinding {
		t.Fatalf("expected E0103, got %+v", diags)
	}
	if !strings.Contains(diags[0].Suggest, "ghtmxgen.GetUser") {
		t.Errorf("suggestion must direct to the constructor, got %q", diags[0].Suggest)
	}
}

func TestUnimportedPackageIsError(t *testing.T) {
	_, diags := resolve(t, `package main

templ page() {
	<button hx-post={ other.Create }>x</button>
}
`)
	if len(diags) != 1 || diags[0].ID != diag.UnknownHandler {
		t.Fatalf("expected E0101 for the unimported package, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, `"other"`) {
		t.Errorf("error must name the missing package, got %q", diags[0].Message)
	}
}

func TestConstructorCallsPassThroughUnlowered(t *testing.T) {
	tf, diags := resolve(t, bindingHeader+`templ page(id string) {
	<a hx-get={ ghtmxgen.GetUser(id) }>c</a>
}
`)
	if len(diags) != 0 {
		t.Fatalf("constructor calls without the table still pass classification, got %+v", diags)
	}
	if _, ok := firstHxAttribute(t, tf).(*parser.ExpressionAttribute); !ok {
		t.Error("constructor expressions must be left untouched")
	}
}

func TestNonVerbHxAttributesUntouched(t *testing.T) {
	tf, diags := resolve(t, bindingHeader+`templ page(mode string) {
	<div hx-swap={ mode }></div>
}
`)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", diags)
	}
	if _, ok := firstHxAttribute(t, tf).(*parser.ExpressionAttribute); !ok {
		t.Error("non-verb attributes must not be resolved")
	}
}

func resolveWithConstructors(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	tf, err := parser.ParseString(src)
	if err != nil {
		t.Fatalf("fixture does not parse: %v", err)
	}
	tf.Filepath = "app/page.ghtmx"
	surface, err := htmxsurface.ForVersion("2.0.10")
	if err != nil {
		t.Fatal(err)
	}
	table := testTable(t)
	constructors, conflicts := central.Naming(table)
	if len(conflicts) != 0 {
		t.Fatalf("unexpected naming conflicts: %+v", conflicts)
	}
	sink := diag.NewSink(nil)
	ResolveBindings(tf, BindingEnv{
		Table:            table,
		Surface:          surface,
		PkgPath:          "example.com/app",
		GeneratedPkgName: "ghtmxgen",
		Constructors:     constructors,
	}, sink)
	return sink.Diagnostics()
}

func TestConstructorCallValid(t *testing.T) {
	diags := resolveWithConstructors(t, bindingHeader+`templ page(id string) {
	<a hx-get={ ghtmxgen.GetUser(id) }>u</a>
	<a hx-get={ ghtmxgen.ListUsersPath }>l</a>
}
`)
	if len(diags) != 0 {
		t.Fatalf("expected valid constructor usage, got %+v", diags)
	}
}

func TestConstructorUnknown(t *testing.T) {
	diags := resolveWithConstructors(t, bindingHeader+`templ page(id string) {
	<a hx-get={ ghtmxgen.GetUserz(id) }>u</a>
}
`)
	if len(diags) != 1 || diags[0].ID != diag.UnknownHandler {
		t.Fatalf("expected E0101 for the unknown constructor, got %+v", diags)
	}
}

func TestConstructorArityMismatch(t *testing.T) {
	diags := resolveWithConstructors(t, bindingHeader+`templ page(id string) {
	<a hx-get={ ghtmxgen.GetUser(id, "extra") }>u</a>
}
`)
	if len(diags) != 1 || diags[0].ID != diag.ConstructorArity {
		t.Fatalf("expected E0104 for wrong arity, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "takes 1 argument(s) (id), got 2") {
		t.Errorf("message must state expected and actual arity, got %q", diags[0].Message)
	}
}

func TestConstructorVerbMismatch(t *testing.T) {
	diags := resolveWithConstructors(t, bindingHeader+`templ page(id string) {
	<a hx-post={ ghtmxgen.GetUser(id) }>u</a>
}
`)
	if len(diags) != 1 || diags[0].ID != diag.VerbMismatch {
		t.Fatalf("expected E0102, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "POST") || !strings.Contains(diags[0].Message, "GET") {
		t.Errorf("message must state both verbs, got %q", diags[0].Message)
	}
}

func TestConstructorMustBeCalled(t *testing.T) {
	diags := resolveWithConstructors(t, bindingHeader+`templ page() {
	<a hx-get={ ghtmxgen.GetUser }>u</a>
}
`)
	if len(diags) != 1 || diags[0].ID != diag.ConstructorArity {
		t.Fatalf("expected a must-be-called diagnostic, got %+v", diags)
	}
}

// Carve-out reporter tests (FR-004, spec task 34). The string inputs for
// carve-out 1 are the exact forms the corpus exclusions in CONFORMANCE.md
// used before adaptation.

func TestCarveOut1StringURL(t *testing.T) {
	for _, src := range []string{
		`templ page() {
	<button hx-post="/click">Click</button>
}
`,
		`templ page() {
	<form hx-post="/api/secret/unlock"></form>
}
`,
	} {
		diags := validate(t, "2.0.10", "package main\n\n"+src)
		if len(diags) != 1 || diags[0].ID != diag.CarveOutStringURL {
			t.Fatalf("expected E0602, got %+v", diags)
		}
		d := diags[0]
		if !strings.Contains(d.Message, "carve-out 1") {
			t.Errorf("message must name the carve-out, got %q", d.Message)
		}
		if !strings.Contains(d.Suggest, "handlers.MyHandler") || !strings.Contains(d.Suggest, "ghtmxgen.MyRoute") {
			t.Errorf("suggestion must name the replacement constructs, got %q", d.Suggest)
		}
	}
}

func TestCarveOut1ArbitraryExpression(t *testing.T) {
	_, diags := resolve(t, bindingHeader+`templ page(url string) {
	<a hx-get={ url + "?x=1" }>a</a>
}
`)
	if len(diags) != 1 || diags[0].ID != diag.CarveOutTypedBinding {
		t.Fatalf("expected E0601, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "carve-out 1") {
		t.Errorf("message must name the carve-out, got %q", diags[0].Message)
	}
}

func TestCarveOut2AuthorEscaping(t *testing.T) {
	for _, expr := range []string{"ghtmx.URL(u)", "ghtmx.SafeURL(u)", "templ.URL(u)"} {
		_, diags := resolve(t, bindingHeader+`templ page(u string) {
	<a hx-get={ `+expr+` }>a</a>
}
`)
		if len(diags) != 1 || diags[0].ID != diag.CarveOutAuthorEscaping {
			t.Fatalf("expr %s: expected E0603, got %+v", expr, diags)
		}
		if !strings.Contains(diags[0].Message, "carve-out 2") {
			t.Errorf("message must name the carve-out, got %q", diags[0].Message)
		}
	}
}

func TestCarveOutsExemptDynamicKeysAndNonVerbAttributes(t *testing.T) {
	_, diags := resolve(t, bindingHeader+`templ page(mode string) {
	<div hx-swap={ mode }></div>
	<div { "hx-" + "post" }="/x"></div>
}
`)
	if len(diags) != 0 {
		t.Fatalf("dynamic keys and non-verb attributes are exempt, got %+v", diags)
	}
}
