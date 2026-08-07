package routes

import (
	"fmt"
	"go/ast"
	"go/token"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/go-monolith/ghtmx/internal/diag"
)

// flavour describes one supported router's registration idioms. One entry
// per router (solution design D3): explicit and directly testable against
// fixtures, rather than a declarative rule engine.
type flavour struct {
	name string
	// importPrefixes identify the router package by import path prefix.
	importPrefixes []string
	// constructors are functions in the router package returning a router.
	constructors []string
	// paramTypes are type names in the router package that identify a
	// router-typed function parameter.
	paramTypes []string
	// verbMethods maps method names to HTTP verbs.
	verbMethods map[string]Verb
	// methodForms describes Method("GET", ...)-style registration: argument
	// positions for verb, path, and handler (-1 means last).
	methodForms map[string]methodForm
	// handlerLast: the handler is the last argument (gin, fiber);
	// otherwise it is the argument after the path.
	handlerLast bool
	// style is the flavour's path parameter syntax.
	style ParamStyle
	// closureGroups: Route(prefix, func(r)) / Group(func(r)) nesting (chi).
	closureGroups bool
	// valueGroups: Group(prefix, ...) returns a sub-router value
	// (echo, gin, fiber).
	valueGroups bool
	// passthrough methods return the same router (middleware chaining).
	passthrough []string
}

type methodForm struct {
	verbArg, pathArg, handlerArg int // handlerArg -1 means last
}

var flavours = []*flavour{
	{
		name:           "nethttp",
		importPrefixes: []string{"net/http"},
		constructors:   []string{"NewServeMux"},
		paramTypes:     []string{"ServeMux"},
		// Patterns carry the verb: handled specially in handleCall.
		verbMethods: map[string]Verb{},
		style:       BraceStyle,
	},
	{
		name:           "chi",
		importPrefixes: []string{"github.com/go-chi/chi"},
		constructors:   []string{"NewRouter", "NewMux"},
		paramTypes:     []string{"Router", "Mux"},
		verbMethods: map[string]Verb{
			"Get": GET, "Post": POST, "Put": PUT, "Patch": PATCH, "Delete": DELETE,
			"Head": "HEAD", "Options": "OPTIONS",
		},
		methodForms: map[string]methodForm{
			"Method":     {verbArg: 0, pathArg: 1, handlerArg: 2},
			"MethodFunc": {verbArg: 0, pathArg: 1, handlerArg: 2},
		},
		style:         BraceStyle,
		closureGroups: true,
		passthrough:   []string{"With"},
	},
	{
		name:           "echo",
		importPrefixes: []string{"github.com/labstack/echo"},
		constructors:   []string{"New"},
		paramTypes:     []string{"Echo", "Group"},
		verbMethods: map[string]Verb{
			"GET": GET, "POST": POST, "PUT": PUT, "PATCH": PATCH, "DELETE": DELETE,
			"HEAD": "HEAD", "OPTIONS": "OPTIONS",
		},
		methodForms: map[string]methodForm{
			"Add": {verbArg: 0, pathArg: 1, handlerArg: 2},
		},
		style:       ColonStyle,
		valueGroups: true,
	},
	{
		name:           "gin",
		importPrefixes: []string{"github.com/gin-gonic/gin"},
		constructors:   []string{"New", "Default"},
		paramTypes:     []string{"Engine", "RouterGroup"},
		verbMethods: map[string]Verb{
			"GET": GET, "POST": POST, "PUT": PUT, "PATCH": PATCH, "DELETE": DELETE,
			"HEAD": "HEAD", "OPTIONS": "OPTIONS",
		},
		methodForms: map[string]methodForm{
			"Handle": {verbArg: 0, pathArg: 1, handlerArg: -1},
		},
		handlerLast: true,
		style:       ColonStyle,
		valueGroups: true,
		passthrough: []string{"Use"},
	},
	{
		name:           "fiber",
		importPrefixes: []string{"github.com/gofiber/fiber"},
		constructors:   []string{"New"},
		paramTypes:     []string{"App", "Router"},
		verbMethods: map[string]Verb{
			"Get": GET, "Post": POST, "Put": PUT, "Patch": PATCH, "Delete": DELETE,
			"Head": "HEAD", "Options": "OPTIONS",
		},
		methodForms: map[string]methodForm{
			"Add": {verbArg: 0, pathArg: 1, handlerArg: -1},
		},
		handlerLast: true,
		style:       ColonStyle,
		valueGroups: true,
		passthrough: []string{"Use"},
	},
}

func flavourByImport(path string) *flavour {
	for _, f := range flavours {
		for _, prefix := range f.importPrefixes {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				return f
			}
		}
	}
	return nil
}

// importMap maps a file's import aliases (or default names) to import
// paths.
type importMap map[string]string

// FileImports maps a parsed Go file's import aliases (or default package
// names, guessed from the import path) to import paths. Shared with the
// analyzer, which resolves template-side symbols the same way.
func FileImports(file *ast.File) map[string]string {
	return fileImports(file)
}

func fileImports(file *ast.File) importMap {
	out := importMap{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := defaultImportName(path)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == "_" || name == "." {
			continue
		}
		out[name] = path
	}
	return out
}

// defaultImportName guesses the package name of an import path without type
// information: the last path element, skipping version suffixes (chi/v5).
func defaultImportName(path string) string {
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]
	if len(parts) > 1 && strings.HasPrefix(last, "v") {
		if _, err := strconv.Atoi(last[1:]); err == nil {
			last = parts[len(parts)-2]
		}
	}
	return last
}

// binding associates an in-scope variable with a router flavour and the
// path prefix accumulated through groups.
type binding struct {
	fl     *flavour
	prefix string
}

// pendingRoute is a discovered registration awaiting mount-prefix
// resolution at function end.
type pendingRoute struct {
	route Route
	recv  string // base receiver variable name, "" when untracked
}

// mountEdge records r.Mount(prefix, child): routes registered on child move
// under the parent's prefix plus the mount prefix, and then belong to the
// parent for the purpose of further (nested) mounts.
type mountEdge struct {
	parentVar    string
	parentPrefix string
	mountPrefix  string
	child        string
}

// funcState carries the per-function collectors. They are shared across
// group closures so that a Mount in a closure still sees routes registered
// on a variable defined in the enclosing function, and vice versa.
type funcState struct {
	env    map[string]binding
	locals map[string]bool // locally (re)defined identifiers, shadowing package consts
	// recvTypes maps a local variable to the named type it holds, so a
	// method value registered as a handler (h.ListUsers) resolves to
	// Handlers.ListUsers. Populated only from same-file syntax — a
	// parameter's declared type, a var with an explicit type, a
	// composite literal, or new(T) — which is the same conservative
	// single-file inference paramFlavour performs for router variables.
	recvTypes map[string]SymbolRef
	pending   []pendingRoute
	mounts    []mountEdge
}

func newFuncState() *funcState {
	return &funcState{
		env:       map[string]binding{},
		locals:    map[string]bool{},
		recvTypes: map[string]SymbolRef{},
	}
}

// Discover walks the loaded packages and produces the route table plus
// diagnostics. Escape-hatch annotations are merged; verb+path conflicts are
// reported per FR-050 with both sites named.
func Discover(pkgs []*Package, sink *diag.Sink) *Table {
	table := NewTable()
	add := func(r Route) {
		if existing, ok := table.Add(r); !ok {
			sink.Add(diag.DuplicateRoute,
				diag.Position{File: r.Pos.File, Line: r.Pos.Line, Col: r.Pos.Col},
				fmt.Sprintf("duplicate route %s %s: already registered at %s", r.Verb, r.Path, existing.Pos),
				"remove one of the registrations")
		}
	}
	for _, pkg := range pkgs {
		consts := packageStringConsts(pkg)
		typeNames := packageTypeNames(pkg)
		for _, file := range pkg.Files {
			imports := fileImports(file)
			for _, r := range collectAnnotations(pkg, file, imports, typeNames, sink) {
				add(r)
			}
			d := &discoverer{pkg: pkg, imports: imports, consts: consts, sink: sink}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				st := newFuncState()
				if fn.Type.Params != nil {
					for _, field := range fn.Type.Params.List {
						fl := d.paramFlavour(field.Type)
						recv, hasRecv := d.namedTypeRef(field.Type)
						for _, name := range field.Names {
							st.locals[name.Name] = true
							if fl != nil {
								st.env[name.Name] = binding{fl: fl}
							}
							// func routes(h *Handlers): h.ListUsers is a
							// method value on a type this file names.
							if hasRecv {
								st.recvTypes[name.Name] = recv
							}
						}
					}
				}
				d.walkBody(fn.Body, st, st.env)
				for _, r := range applyMounts(st) {
					add(r.route)
				}
			}
		}
	}
	reportMultiPathHandlers(table, sink)
	return table
}

// reportMultiPathHandlers emits GHTMX-W0105 when one handler symbol is
// registered for the same effective verb at more than one path — for
// example a route both discovered and declared by annotation. The table
// keys conflicts on (verb, path), so these pairs coexist without E0401,
// and Lookup silently resolves a template binding to whichever route
// wins — a URL the author may not expect. Warning-class: a project that
// genuinely serves one handler at several URLs can turn it off.
func reportMultiPathHandlers(table *Table, sink *diag.Sink) {
	seen := map[SymbolRef]bool{}
	for _, first := range table.All() {
		if seen[first.Handler] {
			continue
		}
		seen[first.Handler] = true
		rs := table.ByHandler(first.Handler)
		if len(rs) < 2 {
			continue
		}
		verb, ok := handlerVerbConflict(rs)
		if !ok {
			continue
		}
		winner, _ := table.Lookup(verb, first.Handler)
		// Only the routes actually serving the conflicting verb belong in
		// the message; a route on an unrelated verb is not part of the
		// ambiguity.
		var conflicting []Route
		for _, r := range rs {
			if servesVerb(r, verb) {
				conflicting = append(conflicting, r)
			}
		}
		sites := make([]string, 0, len(conflicting))
		for _, r := range conflicting {
			sites = append(sites, fmt.Sprintf("%s %s (%s)", verbLabel(r.Verb), r.Path, r.Pos))
		}
		resolution := fmt.Sprintf("a %s template binding resolves to %s", verb, winner.Path)
		if verb == AnyVerb {
			// No binding is literally ANY; every concrete verb resolves
			// through the same first any-verb route.
			resolution = fmt.Sprintf("template bindings resolve to %s", winner.Path)
		}
		// Anchored at the last conflicting registration in discovery
		// order, matching how E0401 blames the later site.
		last := conflicting[len(conflicting)-1]
		sink.Add(diag.MultiPathHandler,
			diag.Position{File: last.Pos.File, Line: max(last.Pos.Line, 1), Col: max(last.Pos.Col, 1)},
			fmt.Sprintf("handler %s is registered for the same verb at more than one path: %s; %s",
				first.Handler, strings.Join(sites, ", "), resolution),
			"remove one registration, or silence this check with GHTMX-W0105=off if one handler deliberately serves several URLs")
	}
}

// servesVerb reports whether the route can serve requests for verb; an
// any-verb route serves every verb.
func servesVerb(r Route, verb Verb) bool {
	return r.Verb == verb || (verb != AnyVerb && r.Verb == AnyVerb)
}

// handlerVerbConflict reports the first verb (in BindableVerbs order,
// then any-verb) that the handler's routes serve at more than one
// DISTINCT path. Same-path pairs are not conflicts — an exact-verb
// route pinning one verb of an any-verb registration at the same path
// resolves every lookup to that one path.
func handlerVerbConflict(rs []Route) (Verb, bool) {
	verbs := append(append([]Verb{}, BindableVerbs...), AnyVerb)
	for _, v := range verbs {
		exact := 0
		paths := map[string]bool{}
		for _, r := range rs {
			if !servesVerb(r, v) {
				continue
			}
			if r.Verb == v {
				exact++
			}
			paths[r.Path] = true
		}
		if exact >= 1 && len(paths) >= 2 {
			return v, true
		}
	}
	return "", false
}

func verbLabel(v Verb) string {
	if v == AnyVerb {
		return "ANY"
	}
	return string(v)
}

// applyMounts moves routes registered on mounted variables under their
// mount points. Nested mounts resolve by following the parent chain: after
// an edge is applied the route belongs to the parent variable, whose own
// mount edge (if any) applies on the next pass. Iterations are bounded so
// a pathological mount cycle terminates.
func applyMounts(st *funcState) []pendingRoute {
	edgeByChild := make(map[string]mountEdge, len(st.mounts))
	for _, m := range st.mounts {
		if _, dup := edgeByChild[m.child]; !dup {
			edgeByChild[m.child] = m
		}
	}
	for range len(st.mounts) + 1 {
		changed := false
		for i := range st.pending {
			p := &st.pending[i]
			if p.recv == "" {
				continue
			}
			edge, ok := edgeByChild[p.recv]
			if !ok {
				continue
			}
			p.route.Path = JoinPaths(JoinPaths(edge.parentPrefix, edge.mountPrefix), p.route.Path)
			p.route.OriginalPath = p.route.Path
			p.recv = edge.parentVar
			changed = true
			// Self-mount guard: a variable "mounted" onto itself would loop.
			if p.recv == edge.child {
				p.recv = ""
			}
		}
		if !changed {
			break
		}
	}
	return st.pending
}

// packageStringConsts collects package-level `const Name = "literal"`
// declarations for path resolution.
func packageStringConsts(pkg *Package) map[string]string {
	out := map[string]string{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != len(vs.Values) {
					continue
				}
				for i, name := range vs.Names {
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if v, err := strconv.Unquote(lit.Value); err == nil {
							out[name.Name] = v
						}
					}
				}
			}
		}
	}
	return out
}

type discoverer struct {
	pkg     *Package
	imports importMap
	consts  map[string]string
	sink    *diag.Sink
}

// paramFlavour matches a parameter type expression against the known
// router types, syntactically.
func (d *discoverer) paramFlavour(expr ast.Expr) *flavour {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}
	path, ok := d.imports[pkgIdent.Name]
	if !ok {
		return nil
	}
	fl := flavourByImport(path)
	if fl == nil {
		return nil
	}
	if slices.Contains(fl.paramTypes, sel.Sel.Name) {
		return fl
	}
	return nil
}

// walkBody walks one function body (or group closure) in source order,
// tracking router variable bindings into env and collecting registrations
// into the shared function state.
func (d *discoverer) walkBody(body *ast.BlockStmt, st *funcState, env map[string]binding) {
	handledClosures := map[*ast.FuncLit]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			// Group closures are walked explicitly with their own scope;
			// other closures (goroutines, handlers) share the enclosing
			// bindings.
			return !handledClosures[n]
		case *ast.DeclStmt:
			// Local var/const declarations shadow package consts.
			if gd, ok := n.Decl.(*ast.GenDecl); ok {
				for _, spec := range gd.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						// var h Handlers / var h *pkg.Handlers: an
						// explicit type names the receiver directly;
						// otherwise the initialiser may.
						recv, hasRecv := SymbolRef{}, false
						if vs.Type != nil {
							recv, hasRecv = d.namedTypeRef(vs.Type)
						}
						for i, name := range vs.Names {
							st.locals[name.Name] = true
							value := recv
							ok := hasRecv
							if !ok && i < len(vs.Values) {
								value, ok = d.receiverTypeOf(vs.Values[i])
							}
							if ok {
								st.recvTypes[name.Name] = value
							} else {
								delete(st.recvTypes, name.Name)
							}
						}
					}
				}
			}
			return true
		case *ast.AssignStmt:
			d.handleAssign(n, st, env)
			return true
		case *ast.CallExpr:
			d.handleCall(n, st, env, handledClosures)
			return true
		}
		return true
	})
}

// handleAssign tracks router bindings: constructor calls, group creation,
// middleware chains, and plain aliases. A name rebound to a non-router
// expression is conservatively unbound, and every defined name is recorded
// as a local (it shadows any package const of the same name).
func (d *discoverer) handleAssign(n *ast.AssignStmt, st *funcState, env map[string]binding) {
	if len(n.Lhs) != len(n.Rhs) {
		// Multi-value assignment from a single call: nothing to track, but
		// the names still shadow.
		for _, lhs := range n.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" {
				st.locals[id.Name] = true
				delete(env, id.Name)
			}
		}
		return
	}
	for i, rhs := range n.Rhs {
		lhs, ok := n.Lhs[i].(*ast.Ident)
		if !ok || lhs.Name == "_" {
			continue
		}
		st.locals[lhs.Name] = true
		// Receiver tracking is independent of router tracking: a
		// rebinding replaces or clears whichever type the name held.
		if recv, ok := d.receiverTypeOf(rhs); ok {
			st.recvTypes[lhs.Name] = recv
		} else {
			delete(st.recvTypes, lhs.Name)
		}
		if b, ok := d.evalRouterExpr(rhs, env); ok {
			env[lhs.Name] = b
			continue
		}
		// Rebound to something that is not a router: stop tracking it.
		delete(env, lhs.Name)
	}
}

// evalRouterExpr evaluates an expression that may produce a router: a
// tracked variable, a constructor call, a Group(...) call, or a
// passthrough chain.
func (d *discoverer) evalRouterExpr(expr ast.Expr, env map[string]binding) (binding, bool) {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return d.evalRouterExpr(e.X, env)
	case *ast.Ident:
		b, ok := env[e.Name]
		return b, ok
	case *ast.CallExpr:
		sel, ok := e.Fun.(*ast.SelectorExpr)
		if !ok {
			return binding{}, false
		}
		// Constructor: pkg.NewRouter() etc.
		if pkgIdent, ok := sel.X.(*ast.Ident); ok {
			if path, isImport := d.imports[pkgIdent.Name]; isImport {
				if fl := flavourByImport(path); fl != nil {
					if slices.Contains(fl.constructors, sel.Sel.Name) {
						return binding{fl: fl}, true
					}
				}
				return binding{}, false
			}
		}
		// Method on a tracked router: Group / passthrough.
		recv, ok := d.evalRouterExpr(sel.X, env)
		if !ok {
			return binding{}, false
		}
		if recv.fl.valueGroups && sel.Sel.Name == "Group" && len(e.Args) > 0 {
			if prefix, ok := d.stringArg(e.Args[0], nil); ok {
				return binding{fl: recv.fl, prefix: JoinPaths(recv.prefix, prefix)}, true
			}
			return binding{}, false
		}
		if slices.Contains(recv.fl.passthrough, sel.Sel.Name) {
			return recv, true
		}
		return binding{}, false
	}
	return binding{}, false
}

// handleCall recognizes registration calls on tracked receivers.
func (d *discoverer) handleCall(call *ast.CallExpr, st *funcState, env map[string]binding, handledClosures map[*ast.FuncLit]bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	method := sel.Sel.Name

	// net/http package-level registration: http.HandleFunc / http.Handle on
	// the DefaultServeMux.
	if pkgIdent, ok := sel.X.(*ast.Ident); ok {
		if path, isImport := d.imports[pkgIdent.Name]; isImport && path == "net/http" && (method == "HandleFunc" || method == "Handle") {
			d.registerNetHTTP(call, st, binding{fl: flavours[0]}, "")
			return
		}
	}

	recv, ok := d.evalRouterExpr(sel.X, env)
	if !ok {
		return
	}
	recvName := receiverBaseName(sel.X)
	fl := recv.fl

	switch {
	case fl.name == "nethttp" && (method == "HandleFunc" || method == "Handle"):
		d.registerNetHTTP(call, st, recv, recvName)

	case fl.closureGroups && method == "Route" && len(call.Args) == 2:
		prefix, ok := d.stringArg(call.Args[0], st.locals)
		if !ok {
			d.errUnresolvable(call.Args[0], "the group prefix is not a string literal or package constant")
			return
		}
		d.walkClosureGroup(call.Args[1], binding{fl: fl, prefix: JoinPaths(recv.prefix, prefix)}, st, env, handledClosures)

	case fl.closureGroups && method == "Group" && len(call.Args) == 1:
		d.walkClosureGroup(call.Args[0], recv, st, env, handledClosures)

	case fl.closureGroups && method == "Mount" && len(call.Args) == 2:
		prefix, ok := d.stringArg(call.Args[0], st.locals)
		if !ok {
			d.errUnresolvable(call.Args[0], "the mount prefix is not a string literal or package constant")
			return
		}
		child, ok := call.Args[1].(*ast.Ident)
		if !ok {
			d.errUnresolvable(call.Args[1], "only a same-function router variable can be mounted")
			return
		}
		if _, tracked := env[child.Name]; !tracked {
			d.errUnresolvable(call.Args[1], fmt.Sprintf("mounted router %q is not a same-function router variable", child.Name))
			return
		}
		st.mounts = append(st.mounts, mountEdge{
			parentVar:    recvName,
			parentPrefix: recv.prefix,
			mountPrefix:  prefix,
			child:        child.Name,
		})

	default:
		if verb, ok := fl.verbMethods[method]; ok {
			d.registerVerbCall(call, st, recv, recvName, verb, 0, flHandlerArg(fl, 1))
			return
		}
		if form, ok := fl.methodForms[method]; ok {
			if len(call.Args) <= form.verbArg || len(call.Args) <= form.pathArg {
				return // Malformed call; the Go compiler reports it.
			}
			verbStr, ok := d.stringArg(call.Args[form.verbArg], st.locals)
			if !ok {
				d.errUnresolvable(call.Args[form.verbArg], "the method is not a string literal")
				return
			}
			d.registerVerbCall(call, st, recv, recvName, Verb(strings.ToUpper(verbStr)), form.pathArg, form.handlerArg)
		}
	}
}

func flHandlerArg(fl *flavour, afterPath int) int {
	if fl.handlerLast {
		return -1
	}
	return afterPath
}

// walkClosureGroup walks a chi Route/Group closure with the composed
// prefix, sharing the function state so mounts and registrations compose
// across the closure boundary. A named function reference is reported
// rather than silently registered with a wrong prefix (FR-051).
func (d *discoverer) walkClosureGroup(arg ast.Expr, group binding, st *funcState, env map[string]binding, handledClosures map[*ast.FuncLit]bool) {
	closure, ok := arg.(*ast.FuncLit)
	if !ok {
		d.errUnresolvable(arg, "group routes must be registered in an inline closure for prefix composition")
		return
	}
	handledClosures[closure] = true
	childEnv := make(map[string]binding, len(env)+1)
	maps.Copy(childEnv, env)
	if closure.Type.Params != nil && len(closure.Type.Params.List) > 0 {
		for _, name := range closure.Type.Params.List[0].Names {
			childEnv[name.Name] = group
		}
	}
	d.walkBody(closure.Body, st, childEnv)
}

// registerNetHTTP handles ServeMux method-and-path patterns (FR-011):
// mux.HandleFunc("GET /users/{id}", handlers.GetUser).
func (d *discoverer) registerNetHTTP(call *ast.CallExpr, st *funcState, recv binding, recvName string) {
	if len(call.Args) < 2 {
		return
	}
	pattern, ok := d.stringArg(call.Args[0], st.locals)
	if !ok {
		d.errUnresolvable(call.Args[0], "the route pattern is not a string literal or package constant")
		return
	}
	verb := AnyVerb
	path := pattern
	if m, p, found := strings.Cut(pattern, " "); found && isUpperAlpha(m) {
		verb = Verb(m)
		path = p
	}
	d.emit(call, st, recv, recvName, verb, path, call.Args[1])
}

func (d *discoverer) registerVerbCall(call *ast.CallExpr, st *funcState, recv binding, recvName string, verb Verb, pathArg, handlerArg int) {
	if len(call.Args) <= pathArg {
		return
	}
	path, ok := d.stringArg(call.Args[pathArg], st.locals)
	if !ok {
		d.errUnresolvable(call.Args[pathArg], "the route path is not a string literal or package constant")
		return
	}
	var handlerExpr ast.Expr
	if handlerArg == -1 {
		// The handler is the last argument, which must come after the path.
		if len(call.Args) <= pathArg+1 {
			return // No handler present; the router itself rejects this.
		}
		handlerExpr = call.Args[len(call.Args)-1]
	} else {
		if len(call.Args) <= handlerArg {
			return
		}
		handlerExpr = call.Args[handlerArg]
	}
	d.emit(call, st, recv, recvName, verb, path, handlerExpr)
}

func (d *discoverer) emit(call *ast.CallExpr, st *funcState, recv binding, recvName string, verb Verb, path string, handlerExpr ast.Expr) {
	handler, ok := d.resolveHandler(handlerExpr, st)
	if !ok {
		d.errUnresolvable(handlerExpr, "the handler cannot be resolved to a package-level symbol or a method on a locally-typed receiver")
		return
	}
	// Only compose through JoinPaths when a group prefix exists: a raw
	// registered path passes through untouched, since ServeMux patterns may
	// legitimately carry a host ("GET example.com/x").
	full := path
	if recv.prefix != "" {
		full = JoinPaths(recv.prefix, path)
	}
	normalized, params := NormalizePath(full, recv.fl.style)
	st.pending = append(st.pending, pendingRoute{
		recv: recvName,
		route: Route{
			Verb:         verb,
			Path:         normalized,
			OriginalPath: full,
			Params:       params,
			Handler:      handler,
			Pos:          position(d.pkg.Fset, call.Pos()),
			Origin:       Discovered,
			Recognizer:   recv.fl.name,
		},
	})
}

// resolveHandler maps a handler expression to a package-qualified symbol by
// identifier matching against the file's imports (constitution A3.1).
// Single-argument wrapping calls (middleware, http.HandlerFunc conversions)
// are seen through to the underlying symbol (FR-014).
func (d *discoverer) resolveHandler(expr ast.Expr, st *funcState) (SymbolRef, bool) {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return d.resolveHandler(e.X, st)
	case *ast.Ident:
		return SymbolRef{PkgPath: d.pkg.PkgPath, Name: e.Name}, true
	case *ast.SelectorExpr:
		baseIdent, ok := e.X.(*ast.Ident)
		if !ok {
			// A selector on anything but a bare identifier — a field
			// chain (s.handlers.Show) or a call result — needs type
			// information this scan does not have.
			return SymbolRef{}, false
		}
		// Imports win: a local named like an import alias resolved as
		// the import before method values were understood, and that
		// must not change.
		if path, ok := d.imports[baseIdent.Name]; ok {
			return SymbolRef{PkgPath: path, Name: e.Sel.Name}, true
		}
		// A method value on a receiver whose type this file names.
		if recv, ok := st.recvTypes[baseIdent.Name]; ok {
			return SymbolRef{PkgPath: recv.PkgPath, Name: recv.Name + "." + e.Sel.Name}, true
		}
		return SymbolRef{}, false
	case *ast.CallExpr:
		// Conversions and single-argument middleware wraps: mw(h),
		// http.HandlerFunc(h).
		if len(e.Args) == 1 {
			return d.resolveHandler(e.Args[0], st)
		}
		return SymbolRef{}, false
	}
	return SymbolRef{}, false
}

// namedTypeRef recovers the named type an expression denotes, for
// receiver tracking: T and *T resolve to this package's T, pkg.T and
// *pkg.T to the imported package's T. Anything else — an interface
// literal, a generic instantiation, a func type — yields nothing, and
// its method values keep failing with GHTMX-E0402.
func (d *discoverer) namedTypeRef(expr ast.Expr) (SymbolRef, bool) {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return d.namedTypeRef(e.X)
	case *ast.StarExpr:
		return d.namedTypeRef(e.X)
	case *ast.Ident:
		if !token.IsExported(e.Name) && !isTypeNameCandidate(e.Name) {
			return SymbolRef{}, false
		}
		return SymbolRef{PkgPath: d.pkg.PkgPath, Name: e.Name}, true
	case *ast.SelectorExpr:
		pkgIdent, ok := e.X.(*ast.Ident)
		if !ok {
			return SymbolRef{}, false
		}
		path, ok := d.imports[pkgIdent.Name]
		if !ok {
			return SymbolRef{}, false
		}
		return SymbolRef{PkgPath: path, Name: e.Sel.Name}, true
	}
	return SymbolRef{}, false
}

// isTypeNameCandidate rejects the predeclared type names, which carry no
// methods worth registering and would otherwise turn a `var s string`
// into a receiver.
func isTypeNameCandidate(name string) bool {
	switch name {
	case "any", "bool", "byte", "complex64", "complex128", "error", "float32", "float64",
		"int", "int8", "int16", "int32", "int64", "rune", "string",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		return false
	}
	return true
}

// receiverTypeOf recovers the named type a value expression constructs,
// for `h := Handlers{...}`, `&Handlers{...}`, and `new(Handlers)`. A
// constructor call (NewHandlers()) yields nothing: its return type is
// not knowable from syntax alone.
func (d *discoverer) receiverTypeOf(expr ast.Expr) (SymbolRef, bool) {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return d.receiverTypeOf(e.X)
	case *ast.UnaryExpr:
		if e.Op != token.AND {
			return SymbolRef{}, false
		}
		return d.receiverTypeOf(e.X)
	case *ast.CompositeLit:
		if e.Type == nil {
			return SymbolRef{}, false
		}
		return d.namedTypeRef(e.Type)
	case *ast.CallExpr:
		// new(T) is the one call whose result type is syntactic.
		if fn, ok := e.Fun.(*ast.Ident); ok && fn.Name == "new" && len(e.Args) == 1 && !d.pkgDeclares(fn.Name) {
			return d.namedTypeRef(e.Args[0])
		}
		return SymbolRef{}, false
	}
	return SymbolRef{}, false
}

// pkgDeclares reports whether the package declares the name itself,
// which is how a local `func new(...)` is kept from being read as the
// builtin.
func (d *discoverer) pkgDeclares(name string) bool {
	for _, file := range d.pkg.Files {
		for _, decl := range file.Decls {
			switch dcl := decl.(type) {
			case *ast.FuncDecl:
				if dcl.Recv == nil && dcl.Name.Name == name {
					return true
				}
			case *ast.GenDecl:
				for _, spec := range dcl.Specs {
					switch s := spec.(type) {
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.Name == name {
								return true
							}
						}
					case *ast.TypeSpec:
						if s.Name.Name == name {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// stringArg resolves a string literal or package-level string constant.
// A package const shadowed by a local variable is not resolvable: locals
// are dynamic by assumption.
func (d *discoverer) stringArg(expr ast.Expr, locals map[string]bool) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(e.Value)
		return v, err == nil
	case *ast.Ident:
		if locals[e.Name] {
			return "", false
		}
		v, ok := d.consts[e.Name]
		return v, ok
	}
	return "", false
}

func (d *discoverer) errUnresolvable(at ast.Expr, why string) {
	pos := position(d.pkg.Fset, at.Pos())
	d.sink.Add(diag.UnresolvableRoute,
		diag.Position{File: pos.File, Line: pos.Line, Col: pos.Col},
		"route registration cannot be resolved statically: "+why,
		"declare the route explicitly with a //ghtmx:route annotation")
}

// receiverBaseName resolves a receiver expression to its base variable
// name, seeing through passthrough call chains: sub.With(mw).Get(...) has
// base variable sub. Untracked shapes yield "".
func isUpperAlpha(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func receiverBaseName(expr ast.Expr) string {
	for {
		switch e := expr.(type) {
		case *ast.Ident:
			return e.Name
		case *ast.ParenExpr:
			expr = e.X
		case *ast.CallExpr:
			sel, ok := e.Fun.(*ast.SelectorExpr)
			if !ok {
				return ""
			}
			expr = sel.X
		default:
			return ""
		}
	}
}
