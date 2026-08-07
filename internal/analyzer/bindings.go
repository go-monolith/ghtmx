package analyzer

import (
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	"go/token"
	"html"
	"sort"
	"strings"

	"github.com/go-monolith/ghtmx/internal/diag"
	"github.com/go-monolith/ghtmx/internal/generator/central"
	"github.com/go-monolith/ghtmx/internal/htmxsurface"
	parser "github.com/go-monolith/ghtmx/internal/parser"
	"github.com/go-monolith/ghtmx/internal/routes"
)

// BindingEnv is the context binding resolution needs beyond the AST.
type BindingEnv struct {
	// Table is the discovered route table.
	Table *routes.Table
	// Surface supplies the verb for each hx-* attribute.
	Surface *htmxsurface.Surface
	// PkgPath is the import path of the Go package containing the template
	// file; bare handler identifiers resolve against it.
	PkgPath string
	// GeneratedPkgName is the central generated package's name
	// (ghtmxgen by default): constructor calls on it are typed route
	// constructors.
	GeneratedPkgName string
	// Constructors maps generated constructor names to their routes,
	// produced by central.Naming over the same table.
	Constructors map[string]central.Constructor
	// SetAnalysis, when set, receives bound-route marks for the
	// unreachable-route check (FR-043).
	SetAnalysis *SetAnalysis
}

// ResolveBindings resolves route-aware hx-* verb bindings against the
// route table (FR-020, FR-022, FR-040) and lowers each resolved
// symbol binding into a constant attribute carrying the handler's
// registered path, so the generator folds it into the surrounding static
// markup with zero runtime cost.
//
// Diagnostics: GHTMX-E0101 unknown handler (naming the symbol),
// GHTMX-E0102 verb mismatch (stating expected and actual verbs),
// GHTMX-E0103 direct binding to a parameterised route (directing to the
// generated constructor).
//
// Expressions that are not symbol references — constructor calls on the
// generated package, and arbitrary expressions — are left untouched here:
// constructor emission and the carve-out reporter are separate passes.
func ResolveBindings(file *parser.TemplateFile, env BindingEnv, sink *diag.Sink) {
	if env.Table == nil || env.Surface == nil {
		return
	}
	r := &bindingResolver{file: file, env: env, sink: sink, imports: templateImports(file)}
	for _, node := range file.Nodes {
		switch t := node.(type) {
		case *parser.HTMLTemplate:
			walkNodes(t.Children, r.visitNode)
		case *parser.FragmentDeclaration:
			walkNodes(t.Children, r.visitNode)
		}
	}
	// Lowering folds bound symbols into static markup; keep each bound
	// symbol referenced so its import stays used and a renamed handler
	// still breaks the generated file at compile time (P1, D9).
	if len(r.loweredRefs) > 0 {
		refs := make([]string, 0, len(r.loweredRefs))
		for ref := range r.loweredRefs {
			refs = append(refs, ref)
		}
		sort.Strings(refs)
		var b strings.Builder
		b.WriteString("// Handlers bound from hx-* attributes: the references keep imports\n")
		b.WriteString("// used and the symbols compile-checked.\n")
		b.WriteString("var (\n")
		for _, ref := range refs {
			b.WriteString("\t_ = " + ref + "\n")
		}
		b.WriteString(")")
		file.Nodes = append(file.Nodes, &parser.TemplateFileGoExpression{
			Expression: parser.Expression{Value: b.String()},
		})
	}
}

type bindingResolver struct {
	file        *parser.TemplateFile
	env         BindingEnv
	sink        *diag.Sink
	imports     map[string]string
	loweredRefs map[string]bool
}

func (r *bindingResolver) visitNode(n parser.Node) {
	switch e := n.(type) {
	case *parser.Element:
		r.resolveAttributes(e.Attributes)
	case *parser.ScriptElement:
		r.resolveAttributes(e.Attributes)
	case *parser.RawElement:
		r.resolveAttributes(e.Attributes)
	}
}

func (r *bindingResolver) resolveAttributes(attrs []parser.Attribute) {
	for i, a := range attrs {
		switch attr := a.(type) {
		case *parser.ExpressionAttribute:
			if replacement, ok := r.resolveExpressionAttribute(attr); ok {
				attrs[i] = replacement
			}
		case *parser.ConditionalAttribute:
			r.resolveAttributes(attr.Then)
			r.resolveAttributes(attr.Else)
		}
	}
}

// verbFor returns the HTTP verb when the attribute is one of the five
// route-aware verb attributes.
func (r *bindingResolver) verbFor(key parser.AttributeKey) (routes.Verb, parser.Range, bool) {
	name, rng, ok := constantKey(key)
	if !ok || !strings.HasPrefix(name, "hx-") {
		return "", parser.Range{}, false
	}
	def, ok := r.env.Surface.Attribute(name)
	if !ok || def.Verb == "" {
		return "", parser.Range{}, false
	}
	return routes.Verb(def.Verb), rng, true
}

func (r *bindingResolver) resolveExpressionAttribute(attr *parser.ExpressionAttribute) (parser.Attribute, bool) {
	verb, keyRange, ok := r.verbFor(attr.Key)
	if !ok {
		return nil, false
	}
	// The file ATTEMPTS a route binding: mark it now, not on success, so
	// registering a missing route later re-invalidates the file that
	// failed to bind to it (FR-061 tier one).
	if r.env.SetAnalysis != nil {
		r.env.SetAnalysis.MarkBindingFile(r.file.Filepath)
	}
	expr, err := goparser.ParseExpr(attr.Expression.Value)
	if err != nil {
		// Unparseable expressions surface as Go compile errors in the
		// generated code; nothing to resolve here.
		return nil, false
	}

	var sym routes.SymbolRef
	switch e := expr.(type) {
	case *goast.Ident:
		sym = routes.SymbolRef{PkgPath: r.env.PkgPath, Name: e.Name}
	case *goast.CallExpr:
		// A call on the central generated package is a typed route
		// constructor (FR-021): existence, verb agreement, and arity are
		// checked here; argument types are the Go compiler's (D9).
		sel, ok := e.Fun.(*goast.SelectorExpr)
		if !ok {
			r.carveOut1(attr)
			return nil, false
		}
		pkgIdent, ok := sel.X.(*goast.Ident)
		if !ok {
			r.carveOut1(attr)
			return nil, false
		}
		if pkgIdent.Name != r.env.GeneratedPkgName {
			// templ carve-out 2 (FR-004, S1.1): author-selected escaping —
			// ghtmx.URL / ghtmx.SafeURL at a binding site.
			if isEscapingSelector(pkgIdent.Name, sel.Sel.Name) {
				r.sink.Add(diag.CarveOutAuthorEscaping, r.pos(attr.Expression.Range),
					fmt.Sprintf("templ carve-out 2: %s.%s selects URL escaping by hand, but the escaping context at a route-binding site is fixed by the engine", pkgIdent.Name, sel.Sel.Name),
					"pass values through a generated route constructor; each parameter is percent-encoded for its position automatically")
				return nil, false
			}
			r.carveOut1(attr)
			return nil, false
		}
		r.checkConstructorCall(attr, verb, sel.Sel.Name, len(e.Args))
		return nil, false
	case *goast.SelectorExpr:
		pkgIdent, ok := e.X.(*goast.Ident)
		if !ok {
			r.carveOut1(attr)
			return nil, false
		}
		if pkgIdent.Name == r.env.GeneratedPkgName {
			r.checkConstructorReference(attr, verb, e.Sel.Name)
			return nil, false
		}
		path, imported := r.imports[pkgIdent.Name]
		if !imported {
			r.sink.Add(diag.UnknownHandler, r.pos(attr.Expression.Range),
				fmt.Sprintf("unknown handler %s.%s: package %q is not imported in this template", pkgIdent.Name, e.Sel.Name, pkgIdent.Name),
				"add the import to the template's import block")
			return nil, false
		}
		sym = routes.SymbolRef{PkgPath: path, Name: e.Sel.Name}
	default:
		// Anything else on a verb attribute is templ carve-out 1.
		r.carveOut1(attr)
		return nil, false
	}

	route, found := r.env.Table.Lookup(verb, sym)
	if !found {
		if registered := r.env.Table.ByHandler(sym); len(registered) > 0 {
			verbs := registeredVerbs(registered)
			r.sink.Add(diag.VerbMismatch, r.pos(attr.Expression.Range),
				fmt.Sprintf("verb mismatch: %s expects a %s route, but %s is registered for %s", keyName(attr.Key), verb, sym, strings.Join(verbs, ", ")),
				fmt.Sprintf("use hx-%s, or register the handler for %s", strings.ToLower(string(verb)), verb))
			return nil, false
		}
		r.sink.Add(diag.UnknownHandler, r.pos(attr.Expression.Range),
			fmt.Sprintf("unknown handler %s: no route is registered for it", sym),
			"register the route in Go code, or declare it with a //ghtmx:route annotation")
		return nil, false
	}
	if len(route.Params) > 0 {
		r.sink.Add(diag.ParameterisedBinding, r.pos(attr.Expression.Range),
			fmt.Sprintf("%s has path parameters (%s): a direct symbol binding cannot supply them", sym, route.Path),
			fmt.Sprintf("call the generated constructor instead: %s={ %s.%s(...) }", keyName(attr.Key), r.env.GeneratedPkgName, constructorName(route)),
		)
		return nil, false
	}

	// Lower to a constant attribute: the path becomes part of the static
	// markup. It is HTML-attribute-escaped here, once, at compile time.
	_ = keyRange
	if r.loweredRefs == nil {
		r.loweredRefs = map[string]bool{}
	}
	r.loweredRefs[strings.TrimSpace(attr.Expression.Value)] = true
	r.markBound(route)
	return &parser.ConstantAttribute{
		Key:        attr.Key,
		Value:      html.EscapeString(route.Path),
		ValueRange: attr.Expression.Range,
		Range:      attr.Range,
	}, true
}

// markBound records a route as bound for the reachability check, and the
// binding file for dependency-graph invalidation (FR-061).
func (r *bindingResolver) markBound(route routes.Route) {
	if r.env.SetAnalysis != nil {
		r.env.SetAnalysis.MarkBound(route)
		r.env.SetAnalysis.MarkBindingFile(r.file.Filepath)
	}
}

// carveOut1 reports an arbitrary expression on a verb attribute
// (GHTMX-E0601, templ carve-out 1).
func (r *bindingResolver) carveOut1(attr *parser.ExpressionAttribute) {
	name := keyName(attr.Key)
	r.sink.Add(diag.CarveOutTypedBinding, r.pos(attr.Expression.Range),
		fmt.Sprintf("templ carve-out 1: %s takes a typed route binding, not an arbitrary expression", name),
		fmt.Sprintf("bind a handler symbol (%s={ handlers.MyHandler }) or a generated route constructor (%s={ %s.MyRoute(...) })", name, name, r.env.GeneratedPkgName))
}

// isEscapingSelector reports author-facing URL escaping helpers.
func isEscapingSelector(pkg, name string) bool {
	if pkg != "ghtmx" && pkg != "templ" {
		return false
	}
	switch name {
	case "URL", "SafeURL", "JoinURLErrs":
		return true
	}
	return false
}

// checkConstructorCall validates ghtmxgen.Name(args...) against the
// constructor table.
func (r *bindingResolver) checkConstructorCall(attr *parser.ExpressionAttribute, verb routes.Verb, name string, argCount int) {
	if r.env.Constructors == nil {
		return
	}
	c, ok := r.env.Constructors[name]
	if !ok {
		r.sink.Add(diag.UnknownHandler, r.pos(attr.Expression.Range),
			fmt.Sprintf("unknown route constructor %s.%s: no route generates it", r.env.GeneratedPkgName, name),
			"run `ghtmx routes` to list discovered routes and their generated symbols")
		return
	}
	route := c.Route
	r.markBound(route)
	if route.Verb != verb && route.Verb != routes.AnyVerb {
		r.sink.Add(diag.VerbMismatch, r.pos(attr.Expression.Range),
			fmt.Sprintf("verb mismatch: %s expects a %s route, but %s.%s is the %s route %s", keyName(attr.Key), verb, r.env.GeneratedPkgName, name, route.Verb, route.Path),
			fmt.Sprintf("use hx-%s, or bind a %s route", strings.ToLower(string(route.Verb)), verb))
		return
	}
	if argCount != len(route.Params) {
		names := make([]string, len(route.Params))
		for i, p := range route.Params {
			names[i] = p.Name
		}
		r.sink.Add(diag.ConstructorArity, r.pos(attr.Expression.Range),
			fmt.Sprintf("%s.%s takes %d argument(s) (%s), got %d", r.env.GeneratedPkgName, name, len(route.Params), strings.Join(names, ", "), argCount),
			fmt.Sprintf("the parameter list matches the path parameters of %s in order", route.Path))
	}
}

// checkConstructorReference validates a non-call selector on the generated
// package: <Name>Path constants pass with verb agreement; a bare
// constructor reference must be called.
func (r *bindingResolver) checkConstructorReference(attr *parser.ExpressionAttribute, verb routes.Verb, name string) {
	if r.env.Constructors == nil {
		return
	}
	if base, isConst := strings.CutSuffix(name, "Path"); isConst {
		if c, ok := r.env.Constructors[base]; ok && len(c.Route.Params) == 0 {
			r.markBound(c.Route)
			if c.Route.Verb != verb && c.Route.Verb != routes.AnyVerb {
				r.sink.Add(diag.VerbMismatch, r.pos(attr.Expression.Range),
					fmt.Sprintf("verb mismatch: %s expects a %s route, but %s.%s is the path of a %s route", keyName(attr.Key), verb, r.env.GeneratedPkgName, name, c.Route.Verb), "")
			}
			return
		}
	}
	if c, ok := r.env.Constructors[name]; ok {
		r.sink.Add(diag.ConstructorArity, r.pos(attr.Expression.Range),
			fmt.Sprintf("%s.%s is a route constructor and must be called", r.env.GeneratedPkgName, name),
			fmt.Sprintf("write %s={ %s.%s(...) } with %d argument(s)", keyName(attr.Key), r.env.GeneratedPkgName, name, len(c.Route.Params)))
		return
	}
	r.sink.Add(diag.UnknownHandler, r.pos(attr.Expression.Range),
		fmt.Sprintf("unknown route symbol %s.%s", r.env.GeneratedPkgName, name),
		"run `ghtmx routes` to list discovered routes and their generated symbols")
}

func keyName(key parser.AttributeKey) string {
	if name, _, ok := constantKey(key); ok {
		return name
	}
	return key.String()
}

func registeredVerbs(rs []routes.Route) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rs {
		v := string(r.Verb)
		if r.Verb == routes.AnyVerb {
			v = "any method"
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// constructorName mirrors the naming the central generated package will use
// for a route's typed constructor: the handler symbol's name.
// constructorName is the symbol the generated central package will
// carry for a route, which is what the E0103 suggestion has to name.
// It goes through the generator's own transform rather than using the
// handler name raw: an unexported handler is exported there (getUser →
// GetUser), and a method handler's dotted name is folded
// (Handlers.ListUsers → HandlersListUsers).
func constructorName(r routes.Route) string {
	return central.ConstructorBaseName(r.Handler.Name)
}

// templateImports extracts the template file's Go import declarations by
// parsing its header expressions, mapping alias (or default package name)
// to import path — the same identifier-matching model route discovery uses
// (constitution A3.1).
func templateImports(file *parser.TemplateFile) map[string]string {
	var sb strings.Builder
	sb.WriteString("package p\n")
	// Import declarations live in the file's Go expression nodes (the
	// content between the package line and the first template).
	for _, n := range file.Nodes {
		if g, ok := n.(*parser.TemplateFileGoExpression); ok {
			sb.WriteString(g.Expression.Value)
			sb.WriteString("\n")
		}
	}
	f, err := goparser.ParseFile(token.NewFileSet(), "imports.go", sb.String(), goparser.ImportsOnly)
	if err != nil || f == nil {
		return map[string]string{}
	}
	return routes.FileImports(f)
}

func (r *bindingResolver) pos(rng parser.Range) diag.Position {
	return diag.Position{
		File:  r.file.Filepath,
		Line:  rng.From.Line + 1,
		Col:   rng.From.Col + 1,
		Index: rng.From.Index,
	}
}
